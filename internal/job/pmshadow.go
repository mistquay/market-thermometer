package job

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/pmstrat"
	"market-thermometer/internal/store"
)

// PMShadow 是短期涨跌盘公允概率策略的影子模式：挂在盘口采集器的每轮回调上，
// 对进行中的市场算 P(Up)，与盘口（卖一 + 手续费）比较，超过阈值时只记录信号，不下单。
type PMShadow struct {
	store    *store.Store
	now      func() time.Time
	logf     func(string, ...any)
	name     string
	minEdge  float64       // 记录信号的最小预期收益（每股，美元）
	cooldown time.Duration // 同一市场同一结果两次信号的最小间隔
	keep     time.Duration // 内存里保留多久的现价

	mu       sync.Mutex
	spot     map[string][]pmstrat.Point // 品种 → 现价（升序）
	loaded   map[string]bool
	basis    map[string]float64 // 资产 → 币安相对 Chainlink 的溢价
	basisN   map[string]int
	basisAt  time.Time
	kCache   map[string]kInfo // 资产/开始时间 → 起始价
	lastSig  map[string]time.Time
	evals    []PMEval
	signals  int64
	lastErr  string
	errAt    time.Time
	sigmaOf  map[string]float64
	lastTick time.Time
}

type kInfo struct {
	k     float64
	src   string // exact / spot / bar
	errBp float64
	at    time.Time
}

// PMEval 是一个市场最近一次估值（供页面展示）。
type PMEval struct {
	Target PMBookTarget `json:"target"`
	OK     bool         `json:"ok"`
	Reason string       `json:"reason,omitempty"`
	P      float64      `json:"p"` // P(Up)
	BidUp  float64      `json:"bidUp"`
	AskUp  float64      `json:"askUp"`
	AskDn  float64      `json:"askDn"`
	EdgeUp float64      `json:"edgeUp"`
	EdgeDn float64      `json:"edgeDn"`
	K      float64      `json:"k"`
	KSrc   string       `json:"kSrc"`
	Spot   float64      `json:"spot"` // 当前价（Chainlink 口径）
	SdBp   float64      `json:"sdBp"`
	Tau    int64        `json:"tau"`
	At     time.Time    `json:"at"`
}

func NewPMShadow(st *store.Store, minEdge float64) *PMShadow {
	if minEdge <= 0 {
		minEdge = 0.03
	}
	return &PMShadow{store: st, now: time.Now, logf: log.Printf, name: "updown-fair", minEdge: minEdge, cooldown: 30 * time.Second,
		keep: 70 * time.Minute, spot: map[string][]pmstrat.Point{}, loaded: map[string]bool{}, basis: map[string]float64{},
		basisN: map[string]int{}, kCache: map[string]kInfo{}, lastSig: map[string]time.Time{}, sigmaOf: map[string]float64{}}
}

func assetOf(series string) string { a, _, _ := strings.Cut(series, "-"); return a }

// Tick 是盘口采集器的回调。
func (p *PMShadow) Tick(ctx context.Context, now time.Time, targets []PMBookTarget, books []ds.Book, spots []store.PMSpot) {
	if err := p.tick(ctx, now, targets, books, spots); err != nil {
		p.mu.Lock()
		first := p.lastErr == ""
		p.lastErr, p.errAt = err.Error(), now
		p.mu.Unlock()
		if first {
			p.logf("策略影子模式：%v", err)
		}
	}
}

func (p *PMShadow) tick(ctx context.Context, now time.Time, targets []PMBookTarget, books []ds.Book, spots []store.PMSpot) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastTick = now
	// 现价：首次从库里补最近 keep 的数据，之后用每轮的现价追加
	for _, t := range targets {
		if t.Symbol == "" || p.loaded[t.Symbol] {
			continue
		}
		pts, err := p.store.PMSpots(ctx, t.Symbol, now.Add(-p.keep).UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
		for _, x := range pts {
			p.spot[t.Symbol] = append(p.spot[t.Symbol], pmstrat.Point{T: x.T, P: x.P})
		}
		p.loaded[t.Symbol] = true
	}
	for _, x := range spots {
		s := p.spot[x.Symbol]
		if n := len(s); n == 0 || x.T > s[n-1].T {
			p.spot[x.Symbol] = append(s, pmstrat.Point{T: x.T, P: x.P})
		}
	}
	cut := now.Add(-p.keep).UnixMilli()
	for sym, s := range p.spot {
		i := 0
		for i < len(s) && s[i].T < cut {
			i++
		}
		p.spot[sym] = s[i:]
	}
	if now.Sub(p.basisAt) >= 2*time.Minute {
		if err := p.refreshBasis(ctx, targets, now); err != nil {
			return err
		}
		p.basisAt = now
	}
	bookOf := map[string]ds.Book{}
	for _, b := range books {
		bookOf[b.Token] = b
	}
	var sigs []store.PMSignal
	p.evals = p.evals[:0]
	for _, t := range targets {
		e := p.eval(ctx, now, t, bookOf[t.Token])
		p.evals = append(p.evals, e)
		if !e.OK {
			continue
		}
		for _, side := range []struct {
			outcome    int
			prob, edge float64
			ask, size  float64
		}{{0, e.P, e.EdgeUp, e.AskUp, sizeAt(bookOf[t.Token].Asks)}, {1, 1 - e.P, e.EdgeDn, e.AskDn, sizeAt(bookOf[t.Token].Bids)}} {
			key := fmt.Sprintf("%s/%d", t.Cond, side.outcome)
			if side.edge < p.minEdge || now.Sub(p.lastSig[key]) < p.cooldown {
				continue
			}
			p.lastSig[key] = now
			sigs = append(sigs, store.PMSignal{T: now.UnixMilli(), Strategy: p.name, Slug: t.Slug, Cond: t.Cond, Outcome: side.outcome,
				P: side.prob, Ask: side.ask, AskSize: side.size, FeeRate: t.Fee, Edge: side.edge, K: e.K, KSrc: e.KSrc, SdBp: e.SdBp,
				Tau: e.Tau, Spot: e.Spot, Basis: p.basis[assetOf(t.Series)]})
		}
	}
	for k, at := range p.lastSig {
		if now.Sub(at) > time.Hour {
			delete(p.lastSig, k)
		}
	}
	if err := p.store.AddPMSignals(context.WithoutCancel(ctx), sigs); err != nil {
		return err
	}
	p.signals += int64(len(sigs))
	return nil
}

func sizeAt(ls []ds.BookLevel) float64 {
	if len(ls) == 0 {
		return 0
	}
	return ls[0].Size
}

// refreshBasis 用最近已结算的起始价校准各资产的基差：起始时刻前 60 秒的币安均价 / 起始价 − 1，取中位数。
// 现价（2.5 秒）不够时退回 1 分钟 K 线的 OHLC 均值。
func (p *PMShadow) refreshBasis(ctx context.Context, targets []PMBookTarget, now time.Time) error {
	seen := map[string]bool{}
	for _, t := range targets {
		a := assetOf(t.Series)
		if seen[a] || t.Symbol == "" {
			continue
		}
		seen[a] = true
		pts, err := p.store.PMRecentStartPrices(ctx, a, 24, now)
		if err != nil {
			return err
		}
		var rel []float64
		for _, x := range pts {
			m, src, err := p.binanceTwap(ctx, t.Symbol, x.Start)
			if err != nil {
				return err
			}
			if src != "" {
				rel = append(rel, m/x.Price-1)
			}
			if len(rel) >= 12 {
				break
			}
		}
		if len(rel) > 0 {
			p.basis[a], p.basisN[a] = pmstrat.Median(rel), len(rel)
		}
	}
	return nil
}

// binanceTwap 返回 t（Unix 秒）之前 60 秒的币安均价：优先用 2.5 秒现价（至少 15 个点），否则用 1 分钟 K 线的 OHLC 均值。
func (p *PMShadow) binanceTwap(ctx context.Context, sym string, t int64) (float64, string, error) {
	if m, n := pmstrat.TwapBefore(p.spot[sym], t, 60); n >= 15 {
		return m, "spot", nil
	}
	pts, err := p.store.PMSpots(ctx, sym, (t-60)*1000, t*1000-1)
	if err != nil {
		return 0, "", err
	}
	if len(pts) >= 15 {
		var sum float64
		for _, x := range pts {
			sum += x.P
		}
		return sum / float64(len(pts)), "spot", nil
	}
	ks, err := p.store.Klines(ctx, sym, "1m", (t-60)*1000, t*1000)
	if err != nil || len(ks) == 0 {
		return 0, "", err
	}
	k := ks[0]
	return (k.Open + k.High + k.Low + k.Close) / 4, "bar", nil
}

// startPrice 返回市场起始价：同一时刻的已结算起始价（精确），否则用开始前 60 秒的币安均价按基差换算（估计）。
func (p *PMShadow) startPrice(ctx context.Context, asset, sym string, start int64, now time.Time) (kInfo, error) {
	key := fmt.Sprintf("%s/%d", asset, start)
	if k, ok := p.kCache[key]; ok && (k.src == "exact" || now.Sub(k.at) < 30*time.Second) {
		return k, nil
	}
	exact, err := p.store.PMStartPrice(ctx, asset, start)
	if err != nil {
		return kInfo{}, err
	}
	var k kInfo
	if exact > 0 {
		k = kInfo{k: exact, src: "exact", at: now}
	} else if m, src, err := p.binanceTwap(ctx, sym, start); err != nil {
		return kInfo{}, err
	} else if src != "" && p.basisN[asset] > 0 {
		// 误差：2.5 秒现价的 60 秒均值约 0.5bp，1 分钟 K 线约 2bp（docs/polymarket.md §8）
		e := 0.5
		if src == "bar" {
			e = 2
		}
		k = kInfo{k: m / (1 + p.basis[asset]), src: src, errBp: e, at: now}
	}
	if k.k > 0 {
		p.kCache[key] = k
	}
	for kk, v := range p.kCache {
		if now.Sub(v.at) > 3*time.Hour {
			delete(p.kCache, kk)
		}
	}
	return k, nil
}

func (p *PMShadow) eval(ctx context.Context, now time.Time, t PMBookTarget, b ds.Book) PMEval {
	e := PMEval{Target: t, At: now}
	switch {
	case now.Before(t.Start):
		e.Reason = "未开始"
		return e
	case !now.Before(t.End):
		e.Reason = "已结束"
		return e
	case t.Symbol == "":
		e.Reason = "没有现价品种"
		return e
	}
	a := assetOf(t.Series)
	if p.basisN[a] == 0 {
		e.Reason = "基差未校准"
		return e
	}
	k, err := p.startPrice(ctx, a, t.Symbol, t.Start.Unix(), now)
	if err != nil || k.k <= 0 {
		e.Reason = "起始价未知"
		return e
	}
	e.K, e.KSrc = k.k, k.src
	recent := p.spot[t.Symbol]
	i := len(recent)
	for i > 0 && recent[i-1].T > now.Add(-30*time.Minute).UnixMilli() {
		i--
	}
	sigma := pmstrat.Sigma(recent[i:])
	if sigma <= 0 {
		e.Reason = "波动率数据不足"
		return e
	}
	p.sigmaOf[t.Symbol] = sigma
	r, ok := pmstrat.ProbUp(pmstrat.Inputs{Now: now.Unix(), Start: t.Start.Unix(), End: t.End.Unix(), K: k.k, KErrBp: k.errBp,
		Basis: p.basis[a], Sigma: sigma, Spot: recent})
	if !ok {
		e.Reason = "现价中断"
		return e
	}
	e.OK, e.P, e.SdBp, e.Tau = true, r.P, r.SdBp, r.Tau
	if n := len(recent); n > 0 {
		e.Spot = recent[n-1].P / (1 + p.basis[a])
	}
	e.EdgeUp, e.EdgeDn = math.Inf(-1), math.Inf(-1)
	if len(b.Asks) > 0 {
		e.AskUp = b.Asks[0].Price
		e.EdgeUp = pmstrat.Edge(r.P, e.AskUp, t.Fee)
	}
	if len(b.Bids) > 0 {
		e.BidUp = b.Bids[0].Price
		e.AskDn = 1 - e.BidUp // Down 的卖一 = 1 − Up 的买一
		e.EdgeDn = pmstrat.Edge(1-r.P, e.AskDn, t.Fee)
	}
	return e
}

// PMShadowStatus 是影子模式状态。
type PMShadowStatus struct {
	Strategy string             `json:"strategy"`
	MinEdge  float64            `json:"minEdge"`
	Evals    []PMEval           `json:"evals"`
	Signals  int64              `json:"signals"` // 本进程记录的信号数
	Basis    map[string]float64 `json:"basis"`
	BasisN   map[string]int     `json:"basisN"`
	Sigma    map[string]float64 `json:"sigma"` // 每 √秒
	LastTick time.Time          `json:"lastTick,omitzero"`
	LastErr  string             `json:"lastErr,omitempty"`
	ErrAt    time.Time          `json:"errAt,omitzero"`
}

func (p *PMShadow) Status() PMShadowStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := func(m map[string]float64) map[string]float64 {
		out := map[string]float64{}
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	n := map[string]int{}
	for k, v := range p.basisN {
		n[k] = v
	}
	evals := append([]PMEval{}, p.evals...)
	for i := range evals { // JSON 不支持 ±Inf
		if math.IsInf(evals[i].EdgeUp, 0) {
			evals[i].EdgeUp = 0
		}
		if math.IsInf(evals[i].EdgeDn, 0) {
			evals[i].EdgeDn = 0
		}
	}
	return PMShadowStatus{Strategy: p.name, MinEdge: p.minEdge, Evals: evals, Signals: p.signals, Basis: cp(p.basis), BasisN: n,
		Sigma: cp(p.sigmaOf), LastTick: p.lastTick, LastErr: p.lastErr, ErrAt: p.errAt}
}
