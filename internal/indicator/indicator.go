// Package indicator 是纯函数指标库。所有函数逐日计算、只使用 t 及以前的数据（无未来函数）。
// 缺失值用 NaN 表示；输出与输入等长，无法计算的位置为 NaN。
package indicator

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// NaN 便于调用方书写。
var NaN = math.NaN()

func valid(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// Pct 计算 x 在样本 xs 中的分位（0–100），相等的样本计一半：
// (count(<x) + 0.5*count(==x)) / n * 100。
func Pct(xs []float64, x float64) float64 {
	var lt, eq int
	for _, v := range xs {
		switch {
		case v < x:
			lt++
		case v == x:
			eq++
		}
	}
	return (float64(lt) + 0.5*float64(eq)) / float64(len(xs)) * 100
}

// RollingPct 逐日计算 xs[t] 在"最近 w 个有效值（含 t）"中的分位。
// 有效样本不足 minW 时输出 NaN；样本少于 w 时用全部可得历史（扩展窗口）。
// 实现用有序窗口维护，复杂度 O(n·w) 的插入删除，对日频数据足够。
func RollingPct(xs []float64, w, minW int) []float64 {
	out := make([]float64, len(xs))
	var win []float64    // 按时间顺序的有效值
	var sorted []float64 // 同一批值的有序副本
	for t, x := range xs {
		out[t] = NaN
		if !valid(x) {
			continue
		}
		win = append(win, x)
		i := sort.SearchFloat64s(sorted, x)
		sorted = append(sorted, 0)
		copy(sorted[i+1:], sorted[i:])
		sorted[i] = x
		if len(win) > w {
			old := win[0]
			win = win[1:]
			j := sort.SearchFloat64s(sorted, old)
			sorted = append(sorted[:j], sorted[j+1:]...)
		}
		if len(win) < minW {
			continue
		}
		lt := sort.SearchFloat64s(sorted, x)
		le := sort.Search(len(sorted), func(k int) bool { return sorted[k] > x })
		out[t] = (float64(lt) + 0.5*float64(le-lt)) / float64(len(sorted)) * 100
	}
	return out
}

// SMA 简单移动平均；窗口内任一值缺失则输出 NaN。
func SMA(xs []float64, n int) []float64 {
	out := make([]float64, len(xs))
	for t := range xs {
		out[t] = NaN
		if t+1 < n {
			continue
		}
		sum := 0.0
		ok := true
		for _, v := range xs[t+1-n : t+1] {
			if !valid(v) {
				ok = false
				break
			}
			sum += v
		}
		if ok {
			out[t] = sum / float64(n)
		}
	}
	return out
}

// EMA 指数移动平均，alpha = 2/(n+1)，以第一个有效值为种子；遇到缺失值输出 NaN 并保持状态。
func EMA(xs []float64, n int) []float64 {
	out := make([]float64, len(xs))
	alpha := 2 / float64(n+1)
	prev, seeded := 0.0, false
	for t, x := range xs {
		out[t] = NaN
		if !valid(x) {
			continue
		}
		if !seeded {
			prev, seeded = x, true
		} else {
			prev = alpha*x + (1-alpha)*prev
		}
		out[t] = prev
	}
	return out
}

// Shift 把序列向后平移 k 位：out[t] = xs[t-k]。用于"使用 T-1 数据"。
func Shift(xs []float64, k int) []float64 {
	out := make([]float64, len(xs))
	for t := range xs {
		out[t] = NaN
		if t-k >= 0 {
			out[t] = xs[t-k]
		}
	}
	return out
}

// wilder 是 Wilder RSI 的增量状态（值语义：step 返回新状态，便于"试算"而不提交）。
type wilder struct {
	n                int
	started          bool
	prev             float64
	count            int // 已处理的涨跌个数
	avgGain, avgLoss float64
}

func (w wilder) step(close float64) wilder {
	if !w.started {
		w.started, w.prev = true, close
		return w
	}
	ch := close - w.prev
	g, l := math.Max(ch, 0), math.Max(-ch, 0)
	w.count++
	n := float64(w.n)
	if w.count <= w.n {
		// 前 n 个涨跌：累加，第 n 个时得到简单平均作为种子
		w.avgGain += g / n
		w.avgLoss += l / n
	} else {
		w.avgGain = (w.avgGain*(n-1) + g) / n
		w.avgLoss = (w.avgLoss*(n-1) + l) / n
	}
	w.prev = close
	return w
}

func (w wilder) value() float64 {
	if w.count < w.n {
		return NaN
	}
	if w.avgLoss == 0 {
		if w.avgGain == 0 {
			return 50
		}
		return 100
	}
	return 100 - 100/(1+w.avgGain/w.avgLoss)
}

// RSI 是 Wilder 平滑的 n 周期 RSI。前 n 个涨跌用简单平均做种子。closes 中不应有 NaN。
func RSI(closes []float64, n int) []float64 {
	out := make([]float64, len(closes))
	w := wilder{n: n}
	for t, c := range closes {
		w = w.step(c)
		out[t] = w.value()
	}
	return out
}

// WeeklyRSI 是"截至当日"的动态周线 RSI：已结束的周用周最后收盘价推进 Wilder 状态，
// 当前未结束的周以当日收盘价作为本周收盘"试算"一步。因此周中数值会随日变化（重绘），
// 但每个 t 只用到 t 及以前的数据，回测与实盘口径一致。dates 为 YYYY-MM-DD。
func WeeklyRSI(dates []string, closes []float64, n int) []float64 {
	out := make([]float64, len(closes))
	committed := wilder{n: n}
	curWeek := ""
	var lastClose float64
	for t, c := range closes {
		wk := isoWeek(dates[t])
		if curWeek != "" && wk != curWeek {
			committed = committed.step(lastClose) // 上一周结束，提交其收盘价
		}
		curWeek, lastClose = wk, c
		out[t] = committed.step(c).value()
	}
	return out
}

func isoWeek(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-%02d", y, w)
}
