// Package pmstrat 是短期涨跌盘（Up or Down）的公允概率模型，纯函数。
//
// 结算规则（docs/polymarket.md §2，实测 7,959 / 7,959 一致）：结束时刻的 Chainlink 60 秒 TWAP ≥ 起始价 K（计价开始时刻的同一 TWAP）则 Up。
// 模型：
//   - 用币安现价（2.5 秒一次）换算成 Chainlink 口径：Chainlink ≈ 币安 / (1 + basis)，basis 为 USDT/USD 基差，用最近已结算事件滚动校准；
//   - 对数价格按布朗运动（无漂移），波动率 σ 由近期现价估计；
//   - 结束时的 60 秒 TWAP：剩余时间 τ ≥ 60 秒时均值为当前价、方差 σ²(τ − 40)；τ < 60 秒时窗口中已过去的部分已知，
//     均值为已知部分均值与当前价按时长加权，方差 (τ/60)² σ² τ / 3；
//   - K 若是估计值，再加上它的误差方差；P(Up) = Φ((均值 − ln K) / 标准差)。
package pmstrat

import (
	"math"
	"sort"
)

// Point 是一个现价点（T 为 Unix 毫秒，P 为币安价）。
type Point struct {
	T int64
	P float64
}

// Inputs 是一次估值的输入。时间均为 Unix 秒。
type Inputs struct {
	Now, Start, End int64
	K               float64 // 起始价（Chainlink 口径）
	KErrBp          float64 // K 的误差（bp，1 个标准差）；精确值时为 0
	Basis           float64 // 币安相对 Chainlink 的溢价，如 0.0013
	Sigma           float64 // 对数价格波动率，每 √秒
	Window          int64   // TWAP 窗口（秒），默认 60
	Spot            []Point // 近期币安现价（升序），至少覆盖 [End − Window, Now] 中已过去的部分
}

// Result 是估值结果。
type Result struct {
	P     float64 `json:"p"`     // P(Up)
	Mean  float64 `json:"mean"`  // 结束 TWAP 的估计（Chainlink 口径）
	SdBp  float64 `json:"sdBp"`  // 对数标准差（bp）
	Tau   int64   `json:"tau"`   // 剩余秒数
	Score float64 `json:"score"` // (ln 均值 − ln K) / 标准差
}

// ProbUp 计算 P(Up)。没有足够输入（还没开始、没有现价、K 或 σ 无效）时 ok 为 false。
func ProbUp(in Inputs) (Result, bool) {
	w := in.Window
	if w <= 0 {
		w = 60
	}
	if in.Now < in.Start || in.Now >= in.End || in.K <= 0 || in.Sigma <= 0 || len(in.Spot) == 0 {
		return Result{}, false
	}
	conv := func(p float64) float64 { return math.Log(p / (1 + in.Basis)) }
	last := in.Spot[len(in.Spot)-1]
	if in.Now*1000-last.T > 15_000 { // 现价太旧（采集中断）：不估值
		return Result{}, false
	}
	s := conv(last.P)
	tau := in.End - in.Now
	var mean, variance float64
	if tau >= w {
		mean, variance = s, in.Sigma*in.Sigma*float64(tau-w+w/3)
	} else {
		// 窗口中已过去的部分 [End−w, Now] 的均值（对数价）
		from := (in.End - w) * 1000
		var sum float64
		var n int
		for _, p := range in.Spot {
			if p.T >= from && p.T <= in.Now*1000 {
				sum += conv(p.P)
				n++
			}
		}
		known := s
		if n > 0 {
			known = sum / float64(n)
		}
		ft := float64(tau) / float64(w)
		mean = (1-ft)*known + ft*s
		variance = ft * ft * in.Sigma * in.Sigma * float64(tau) / 3
	}
	kerr := in.KErrBp / 1e4
	sd := math.Sqrt(variance + kerr*kerr)
	if sd <= 0 {
		return Result{}, false
	}
	z := (mean - math.Log(in.K)) / sd
	return Result{P: Phi(z), Mean: math.Exp(mean), SdBp: sd * 1e4, Tau: tau, Score: z}, true
}

// Phi 是标准正态分布函数。
func Phi(z float64) float64 { return 0.5 * math.Erfc(-z/math.Sqrt2) }

// TwapBefore 返回 [t − w, t)（Unix 秒）内现价的均值与点数。
func TwapBefore(spot []Point, t, w int64) (float64, int) {
	i := sort.Search(len(spot), func(i int) bool { return spot[i].T >= (t-w)*1000 })
	var sum float64
	n := 0
	for ; i < len(spot) && spot[i].T < t*1000; i++ {
		sum += spot[i].P
		n++
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// Sigma 由等间隔（近似）的现价估计对数价格波动率（每 √秒）：相邻点的对数收益平方和 / 总时长。
func Sigma(spot []Point) float64 {
	var ss, dt float64
	for i := 1; i < len(spot); i++ {
		d := float64(spot[i].T-spot[i-1].T) / 1000
		if d <= 0 || d > 30 { // 跳过采集中断
			continue
		}
		r := math.Log(spot[i].P / spot[i-1].P)
		ss += r * r
		dt += d
	}
	if dt < 300 { // 少于 5 分钟的数据不可靠
		return 0
	}
	return math.Sqrt(ss / dt)
}

// Median 返回中位数（xs 会被排序）；空时返回 0。
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

// Edge 是按卖一吃 1 股的预期收益（扣手续费）：p − ask − fee。fee 为每股手续费（rate × ask × (1 − ask)）。
func Edge(p, ask, feeRate float64) float64 {
	if ask <= 0 || ask >= 1 {
		return math.Inf(-1)
	}
	return p - ask - feeRate*ask*(1-ask)
}
