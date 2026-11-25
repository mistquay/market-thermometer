// Package backtest 评估状态机信号：前瞻收益、最大不利波动、与简单基线和无条件收益对比。
// 纯函数，只读引擎输出，不访问网络或数据库。
package backtest

import (
	"math"
	"sort"

	"market-thermometer/internal/engine"
	"market-thermometer/internal/indicator"
)

// Config 是回测设置。
type Config struct {
	From      string `json:"from"`      // 统计区间起点（含）
	To        string `json:"to"`        // 终点（含），空为不限
	Split     string `json:"split"`     // 样本内 / 样本外分界：< Split 为样本内
	Horizons  []int  `json:"horizons"`  // 前瞻收益的交易日数
	MAEWindow int    `json:"maeWindow"` // 最大不利波动的观察窗口
}

func DefaultConfig() Config {
	return Config{From: "2014-01-01", Split: "2021-01-01", Horizons: []int{20, 60, 120}, MAEWindow: 60}
}

// EventOut 是可安全编码为 JSON 的事件（NaN → null）。
type EventOut struct {
	Date    string   `json:"date"`
	Idx     int      `json:"-"`
	Type    string   `json:"type"`
	Reason  string   `json:"reason"`
	Observe bool     `json:"observe"`
	Close   *float64 `json:"close"`
	TS      *float64 `json:"tS"`
	V       *float64 `json:"v"`
	Tier    string   `json:"tier"`
}

func eventOut(e engine.Event) EventOut {
	return EventOut{Date: e.Date, Idx: e.Idx, Type: e.Type, Reason: e.Reason, Observe: e.Observe,
		Close: ptr(e.Close), TS: ptr(e.TS), V: ptr(e.V), Tier: e.Tier}
}

// Signal 是一次买卖信号及其事后表现。收益为小数，nil 表示样本末尾不足以计算。
type Signal struct {
	EventOut
	Source string     `json:"source"` // thermometer | valuation | ma200
	Side   int        `json:"side"`   // +1 看多（买） / -1 看空（卖）
	Rets   []*float64 `json:"rets"`   // 与 Config.Horizons 一一对应
	MAE    *float64   `json:"mae"`    // 看多：期间最大跌幅（≤0）；看空：期间最大涨幅（≥0）
}

// HStat 是某一前瞻期的收益统计。WinRate：看多为收益>0 的比例，看空为收益<0 的比例。
type HStat struct {
	N       int      `json:"n"`
	Median  *float64 `json:"median"`
	Mean    *float64 `json:"mean"`
	WinRate *float64 `json:"winRate"`
}

// Group 是一组信号（同来源、同方向、同区间）的汇总。
type Group struct {
	Source    string   `json:"source"`
	Side      int      `json:"side"`
	Period    string   `json:"period"` // all | in | out
	N         int      `json:"n"`
	Horizons  []HStat  `json:"horizons"`
	MAEMedian *float64 `json:"maeMedian"`
}

// Strategy 是按信号持仓（多 / 空仓，不计成本）的模拟结果。
type Strategy struct {
	Name     string    `json:"name"`
	Label    string    `json:"label"`
	TotalRet float64   `json:"totalRet"`
	CAGR     float64   `json:"cagr"`
	MaxDD    float64   `json:"maxDD"`
	Exposure float64   `json:"exposure"` // 持仓天数占比
	Switches int       `json:"switches"` // 开平仓次数
	Equity   []float64 `json:"equity"`
}

// Result 是一次回测的完整输出。
type Result struct {
	Config     Config       `json:"config"`
	From       string       `json:"from"`
	To         string       `json:"to"`
	Days       int          `json:"days"`
	Signals    []Signal     `json:"signals"`
	Groups     []Group      `json:"groups"`
	BaseRate   []HStat      `json:"baseRate"` // 区间内任意一天买入的前瞻收益（无条件）
	Strategies []Strategy   `json:"strategies"`
	Dates      []string     `json:"dates"`
	Close      []float64    `json:"close"`
	CorrNames  []string     `json:"corrNames"`
	Corr       [][]*float64 `json:"corr"`
	Events     []EventOut   `json:"events"` // 区间内状态机全部事件（含武装/失效）
}

// Run 执行回测。days 为全量引擎输出（含预热期），events 为对应的状态机事件。
func Run(days []engine.Day, events []engine.Event, cfg Config) Result {
	lo := sort.Search(len(days), func(i int) bool { return days[i].Date >= cfg.From })
	hi := len(days)
	if cfg.To != "" {
		hi = sort.Search(len(days), func(i int) bool { return days[i].Date > cfg.To })
	}
	res := Result{Config: cfg}
	if lo >= hi {
		return res
	}
	res.From, res.To, res.Days = days[lo].Date, days[hi-1].Date, hi-lo
	closes := make([]float64, len(days))
	for i, d := range days {
		closes[i] = d.Close
	}
	inRange := func(i int) bool { return i >= lo && i < hi }

	// 温度计信号（观察级的底部信号单列为 observe，不计入持仓）
	for _, e := range events {
		if !inRange(e.Idx) {
			continue
		}
		res.Events = append(res.Events, eventOut(e))
		switch e.Type {
		case engine.EvBuyDip:
			res.Signals = append(res.Signals, signalAt(e, "thermometer", +1, closes, hi, cfg))
		case engine.EvSellPeak:
			res.Signals = append(res.Signals, signalAt(e, "thermometer", -1, closes, hi, cfg))
		}
	}
	// 基线 1：估值档进入便宜 / 昂贵
	for _, e := range events {
		if e.Type != engine.EvTierChange || !inRange(e.Idx) {
			continue
		}
		switch e.Tier {
		case engine.TierCheap:
			res.Signals = append(res.Signals, signalAt(e, "valuation", +1, closes, hi, cfg))
		case engine.TierExpensive:
			res.Signals = append(res.Signals, signalAt(e, "valuation", -1, closes, hi, cfg))
		}
	}
	// 基线 2：收盘价上穿 / 下穿 200 日均线
	ma := indicator.SMA(closes, 200)
	for i := max(lo, 1); i < hi; i++ {
		if math.IsNaN(ma[i]) || math.IsNaN(ma[i-1]) {
			continue
		}
		up, wasUp := closes[i] > ma[i], closes[i-1] > ma[i-1]
		if up == wasUp {
			continue
		}
		side := -1
		if up {
			side = +1
		}
		e := engine.Event{Date: days[i].Date, Idx: i, Type: "MA200_CROSS", Close: closes[i], TS: days[i].TS, V: days[i].V, Tier: days[i].Tier}
		res.Signals = append(res.Signals, signalAt(e, "ma200", side, closes, hi, cfg))
	}

	res.Groups = groups(res.Signals, cfg)
	res.BaseRate = baseRate(closes, lo, hi, cfg)
	res.Dates = make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		res.Dates = append(res.Dates, days[i].Date)
	}
	res.Close = closes[lo:hi]
	res.Strategies = strategies(days, events, ma, lo, hi)
	res.CorrNames, res.Corr = correlations(days[lo:hi])
	return res
}

func signalAt(e engine.Event, source string, side int, closes []float64, hi int, cfg Config) Signal {
	s := Signal{EventOut: eventOut(e), Source: source, Side: side}
	i, c := e.Idx, closes[e.Idx]
	for _, h := range cfg.Horizons {
		if i+h < hi {
			r := closes[i+h]/c - 1
			s.Rets = append(s.Rets, &r)
		} else {
			s.Rets = append(s.Rets, nil)
		}
	}
	if i+cfg.MAEWindow < hi {
		worst := 0.0
		for k := i + 1; k <= i+cfg.MAEWindow; k++ {
			r := closes[k]/c - 1
			if (side > 0 && r < worst) || (side < 0 && r > worst) {
				worst = r
			}
		}
		s.MAE = &worst
	}
	return s
}

func groups(sigs []Signal, cfg Config) []Group {
	type key struct {
		src    string
		side   int
		period string
	}
	buckets := map[key][]Signal{}
	var order []key
	add := func(k key, s Signal) {
		if _, ok := buckets[k]; !ok {
			order = append(order, k)
		}
		buckets[k] = append(buckets[k], s)
	}
	for _, s := range sigs {
		if s.Observe {
			continue // 观察级信号不计入统计
		}
		period := "in"
		if s.Date >= cfg.Split {
			period = "out"
		}
		add(key{s.Source, s.Side, "all"}, s)
		add(key{s.Source, s.Side, period}, s)
	}
	var out []Group
	for _, k := range order {
		ss := buckets[k]
		g := Group{Source: k.src, Side: k.side, Period: k.period, N: len(ss)}
		for hIdx := range cfg.Horizons {
			var rs []float64
			for _, s := range ss {
				if r := s.Rets[hIdx]; r != nil {
					rs = append(rs, *r)
				}
			}
			g.Horizons = append(g.Horizons, hstat(rs, k.side))
		}
		var maes []float64
		for _, s := range ss {
			if s.MAE != nil {
				maes = append(maes, *s.MAE)
			}
		}
		g.MAEMedian = ptr(median(maes))
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return srcRank(out[i].Source) < srcRank(out[j].Source)
		}
		if out[i].Side != out[j].Side {
			return out[i].Side > out[j].Side
		}
		return periodRank(out[i].Period) < periodRank(out[j].Period)
	})
	return out
}

func srcRank(s string) int {
	return map[string]int{"thermometer": 0, "valuation": 1, "ma200": 2}[s]
}
func periodRank(s string) int { return map[string]int{"all": 0, "in": 1, "out": 2}[s] }

func hstat(rs []float64, side int) HStat {
	h := HStat{N: len(rs)}
	if len(rs) == 0 {
		return h
	}
	sum, win := 0.0, 0
	for _, r := range rs {
		sum += r
		if (side > 0 && r > 0) || (side < 0 && r < 0) {
			win++
		}
	}
	h.Median = ptr(median(rs))
	h.Mean = ptr(sum / float64(len(rs)))
	h.WinRate = ptr(float64(win) / float64(len(rs)))
	return h
}

// baseRate：区间内每一天作为买入日的前瞻收益分布（衡量"随便哪天买"的基准）。
func baseRate(closes []float64, lo, hi int, cfg Config) []HStat {
	var out []HStat
	for _, h := range cfg.Horizons {
		var rs []float64
		for i := lo; i+h < hi; i++ {
			rs = append(rs, closes[i+h]/closes[i]-1)
		}
		out = append(out, hstat(rs, +1))
	}
	return out
}

// strategies：信号在第 t 日收盘产生，第 t+1 日起生效（无未来函数）；不计交易成本。
func strategies(days []engine.Day, events []engine.Event, ma []float64, lo, hi int) []Strategy {
	n := hi - lo
	thermo := make([]bool, len(days))
	pos := false
	evAt := map[int][]engine.Event{}
	for _, e := range events {
		evAt[e.Idx] = append(evAt[e.Idx], e)
	}
	valuation := make([]bool, len(days))
	vpos := false
	for i := range days {
		for _, e := range evAt[i] {
			switch {
			case e.Type == engine.EvBuyDip && !e.Observe:
				pos = true
			case e.Type == engine.EvSellPeak:
				pos = false
			}
		}
		thermo[i] = pos
		switch days[i].Tier {
		case engine.TierCheap:
			vpos = true
		case engine.TierExpensive:
			vpos = false
		}
		valuation[i] = vpos
	}
	maPos := make([]bool, len(days))
	for i := range days {
		maPos[i] = !math.IsNaN(ma[i]) && days[i].Close > ma[i]
	}
	hold := make([]bool, len(days))
	for i := range hold {
		hold[i] = true
	}
	mk := func(name, label string, p []bool) Strategy {
		s := Strategy{Name: name, Label: label, Equity: make([]float64, n)}
		eq, peak, held := 1.0, 1.0, 0
		for k := 0; k < n; k++ {
			i := lo + k
			if k > 0 {
				if p[i-1] { // 前一日收盘时的持仓决定今日收益
					eq *= days[i].Close / days[i-1].Close
					held++
				}
				if p[i] != p[i-1] {
					s.Switches++
				}
			}
			peak = math.Max(peak, eq)
			s.MaxDD = math.Min(s.MaxDD, eq/peak-1)
			s.Equity[k] = eq
		}
		s.TotalRet = eq - 1
		if n > 1 {
			s.CAGR = math.Pow(eq, 252/float64(n-1)) - 1
			s.Exposure = float64(held) / float64(n-1)
		}
		return s
	}
	return []Strategy{
		mk("thermometer", "温度计信号持仓", thermo),
		mk("valuation", "估值档持仓（便宜买、昂贵卖）", valuation),
		mk("ma200", "200 日均线择时", maPos),
		mk("hold", "买入持有", hold),
	}
}

// correlations：择时层已启用维度两两的 Pearson 相关系数（只用各维度均有值的日子）。
func correlations(days []engine.Day) ([]string, [][]*float64) {
	names := []string{"资金热度", "杠杆情绪", "市场广度", "价格动量"}
	var idx []int
	for k := range 4 {
		for _, d := range days {
			if !math.IsNaN(d.T[k]) {
				idx = append(idx, k)
				break
			}
		}
	}
	out := make([][]*float64, len(idx))
	var outNames []string
	for a, ka := range idx {
		outNames = append(outNames, names[ka])
		out[a] = make([]*float64, len(idx))
		for b, kb := range idx {
			var xs, ys []float64
			for _, d := range days {
				if !math.IsNaN(d.T[ka]) && !math.IsNaN(d.T[kb]) {
					xs, ys = append(xs, d.T[ka]), append(ys, d.T[kb])
				}
			}
			out[a][b] = ptr(pearson(xs, ys))
		}
	}
	return outNames, out
}

func pearson(xs, ys []float64) float64 {
	n := float64(len(xs))
	if n < 2 {
		return math.NaN()
	}
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx, my = mx/n, my/n
	var sxy, sxx, syy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// ptr 返回指针；NaN 返回 nil（JSON 编码为 null）。
func ptr(x float64) *float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return &x
}
