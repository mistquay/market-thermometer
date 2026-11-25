// Package engine 计算估值层 V 与择时层 T（纯函数，回测与实盘共用）。
// 引擎只认 market.Role，不关心数据来自哪个数据源。
package engine

import (
	"math"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/indicator"
	"market-thermometer/internal/market"
)

// Inputs 是按交易日历对齐后的输入。Dates 为日历（标的指数有收盘价的日子），
// Series[role][t] 为该日的值，缺失为 NaN。
type Inputs struct {
	Dates  []string
	Series map[market.Role][]float64
}

// Align 把各角色的原始点对齐到 close 的交易日历。
// 慢变量（PE、利率）允许用最近的前值填充最多 maxFill 个交易日，其余角色只认当日值。
func Align(raw map[market.Role][]ds.Point, maxFill int) Inputs {
	closePts := raw[market.RoleClose]
	in := Inputs{Series: map[market.Role][]float64{}}
	for _, p := range closePts {
		in.Dates = append(in.Dates, p.Date)
	}
	for role, pts := range raw {
		fill := 0
		if market.SlowRoles[role] {
			fill = maxFill
		}
		in.Series[role] = alignOne(in.Dates, pts, fill)
	}
	return in
}

// alignOne：对日历中的每一天，取日期 ≤ 当天的最新观测；若该观测落后超过 fill 个交易日则视为缺失。
func alignOne(dates []string, pts []ds.Point, fill int) []float64 {
	out := make([]float64, len(dates))
	j := -1     // pts 中最新的 ≤ 当前日期的下标
	lastT := -1 // 该观测被读入时的日历下标
	for t, d := range dates {
		for j+1 < len(pts) && pts[j+1].Date <= d {
			j++
			lastT = t
		}
		out[t] = math.NaN()
		if j >= 0 && t-lastT <= fill {
			out[t] = pts[j].Value
		}
	}
	return out
}

// Tier 估值档。
const (
	TierCheap     = "cheap"
	TierFair      = "fair"
	TierExpensive = "expensive"
)

// Day 是某个交易日的完整打分结果。NaN 表示无法计算（预热期或缺数）。
type Day struct {
	Date  string  `json:"date"`
	Close float64 `json:"close"`

	ERP  float64 `json:"erp"` // 小数，0.05 = 5%
	V    float64 `json:"v"`
	Tier string  `json:"tier"`

	Heat     float64    `json:"heat"`     // T1 原始：按金额换手率（%），已取 N 日均值
	Leverage float64    `json:"leverage"` // T2 原始：融资买入占比（%），已取 N 日均值
	Breadth  float64    `json:"breadth"`  // T3 原始：站上 200 日均线的个股占比（%）
	RSID     float64    `json:"rsiD"`
	RSIW     float64    `json:"rsiW"`
	RMix     float64    `json:"rMix"`
	T        [4]float64 `json:"t"` // T1..T4 子得分
	TBase    float64    `json:"tBase"`
	TPct     float64    `json:"tPct"`
	TS       float64    `json:"tS"`
	NLow     int        `json:"nLow"`
	NHigh    int        `json:"nHigh"`
	Degraded bool       `json:"degraded"` // 有一个已启用维度缺数
}

// Compute 逐日计算全部得分。
func Compute(in Inputs, p Params) []Day {
	n := len(in.Dates)
	s := func(r market.Role) []float64 {
		if v, ok := in.Series[r]; ok {
			return v
		}
		return nanSlice(n)
	}
	closes := s(market.RoleClose)

	// 估值层：ERP_t = 1/PE_{t-1} - Y10_{t-1}/100（当日 PE 发布时间未知，统一用 T-1）
	pe, y10 := indicator.Shift(s(market.RolePE), 1), indicator.Shift(s(market.RoleBondY10), 1)
	erp := make([]float64, n)
	for t := range erp {
		erp[t] = math.NaN()
		if pe[t] > 0 {
			erp[t] = 1/pe[t] - y10[t]/100
		}
	}
	erpPct := indicator.RollingPct(erp, p.ValuationWindow, p.ValuationMinW)

	// T1：中证全指成交额(T) / 全A流通市值(T-1)
	amount, floatMV := s(market.RoleAmount), indicator.Shift(s(market.RoleFloatMV), 1)
	heat := make([]float64, n)
	for t := range heat {
		heat[t] = div(amount[t], floatMV[t]) * 100
	}
	heat = indicator.SMA(heat, p.PreAvgDays)

	// T2：融资买入额 / 中证全指成交额，整体用 T-1（两融 T+1 发布）
	marginBuy := s(market.RoleMarginBuy)
	lev := make([]float64, n)
	for t := range lev {
		lev[t] = div(marginBuy[t], amount[t]) * 100
	}
	lev = indicator.SMA(indicator.Shift(lev, 1), p.PreAvgDays)

	// T4：双周期 RSI
	rsiD := indicator.RSI(closes, p.RSIPeriod)
	rsiW := indicator.WeeklyRSI(in.Dates, closes, p.RSIPeriod)
	rmix := make([]float64, n)
	for t := range rmix {
		rmix[t] = p.RSIDailyW*rsiD[t] + p.RSIWeeklyW*rsiW[t]
	}

	// T3：市场广度，当日值（个股收盘后即可计算）
	breadth := s(market.RoleBreadth)

	sub := [4][]float64{
		indicator.RollingPct(heat, p.TimingWindow, p.TimingMinW),
		indicator.RollingPct(lev, p.TimingWindow, p.TimingMinW),
		indicator.RollingPct(breadth, p.TimingWindow, p.TimingMinW),
		indicator.RollingPct(rmix, p.TimingWindow, p.TimingMinW),
	}

	days := make([]Day, n)
	tbase := make([]float64, n)
	prevTier := ""
	for t := range days {
		d := Day{Date: in.Dates[t], Close: closes[t], ERP: erp[t], V: 100 - erpPct[t],
			Heat: heat[t], Leverage: lev[t], Breadth: breadth[t], RSID: rsiD[t], RSIW: rsiW[t], RMix: rmix[t]}
		d.Tier = nextTier(prevTier, d.V, p)
		prevTier = d.Tier
		var sumW, sum float64
		missing := 0
		for k := range 4 {
			d.T[k] = sub[k][t]
			if p.Weights[k] <= 0 {
				continue
			}
			if math.IsNaN(d.T[k]) {
				missing++
				continue
			}
			sumW += p.Weights[k]
			sum += p.Weights[k] * d.T[k]
			if d.T[k] <= p.Extreme {
				d.NLow++
			}
			if d.T[k] >= 100-p.Extreme {
				d.NHigh++
			}
		}
		tbase[t] = math.NaN()
		if sumW > 0 && missing < 2 {
			tbase[t] = sum / sumW
			d.Degraded = missing == 1
		}
		d.TBase = tbase[t]
		days[t] = d
	}
	tpct := tbase
	if !p.SkipTPct {
		tpct = indicator.RollingPct(tbase, p.TimingWindow, p.TimingMinW)
	}
	ts := indicator.EMA(tpct, p.EMASpan)
	for t := range days {
		days[t].TPct, days[t].TS = tpct[t], ts[t]
	}
	return days
}

// nextTier 带滞回地确定估值档：进入便宜 / 昂贵用阈值本身，离开时需越过阈值 ± TierHysteresis，
// 避免 V 在阈值附近来回抖动导致档位频繁切换。只依赖前一日档位，无未来函数。
func nextTier(prev string, v float64, p Params) string {
	if math.IsNaN(v) {
		return ""
	}
	switch {
	case v <= p.CheapBelow:
		return TierCheap
	case v >= p.ExpensiveAbove:
		return TierExpensive
	case prev == TierCheap && v <= p.CheapBelow+p.TierHysteresis:
		return TierCheap
	case prev == TierExpensive && v >= p.ExpensiveAbove-p.TierHysteresis:
		return TierExpensive
	default:
		return TierFair
	}
}

func div(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) || b == 0 {
		return math.NaN()
	}
	return a / b
}

func nanSlice(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	return out
}
