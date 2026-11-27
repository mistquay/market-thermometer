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
	"market-thermometer/internal/pmsim"
	"market-thermometer/internal/pmstrat"
	"market-thermometer/internal/pmtrade"
	"market-thermometer/internal/store"
)

// SettingPMExec 是策略执行配置在 meta 表里的键。
const SettingPMExec = "pm.exec"

// PMExecConfig 控制影子策略触发信号后的执行方式（默认 shadow 只记信号，安全起见须人工显式切换）。
type PMExecConfig struct {
	Mode      string  `json:"mode"`      // shadow | hitl | auto_sim
	AccountID int64   `json:"accountId"` // 绑定的模拟盘账户 ID（hitl / auto_sim 用）
	Sizing    string  `json:"sizing"`    // fixed | kelly
	OrderUSD  float64 `json:"orderUsd"`  // fixed 金额 / kelly 上限（美元）
	KellyFrac float64 `json:"kellyFrac"` // 分数凯利系数
	KSrc      string  `json:"kSrc"`      // "" 全部 | exact（仅精确起始价）
	MinEdge   float64 `json:"minEdge"`   // 触发执行/提案的最小净预期收益
	TTLSec    int     `json:"ttlSec"`    // HITL 提案有效秒数（默认 90）
	Notify    bool    `json:"notify"`    // 产生新提案或自动下单时发送运维告警
}

func DefaultPMExecConfig() PMExecConfig {
	return PMExecConfig{Mode: "shadow", Sizing: "fixed", OrderUSD: 20, KellyFrac: 0.25, KSrc: "exact", MinEdge: 0.05, TTLSec: 90}
}

// PMShadow 是短期涨跌盘公允概率策略的影子模式：挂在盘口采集器的每轮回调上，
// 对进行中的市场算 P(Up)，与盘口（卖一 + 手续费）比较，超过阈值时只记录信号，不下单。
type PMShadow struct {
	store    *store.Store
	now      func() time.Time
	logf     func(string, ...any)
	name     string
	minEdge  float64       // 记录信号的最小预期收益（每股，美元）
	df       float64       // Student-t 自由度（0 为正态，>2 为厚尾 t 分布）
	cooldown time.Duration // 同一市场同一结果两次信号的最小间隔
	keep     time.Duration // 内存里保留多久的现价

	mu       sync.Mutex
	spot     map[string][]pmstrat.Point // 品种 → 现价（升序）
	loaded   map[string]bool
	basis    map[string]float64 // 资产 → 币安相对 Chainlink 的溢价
	basisN   map[string]int
	basisErr map[string]float64 // 资产 → 基差残差的稳健标准差（bp），用作起始价估计误差与结束端换算误差
	src      ds.PredictionSource
	broker   pmtrade.Broker
	pusher   *Pusher
	execCfg  PMExecConfig
	execAt   time.Time
	fetching map[string]bool // 正在补拉起始价的 5m 事件
	ptbLag   []float64       // 起始价公布延迟（秒，5m 事件结束后多久能取到）
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

// PMEval 是一个市场最近一次估值（供页面展示与白盒透视）。
type PMEval struct {
	Target   PMBookTarget `json:"target"`
	OK       bool         `json:"ok"`
	Reason   string       `json:"reason,omitempty"`
	P        float64      `json:"p"`                 // 模型 P(Up)（按当前分布：Student-t 或正态）
	NormalP  float64      `json:"normalP,omitempty"` // 同 z 下的正态基准 P(Up)，便于白盒对比肥尾影响
	Z        float64      `json:"z,omitempty"`       // 标准化得分 (ln E[TWAP] - ln K) / sd
	DF       float64      `json:"df,omitempty"`      // 本次计算使用的 Student-t 自由度（0 为正态）
	BidUp    float64      `json:"bidUp"`
	AskUp    float64      `json:"askUp"`
	AskDn    float64      `json:"askDn"`
	FeeUp    float64      `json:"feeUp,omitempty"` // 买 Up 每股手续费
	FeeDn    float64      `json:"feeDn,omitempty"` // 买 Down 每股手续费
	EdgeUp   float64      `json:"edgeUp"`
	EdgeDn   float64      `json:"edgeDn"`
	K        float64      `json:"k"`
	KSrc     string       `json:"kSrc"`
	KErrBp   float64      `json:"kErrBp,omitempty"`
	EndErrBp float64      `json:"endErrBp,omitempty"`
	Spot     float64      `json:"spot"` // 当前价（Chainlink 口径）
	SdBp     float64      `json:"sdBp"`
	Tau      int64        `json:"tau"`
	At       time.Time    `json:"at"`
}

// UpdateParams 热更新影子策略阈值与 Student-t 自由度（从 Web 配置页保存后立即生效，无需重启）。
func (p *PMShadow) UpdateParams(minEdge, df float64) {
	p.mu.Lock()
	if minEdge > 0 {
		p.minEdge = minEdge
	}
	if df >= 0 {
		p.df = df
	}
	p.mu.Unlock()
}

// WithSource 设置数据源：用于在 15m / 1h 盘开始 5 分钟后尽快补拉同一时刻开始的 5m 事件，拿到精确起始价。
func (p *PMShadow) WithSource(src ds.PredictionSource) *PMShadow {
	p.src = src
	return p
}

// WithDF 设置 Student-t 自由度（>2 时启用厚尾分布，降低尾部过自信）。
func (p *PMShadow) WithDF(df float64) *PMShadow {
	p.mu.Lock()
	p.df = df
	p.mu.Unlock()
	return p
}

// WithBroker 注入模拟盘 Broker 与可选推送器，支持 HITL 人工确认队列与 auto_sim 自动模拟盘模式。
func (p *PMShadow) WithBroker(b pmtrade.Broker, pusher *Pusher) *PMShadow {
	p.mu.Lock()
	p.broker, p.pusher = b, pusher
	p.mu.Unlock()
	return p
}

// LoadPMExecConfig 从数据库读取策略执行配置（未配置时返回默认 shadow）。
func LoadPMExecConfig(ctx context.Context, st *store.Store) PMExecConfig {
	cfg := DefaultPMExecConfig()
	if ok, err := st.LoadJSON(ctx, SettingPMExec, &cfg); err == nil && ok {
		if cfg.Mode == "" {
			cfg.Mode = "shadow"
		}
		if cfg.Sizing == "" {
			cfg.Sizing = "fixed"
		}
		if cfg.OrderUSD <= 0 {
			cfg.OrderUSD = 20
		}
		if cfg.KellyFrac <= 0 {
			cfg.KellyFrac = 0.25
		}
		if cfg.TTLSec <= 0 {
			cfg.TTLSec = 90
		}
	}
	return cfg
}

func NewPMShadow(st *store.Store, minEdge float64) *PMShadow {
	if minEdge <= 0 {
		minEdge = 0.03
	}
	return &PMShadow{store: st, now: time.Now, logf: log.Printf, name: "updown-fair", minEdge: minEdge, cooldown: 30 * time.Second,
		keep: 70 * time.Minute, spot: map[string][]pmstrat.Point{}, loaded: map[string]bool{}, basis: map[string]float64{},
		basisN: map[string]int{}, basisErr: map[string]float64{}, fetching: map[string]bool{}, kCache: map[string]kInfo{}, lastSig: map[string]time.Time{}, sigmaOf: map[string]float64{}}
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
		for k := range p.fetching { // 节流键（slug@时刻）用过即可丢；进行中的请求键（不含 @）保留
			if strings.Contains(k, "@") {
				delete(p.fetching, k)
			}
		}
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
	if len(sigs) > 0 {
		if now.Sub(p.execAt) >= 10*time.Second {
			p.execCfg = LoadPMExecConfig(ctx, p.store)
			p.execAt = now
		}
		cfg := p.execCfg
		if cfg.Mode == "hitl" || cfg.Mode == "auto_sim" {
			go p.dispatchExec(context.Background(), cfg, sigs, now)
		}
	}
	return nil
}

// dispatchExec 在独立 goroutine 中根据执行模式生成 HITL 提案或自动下模拟单，避免阻塞盘口采集循环。
func (p *PMShadow) dispatchExec(ctx context.Context, cfg PMExecConfig, sigs []store.PMSignal, now time.Time) {
	p.mu.Lock()
	broker, pusher := p.broker, p.pusher
	p.mu.Unlock()
	equity := 1000.0
	if broker != nil && cfg.AccountID > 0 {
		if v, err := broker.Account(ctx, cfg.AccountID); err == nil && v.Equity > 0 {
			equity = v.Equity
		}
	}
	for _, s := range sigs {
		if s.Edge < cfg.MinEdge-1e-12 {
			continue
		}
		if cfg.KSrc != "" && s.KSrc != cfg.KSrc {
			continue
		}
		usd := pmstrat.SizeUSD(equity, s.P, s.Ask, s.FeeRate, pmstrat.SizingConfig{
			Mode: cfg.Sizing, FixedUSD: cfg.OrderUSD, KellyFrac: cfg.KellyFrac, MinUSD: 5,
		})
		if usd <= 0 {
			continue
		}
		switch cfg.Mode {
		case "hitl":
			prop := store.PMProposal{
				CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(time.Duration(cfg.TTLSec) * time.Second).UnixMilli(),
				Strategy: s.Strategy, Slug: s.Slug, Cond: s.Cond, Outcome: s.Outcome,
				P: s.P, Ask: s.Ask, Edge: s.Edge, SuggestedUSD: usd, KSrc: s.KSrc, Tau: s.Tau, AccountID: cfg.AccountID,
			}
			id, err := p.store.AddPMProposal(ctx, prop, now)
			if err == nil && id > 0 && cfg.Notify && pusher != nil {
				side := "Up"
				if s.Outcome == 1 {
					side = "Down"
				}
				pusher.SendAlert(ctx, fmt.Sprintf("pm-prop-%s-%d", s.Cond, s.Outcome), "🔔【Polymarket 待确认提案】"+s.Slug, []string{
					fmt.Sprintf("方向：%s｜模型 %.1f%% vs 卖一 %.1f¢｜净 Edge +%.1f¢", side, s.P*100, s.Ask*100, s.Edge*100),
					fmt.Sprintf("建议金额：$%.2f｜剩余 %ds｜起始价来源：%s（%d 秒内可在网页批准）", usd, s.Tau, s.KSrc, cfg.TTLSec),
				})
			}
		case "auto_sim":
			if broker == nil || cfg.AccountID <= 0 {
				continue
			}
			feeAtAsk := pmsim.Fee{Rate: s.FeeRate}.Of(s.Ask, 1)
			limit := max(0.01, min(0.99, s.P-feeAtAsk))
			o, err := broker.Place(ctx, pmtrade.OrderReq{
				Account: cfg.AccountID, Slug: s.Slug, Cond: s.Cond, Outcome: s.Outcome,
				Side: pmsim.Buy, Amount: usd, Limit: limit, Source: "strategy:" + s.Strategy,
			})
			if err == nil {
				_ = p.store.AddAudit(ctx, "strategy", "pm.auto_order", s.Slug, map[string]any{
					"account": cfg.AccountID, "outcome": s.Outcome, "status": o.Status, "shares": o.Fill.Shares, "gross": o.Fill.Gross, "reason": o.Reason,
				}, now)
			}
		}
	}
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
			p.basis[a], p.basisN[a] = pmstrat.Median(append([]float64(nil), rel...)), len(rel)
			p.basisErr[a] = max(pmstrat.MADSigma(rel)*1e4, 0.5) // 下限 0.5bp
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
		// 误差取基差残差的稳健标准差；用 1 分钟 K 线估计时再放大（OHLC 均值只是 60 秒均价的近似）
		e := p.basisErr[asset]
		if src == "bar" {
			e = max(e, 2)
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
	sigma := pmstrat.BlendedSigma(recent, now.UnixMilli())
	if sigma <= 0 {
		e.Reason = "波动率数据不足"
		return e
	}
	p.sigmaOf[t.Symbol] = sigma
	if k.src != "exact" {
		p.fetchStartPrice(a, t.Start.Unix(), now)
	}
	r, ok := pmstrat.ProbUp(pmstrat.Inputs{Now: now.Unix(), Start: t.Start.Unix(), End: t.End.Unix(), K: k.k, KErrBp: k.errBp,
		EndErrBp: p.basisErr[a], Basis: p.basis[a], Sigma: sigma, DF: p.df, Spot: recent})
	if !ok {
		e.Reason = "现价中断"
		return e
	}
	e.OK, e.P, e.NormalP, e.Z, e.DF, e.SdBp, e.Tau = true, r.P, pmstrat.Phi(r.Score), r.Score, p.df, r.SdBp, r.Tau
	e.KErrBp, e.EndErrBp = k.errBp, p.basisErr[a]
	if n := len(recent); n > 0 {
		e.Spot = recent[n-1].P / (1 + p.basis[a])
	}
	e.EdgeUp, e.EdgeDn = math.Inf(-1), math.Inf(-1)
	if len(b.Asks) > 0 {
		e.AskUp = b.Asks[0].Price
		e.FeeUp = t.Fee * e.AskUp * (1 - e.AskUp)
		e.EdgeUp = pmstrat.Edge(r.P, e.AskUp, t.Fee)
	}
	if len(b.Bids) > 0 {
		e.BidUp = b.Bids[0].Price
		e.AskDn = 1 - e.BidUp // Down 的卖一 = 1 − Up 的买一
		e.FeeDn = t.Fee * e.AskDn * (1 - e.AskDn)
		e.EdgeDn = pmstrat.Edge(1-r.P, e.AskDn, t.Fee)
	}
	return e
}

// fetchStartPrice 在后台按 slug 补拉同一时刻开始的事件，拿到起始价后存入本地，下一轮即用精确值：
//   - 整点开始的：1 小时盘在开始后几分钟内就公布起始价（实测约 3 分钟内），开始 1 分钟后起每 10 秒查一次；
//   - 其余：5m 盘结束后才公布，开始 5 分钟后起每 10 秒查一次。
//
// 同一事件同时只有一个请求；调用方持有 p.mu。
func (p *PMShadow) fetchStartPrice(asset string, start int64, now time.Time) {
	slug, ready := fmt.Sprintf("%s-updown-5m-%d", asset, start), start+300
	if start%3600 == 0 {
		slug, ready = fmt.Sprintf("%s-updown-1h-%d", asset, start), start+60
	}
	if now.Unix() < ready {
		return
	}
	key := slug + "@" + now.Truncate(10*time.Second).String() // 每个事件每 10 秒最多一次
	if p.src == nil || p.fetching[slug] || p.fetching[key] {
		return
	}
	p.fetching[slug], p.fetching[key] = true, true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		evs, err := p.src.Events(ctx, ds.PMEventQuery{Slug: slug, Limit: 1})
		found := err == nil && len(evs) == 1 && evs[0].PriceToBeat > 0
		if found {
			err = p.store.SavePMEvents(ctx, evs, p.now())
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		delete(p.fetching, slug)
		for k := range p.fetching { // 清理过期的节流键
			if strings.HasPrefix(k, slug+"@") && k != key {
				delete(p.fetching, k)
			}
		}
		if found && err == nil {
			delete(p.kCache, fmt.Sprintf("%s/%d", asset, start))
			p.ptbLag = append(p.ptbLag, p.now().Sub(time.Unix(ready, 0)).Seconds())
			if len(p.ptbLag) > 50 {
				p.ptbLag = p.ptbLag[len(p.ptbLag)-50:]
			}
		}
	}()
}

// PMShadowStatus 是影子模式状态（含模型白盒元数据）。
type PMShadowStatus struct {
	Strategy     string             `json:"strategy"`
	MinEdge      float64            `json:"minEdge"`
	DF           float64            `json:"df"`           // Student-t 自由度（0 为正态）
	Distribution string             `json:"distribution"` // 概率分布描述
	VolMode      string             `json:"volMode"`      // 波动率算法描述
	Evals        []PMEval           `json:"evals"`
	Signals      int64              `json:"signals"` // 本进程记录的信号数
	Basis        map[string]float64 `json:"basis"`
	BasisN       map[string]int     `json:"basisN"`
	Sigma        map[string]float64 `json:"sigma"` // 每 √秒
	BasisErr     map[string]float64 `json:"basisErr"`
	PTBLag       float64            `json:"ptbLag"` // 可以开始查询到取到起始价的延迟中位数（秒），0 为还没有样本
	LastTick     time.Time          `json:"lastTick,omitzero"`
	LastErr      string             `json:"lastErr,omitempty"`
	ErrAt        time.Time          `json:"errAt,omitzero"`
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
	dist := "标准正态 Φ(z)"
	if p.df > 2 {
		dist = fmt.Sprintf("Student-t(df=%.1f) 厚尾分布", p.df)
	}
	return PMShadowStatus{
		Strategy: p.name, MinEdge: p.minEdge, DF: p.df, Distribution: dist, VolMode: "BlendedSigma (30m + 5m 实现波动率混合)",
		Evals: evals, Signals: p.signals, Basis: cp(p.basis), BasisN: n,
		Sigma: cp(p.sigmaOf), BasisErr: cp(p.basisErr), PTBLag: pmstrat.Median(append([]float64(nil), p.ptbLag...)),
		LastTick: p.lastTick, LastErr: p.lastErr, ErrAt: p.errAt,
	}
}
