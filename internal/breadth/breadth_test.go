package breadth

import (
	"fmt"
	"math"
	"testing"

	ds "market-thermometer/internal/datasource"
)

func day(i int) string { return fmt.Sprintf("2020-%02d-%02d", 1+i/28, 1+i%28) }

func bars(closes []float64, mults map[int]float64) []ds.StockBar {
	out := make([]ds.StockBar, len(closes))
	for i, c := range closes {
		m := mults[i]
		if m == 0 {
			m = 1
		}
		out[i] = ds.StockBar{Date: day(i), Close: c, Mult: m}
	}
	return out
}

// 除权：10 送 10 后不复权价腰斩，乘数 2；后复权价应连续，不应因此跌破均线。
func TestFeedAdjustsForExRights(t *testing.T) {
	closes := []float64{10, 10, 10, 10, 5.1} // 第 5 天除权，复权后 10.2，涨 2%
	c := Counts{}
	st := Feed(State{}, bars(closes, map[int]float64{4: 2}), 3, "", c)
	if !near(st.LastHfq, 10.2) {
		t.Fatalf("hfq %v", st.LastHfq)
	}
	if got := c[day(4)]; got.Total != 1 || got.Above != 1 {
		t.Fatalf("ex-rights day should be above MA: %+v", got)
	}
	if got := c[day(1)]; got.Total != 0 {
		t.Fatalf("not enough bars yet, should not count: %+v", got)
	}
}

// 增量与一次性计算结果一致，且重复喂入的 K 线被跳过。
func TestFeedIncrementalEqualsFull(t *testing.T) {
	closes := []float64{10, 11, 9, 12, 13, 8, 9, 10, 15, 14, 7, 7.5}
	mults := map[int]float64{6: 1.1}
	full := Counts{}
	stFull := Feed(State{}, bars(closes, mults), 4, day(2), full)

	inc := Counts{}
	b := bars(closes, mults)
	st := Feed(State{}, b[:7], 4, day(2), inc)
	st = Feed(st, b[5:], 4, day(2), inc) // 与上一段重叠 2 根，应跳过
	if fmt.Sprint(st) != fmt.Sprint(stFull) {
		t.Fatalf("state mismatch\n inc %+v\nfull %+v", st, stFull)
	}
	if fmt.Sprint(inc) != fmt.Sprint(full) {
		t.Fatalf("counts mismatch\n inc %v\nfull %v", inc, full)
	}
	if _, ok := full[day(2)]; ok {
		t.Fatal("day 2 has only 3 bars, MA4 not ready")
	}
	// 手算第 4 天（下标 3）：窗口 10,11,9,12 均值 10.5，12 > 10.5 → 站上
	if got := full[day(3)]; got != (Count{Above: 1, Total: 1}) {
		t.Fatalf("day3 %+v", got)
	}
	// 下标 5：窗口 9,12,13,8 均值 10.5，8 < 10.5 → 未站上
	if got := full[day(5)]; got != (Count{Above: 0, Total: 1}) {
		t.Fatalf("day5 %+v", got)
	}
}

func TestPercentSkipsIncompleteDays(t *testing.T) {
	c := Counts{}
	var dates []string
	for i := range 10 {
		dates = append(dates, day(i))
		c[day(i)] = Count{Above: 50, Total: 100}
	}
	c[day(8)] = Count{Above: 10, Total: 20} // 数据源只更新了少数股票
	pts := Percent(dates, c)
	if len(pts) != 9 || pts[0].Value != 50 {
		t.Fatalf("pts %v", pts)
	}
	for _, p := range pts {
		if p.Date == day(8) {
			t.Fatal("incomplete day should be skipped")
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
