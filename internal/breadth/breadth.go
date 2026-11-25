// Package breadth 计算市场广度：全 A 股中收盘价站上自身 N 日均线（后复权）的个股占比。
// 纯函数，逐只股票增量处理：每只股票保存最近 N 个后复权收盘价作为状态，每日按股票累加计数。
// 计数是可加的，因此个股数据晚到时后补，结果与一次性计算一致。
package breadth

import (
	ds "market-thermometer/internal/datasource"
)

// State 是一只股票的增量状态。后复权价以该股第一根 K 线的收盘价为基准（MA 比较与基准无关）。
type State struct {
	LastDate string    `json:"d"`
	LastRaw  float64   `json:"r"` // 最后一根 K 线的不复权收盘
	LastHfq  float64   `json:"h"` // 最后一根 K 线的后复权收盘
	Window   []float64 `json:"w"` // 最近 N 个后复权收盘（时间顺序）
}

// Count 是某日的计数：站上均线的股票数 / 有效股票数（已有满 N 根 K 线且当日有成交）。
type Count struct {
	Above int
	Total int
}

// Counts 按日期（YYYY-MM-DD）累加计数。
type Counts map[string]Count

// Feed 处理一只股票的新 K 线（日期 ≤ st.LastDate 的会被跳过），更新状态并把 countFrom 及以后日期的结果累加进 c。
// 返回新状态；没有新 K 线时原样返回。
func Feed(st State, bars []ds.StockBar, n int, countFrom string, c Counts) State {
	sum := 0.0
	for _, v := range st.Window {
		sum += v
	}
	for _, b := range bars {
		if b.Date <= st.LastDate || b.Close <= 0 {
			continue
		}
		hfq := b.Close
		if st.LastRaw > 0 {
			hfq = st.LastHfq * b.Close * b.Mult / st.LastRaw
		}
		st.Window = append(st.Window, hfq)
		sum += hfq
		if len(st.Window) > n {
			sum -= st.Window[0]
			st.Window = append(st.Window[:0], st.Window[1:]...)
		}
		if len(st.Window) == n && b.Date >= countFrom {
			cnt := c[b.Date]
			cnt.Total++
			if hfq > sum/float64(n) {
				cnt.Above++
			}
			c[b.Date] = cnt
		}
		st.LastDate, st.LastRaw, st.LastHfq = b.Date, b.Close, hfq
	}
	return st
}

// Percent 把计数转换为站上均线的占比（%）。dates 须升序；当日有效股票数低于前 20 个已发布日期中位数的一半时，
// 视为当日数据不完整（数据源尚未更新完），不输出该日。
func Percent(dates []string, c Counts) []ds.Point {
	var out []ds.Point
	var recent []int
	for _, d := range dates {
		cnt, ok := c[d]
		if !ok || cnt.Total == 0 {
			continue
		}
		if len(recent) >= 5 && cnt.Total*2 < median(recent) {
			continue
		}
		out = append(out, ds.Point{Date: d, Value: float64(cnt.Above) / float64(cnt.Total) * 100})
		recent = append(recent, cnt.Total)
		if len(recent) > 20 {
			recent = recent[1:]
		}
	}
	return out
}

func median(xs []int) int {
	s := append([]int(nil), xs...)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s[len(s)/2]
}
