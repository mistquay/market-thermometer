// Package portfolio 是组合分析的纯函数：起始日按目标权重买入后持有（不再平衡），
// 输出净值、回撤、收益风险指标、各持仓贡献、相关矩阵与分年度收益。只读调用方传入的本地数据。
package portfolio

import (
	"math"
	"sort"
)

// Asset 是一个持仓。Prices 为复权价格（股票、场内基金用后复权价，场外基金用累计净值），键为 YYYY-MM-DD。
type Asset struct {
	Key    string             `json:"key"` // kind:code
	Label  string             `json:"label"`
	Weight float64            `json:"weight"`
	Prices map[string]float64 `json:"-"`
}

// Input 是一次分析的输入。Calendar 为交易日历（升序，通常是沪深300 有数据的日子）。
type Input struct {
	Calendar []string
	Bench    map[string]float64 // 基准收盘价
	Assets   []Asset
	Start    string // 可选：起始日，空为所有持仓都有数据的最早日
}

// Stats 是收益风险指标（无风险利率按 0 计）。
type Stats struct {
	TotalRet float64  `json:"totalRet"`
	CAGR     float64  `json:"cagr"`
	Vol      float64  `json:"vol"` // 年化波动
	MaxDD    float64  `json:"maxDD"`
	Sharpe   *float64 `json:"sharpe"`
}

// AssetStat 是单个持仓的结果。
type AssetStat struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	Weight       float64 `json:"weight"`    // 归一化后的初始权重
	EndWeight    float64 `json:"endWeight"` // 期末权重（持有不动导致漂移）
	First        string  `json:"first"`     // 本地最早数据日
	Contribution float64 `json:"contribution"`
	Stats
}

// Year 是一个自然年的收益。
type Year struct {
	Year   string  `json:"year"`
	Ret    float64 `json:"ret"`
	Bench  float64 `json:"bench"`
	Excess float64 `json:"excess"`
}

// Output 是分析结果。
type Output struct {
	From      string       `json:"from"`
	To        string       `json:"to"`
	Dates     []string     `json:"dates"`
	NAV       []float64    `json:"nav"`
	BenchNAV  []float64    `json:"benchNav"`
	Drawdown  []float64    `json:"drawdown"`
	Stats     Stats        `json:"stats"`
	Bench     Stats        `json:"bench"`
	Beta      *float64     `json:"beta"`
	BenchCorr *float64     `json:"benchCorr"`
	Assets    []AssetStat  `json:"assets"`
	CorrKeys  []string     `json:"corrKeys"`
	Corr      [][]*float64 `json:"corr"`
	Years     []Year       `json:"years"`
	Missing   []string     `json:"missing"` // 没有数据、未计入的持仓
	Note      string       `json:"note,omitempty"`
}

// Analyze 计算组合表现。没有数据的持仓列入 Missing，其余持仓权重重新归一化。
func Analyze(in Input) Output {
	out := Output{Missing: []string{}, Assets: []AssetStat{}, Years: []Year{}}
	var assets []Asset
	firsts := map[string]string{}
	for _, a := range in.Assets {
		if len(a.Prices) == 0 || a.Weight <= 0 {
			out.Missing = append(out.Missing, a.Key)
			continue
		}
		first := ""
		for d := range a.Prices {
			if first == "" || d < first {
				first = d
			}
		}
		firsts[a.Key] = first
		assets = append(assets, a)
	}
	if len(assets) == 0 {
		out.Note = "没有可计算的持仓"
		return out
	}
	// 起始日：所有持仓与基准都已有数据的最早交易日（不早于请求的起始日）
	start := in.Start
	for _, f := range firsts {
		start = max(start, f)
	}
	var cal []string
	for _, d := range in.Calendar {
		if d >= start {
			cal = append(cal, d)
		}
	}
	for len(cal) > 0 && in.Bench[cal[0]] == 0 {
		cal = cal[1:]
	}
	if len(cal) < 2 {
		out.Note = "可比较的交易日不足"
		return out
	}
	n := len(cal)
	out.From, out.To, out.Dates = cal[0], cal[n-1], cal

	// 前值填充到日历上：停牌日价格不变（收益为 0）
	series := func(m map[string]float64) []float64 {
		var keys []string
		for d := range m {
			keys = append(keys, d)
		}
		sort.Strings(keys)
		s := make([]float64, n)
		j, last := 0, math.NaN()
		for i, d := range cal {
			for j < len(keys) && keys[j] <= d {
				last = m[keys[j]]
				j++
			}
			s[i] = last
		}
		return s
	}
	totalW := 0.0
	for _, a := range assets {
		totalW += a.Weight
	}
	px := make([][]float64, len(assets))
	units := make([]float64, len(assets))
	for k, a := range assets {
		px[k] = series(a.Prices)
		units[k] = a.Weight / totalW / px[k][0]
	}
	out.NAV = make([]float64, n)
	for i := range cal {
		for k := range assets {
			out.NAV[i] += units[k] * px[k][i]
		}
	}
	bench := series(in.Bench)
	out.BenchNAV = make([]float64, n)
	for i := range cal {
		out.BenchNAV[i] = bench[i] / bench[0]
	}
	out.Drawdown = drawdown(out.NAV)
	out.Stats, out.Bench = stats(out.NAV), stats(out.BenchNAV)

	rp, rb := returns(out.NAV), returns(out.BenchNAV)
	if v := variance(rb); v > 0 {
		out.Beta = ptr(covariance(rp, rb) / v)
	}
	out.BenchCorr = ptr(pearson(rp, rb))

	rets := make([][]float64, len(assets))
	for k, a := range assets {
		nav := make([]float64, n)
		for i := range nav {
			nav[i] = px[k][i] / px[k][0]
		}
		rets[k] = returns(nav)
		st := AssetStat{Key: a.Key, Label: a.Label, Weight: a.Weight / totalW, First: firsts[a.Key], Stats: stats(nav)}
		st.Contribution = st.Weight * st.TotalRet // 买入持有：贡献 = 初始权重 × 区间收益，合计等于组合收益
		st.EndWeight = units[k] * px[k][n-1] / out.NAV[n-1]
		out.Assets = append(out.Assets, st)
		out.CorrKeys = append(out.CorrKeys, a.Key)
	}
	out.Corr = make([][]*float64, len(assets))
	for a := range assets {
		out.Corr[a] = make([]*float64, len(assets))
		for b := range assets {
			out.Corr[a][b] = ptr(pearson(rets[a], rets[b]))
		}
	}
	out.Years = years(cal, out.NAV, out.BenchNAV)
	return out
}

func returns(nav []float64) []float64 {
	r := make([]float64, 0, len(nav)-1)
	for i := 1; i < len(nav); i++ {
		r = append(r, nav[i]/nav[i-1]-1)
	}
	return r
}

func drawdown(nav []float64) []float64 {
	dd := make([]float64, len(nav))
	peak := nav[0]
	for i, v := range nav {
		peak = math.Max(peak, v)
		dd[i] = v/peak - 1
	}
	return dd
}

func stats(nav []float64) Stats {
	n := len(nav)
	s := Stats{TotalRet: nav[n-1]/nav[0] - 1}
	if n > 1 {
		s.CAGR = math.Pow(nav[n-1]/nav[0], 252/float64(n-1)) - 1
	}
	r := returns(nav)
	s.Vol = math.Sqrt(variance(r) * 252)
	for _, d := range drawdown(nav) {
		s.MaxDD = math.Min(s.MaxDD, d)
	}
	if s.Vol > 0 {
		s.Sharpe = ptr(mean(r) * 252 / s.Vol)
	}
	return s
}

func years(cal []string, nav, bench []float64) []Year {
	var out []Year
	startIdx := 0
	for i := 1; i <= len(cal); i++ {
		if i < len(cal) && cal[i][:4] == cal[startIdx][:4] {
			continue
		}
		// 年度收益：上一年末（或区间起点）到本年最后一个交易日
		base := max(startIdx-1, 0)
		y := Year{Year: cal[startIdx][:4], Ret: nav[i-1]/nav[base] - 1, Bench: bench[i-1]/bench[base] - 1}
		if base == 0 && startIdx == 0 && cal[0][5:] > "01-10" {
			y.Year += "（不完整）"
		}
		if i == len(cal) && cal[i-1][5:] < "12-20" {
			y.Year += "（不完整）"
		}
		y.Excess = y.Ret - y.Bench
		out = append(out, y)
		startIdx = i
	}
	return out
}

func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func variance(x []float64) float64 { return covariance(x, x) }

func covariance(x, y []float64) float64 {
	if len(x) < 2 {
		return 0
	}
	mx, my := mean(x), mean(y)
	s := 0.0
	for i := range x {
		s += (x[i] - mx) * (y[i] - my)
	}
	return s / float64(len(x)-1)
}

func pearson(x, y []float64) float64 {
	vx, vy := variance(x), variance(y)
	if vx == 0 || vy == 0 {
		return math.NaN()
	}
	return covariance(x, y) / math.Sqrt(vx*vy)
}

func ptr(x float64) *float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return &x
}

// HFQ 由不复权收盘价与除权乘数链式计算后复权价：hfq_t = hfq_{t-1} × close_t × mult_t / close_{t-1}，首日为 close。
func HFQ(dates []string, close, mult []float64) map[string]float64 {
	out := make(map[string]float64, len(dates))
	var h float64
	for i := range dates {
		if i == 0 {
			h = close[0]
		} else {
			h = h * close[i] * mult[i] / close[i-1]
		}
		out[dates[i]] = h
	}
	return out
}
