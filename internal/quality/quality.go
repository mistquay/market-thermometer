// Package quality 是数据质量检测的纯函数：缺口、按日覆盖、新鲜度、多来源日线比对。
// 只做检测与展示，不修补数据。
package quality

import (
	"math"
	"sort"
)

// Run 是一段连续缺失的交易日。
type Run struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
}

// Gaps 返回 dates（某证券有日线的日期，升序）在其首尾之间缺失的交易日（calendar 升序）。
// 停牌也会表现为缺口，调用方需提示“可能是停牌”。
func Gaps(calendar, dates []string) []Run {
	if len(dates) < 2 {
		return nil
	}
	have := make(map[string]bool, len(dates))
	for _, d := range dates {
		have[d] = true
	}
	first, last := dates[0], dates[len(dates)-1]
	var out []Run
	var cur *Run
	for _, d := range calendar {
		if d <= first || d >= last {
			continue
		}
		if have[d] {
			cur = nil
			continue
		}
		if cur == nil {
			out = append(out, Run{From: d, To: d})
			cur = &out[len(out)-1]
		}
		cur.To = d
		cur.Days++
	}
	return out
}

// DayCount 是某个交易日有日线的证券数。
type DayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
	Low   bool   `json:"low"` // 低于这段时间中位数的 ratio
}

// Coverage 按交易日列出证券数，低于中位数 × ratio 的标记为 Low。
// 只统计 calendar 中不晚于 lastDate（本地日线最新日期）的日子，最新日未同步不算缺失。
func Coverage(calendar []string, counts map[string]int, lastDate string, ratio float64) []DayCount {
	var out []DayCount
	var vals []int
	for _, d := range calendar {
		if d > lastDate {
			break
		}
		out = append(out, DayCount{Date: d, Count: counts[d]})
		vals = append(vals, counts[d])
	}
	if len(vals) == 0 {
		return nil
	}
	sort.Ints(vals)
	med := float64(vals[len(vals)/2])
	for i := range out {
		out[i].Low = float64(out[i].Count) < med*ratio
	}
	return out
}

// Behind 返回 last 相对 expected 落后的交易日数（calendar 升序）；不落后为 0，last 为空时返回 -1。
func Behind(calendar []string, last, expected string) int {
	if last == "" {
		return -1
	}
	if last >= expected {
		return 0
	}
	i := sort.SearchStrings(calendar, expected)
	j := sort.SearchStrings(calendar, last)
	if j < len(calendar) && calendar[j] == last {
		j++ // last 本身是交易日：从它的下一天算起
	}
	return max(i-j+1, 1)
}

// Bar 是用于比对的一根日线（与 datasource.StockBar 字段一致，避免依赖）。
type Bar struct {
	Date  string
	Close float64
	Mult  float64
}

// Diff 是一处差异。
type Diff struct {
	Code   string  `json:"code"`
	Src    string  `json:"src,omitempty"` // 比对的数据源（由调用方填写）
	Date   string  `json:"date"`
	Kind   string  `json:"kind"` // close | mult | only_local | only_source
	Local  float64 `json:"local"`
	Source float64 `json:"source"`
	Recent bool    `json:"recent"` // 在近期（数据源可能仍是临时值）
}

// Tolerance 是收盘价、除权乘数的相对容差。
const Tolerance = 1e-3

// CompareBars 以本地数据为基准比较一只证券的日线。只比较双方都覆盖的区间：
// 本地缺、来源有（only_source）只统计在本地首日之后的日期；来源缺、本地有（only_local）只统计在来源首尾之间的日期。
// 日期 ≥ recentFrom 的差异标记为近期。
func CompareBars(code string, local, src []Bar, recentFrom string) (compared int, diffs []Diff) {
	if len(local) == 0 || len(src) == 0 {
		return 0, nil
	}
	lm := make(map[string]Bar, len(local))
	for _, b := range local {
		lm[b.Date] = b
	}
	sm := make(map[string]Bar, len(src))
	for _, b := range src {
		sm[b.Date] = b
	}
	add := func(d Diff) {
		d.Code, d.Recent = code, d.Date >= recentFrom
		diffs = append(diffs, d)
	}
	for _, s := range src {
		l, ok := lm[s.Date]
		if !ok {
			if s.Date > local[0].Date && s.Date <= local[len(local)-1].Date {
				add(Diff{Date: s.Date, Kind: "only_source", Source: s.Close})
			}
			continue
		}
		compared++
		if rel(l.Close, s.Close) > Tolerance {
			add(Diff{Date: s.Date, Kind: "close", Local: l.Close, Source: s.Close})
		}
		if rel(l.Mult, s.Mult) > Tolerance {
			add(Diff{Date: s.Date, Kind: "mult", Local: l.Mult, Source: s.Mult})
		}
	}
	for _, l := range local {
		if _, ok := sm[l.Date]; !ok && l.Date > src[0].Date && l.Date < src[len(src)-1].Date {
			add(Diff{Date: l.Date, Kind: "only_local", Local: l.Close})
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Date < diffs[j].Date })
	return compared, diffs
}

func rel(a, b float64) float64 {
	if a == 0 {
		return math.Abs(b)
	}
	return math.Abs(b/a - 1)
}
