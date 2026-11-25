package indicator

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// 手算：closes=[1,2,1,3,2]，n=2。涨跌 +1,-1,+2,-1
//
//	t=2 种子：avgG=(1+0)/2=0.5, avgL=(0+1)/2=0.5 → 50
//	t=3：avgG=(0.5+2)/2=1.25, avgL=(0.5+0)/2=0.25 → RS=5 → 83.333…
//	t=4：avgG=(1.25+0)/2=0.625, avgL=(0.25+1)/2=0.625 → 50
func TestRSIHandComputed(t *testing.T) {
	got := RSI([]float64{1, 2, 1, 3, 2}, 2)
	want := []float64{NaN, NaN, 50, 100 - 100.0/6, 50}
	for i, w := range want {
		if math.IsNaN(w) != math.IsNaN(got[i]) || (!math.IsNaN(w) && !near(got[i], w, 1e-12)) {
			t.Fatalf("rsi[%d]=%v want %v", i, got[i], w)
		}
	}
}

// 14 周期：首个值与另行用 Python 逐项手工公式算出的 70.4641 一致。
func TestRSI14FirstValue(t *testing.T) {
	closes := []float64{44.34, 44.09, 44.15, 43.61, 44.33, 44.83, 45.10, 45.42, 45.84, 46.08, 45.89, 46.03, 45.61, 46.28, 46.28}
	got := RSI(closes, 14)
	if !math.IsNaN(got[13]) || !near(got[14], 70.4641, 1e-4) {
		t.Fatalf("got %v / %v", got[13], got[14])
	}
}

func TestRSIFlat(t *testing.T) {
	r := RSI([]float64{1, 1, 1, 1}, 2)
	if r[2] != 50 || r[3] != 50 {
		t.Fatalf("flat series RSI %v", r)
	}
}

func naivePct(xs []float64, t, w, minW int) float64 {
	var win []float64
	for i := t; i >= 0 && len(win) < w; i-- {
		if valid(xs[i]) {
			win = append(win, xs[i])
		}
	}
	if len(win) < minW || !valid(xs[t]) {
		return NaN
	}
	return Pct(win, xs[t])
}

func TestRollingPctMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	xs := make([]float64, 400)
	for i := range xs {
		xs[i] = float64(r.IntN(30)) // 大量重复值，覆盖"相等计一半"
		if r.IntN(10) == 0 {
			xs[i] = NaN
		}
	}
	got := RollingPct(xs, 50, 20)
	for i := range xs {
		want := naivePct(xs, i, 50, 20)
		if math.IsNaN(want) != math.IsNaN(got[i]) || (!math.IsNaN(want) && !near(want, got[i], 1e-9)) {
			t.Fatalf("t=%d got %v want %v", i, got[i], want)
		}
	}
}

func TestPctHandComputed(t *testing.T) {
	// 样本 {1,2,2,3}，x=2：小于 1 个，等于 2 个 → (1+1)/4 = 50
	if p := Pct([]float64{1, 2, 2, 3}, 2); p != 50 {
		t.Fatalf("got %v", p)
	}
	// 扩展窗口：前 2 个不足 minW=3
	got := RollingPct([]float64{3, 1, 2, 5}, 10, 3)
	if !math.IsNaN(got[1]) || got[2] != 50 || got[3] != 87.5 {
		t.Fatalf("got %v", got)
	}
}

func TestSMAEMAShift(t *testing.T) {
	s := SMA([]float64{1, 2, 3, NaN, 5, 6}, 2)
	if !math.IsNaN(s[0]) || s[1] != 1.5 || s[2] != 2.5 || !math.IsNaN(s[3]) || !math.IsNaN(s[4]) || s[5] != 5.5 {
		t.Fatalf("sma %v", s)
	}
	e := EMA([]float64{2, 4, NaN, 4}, 3) // alpha=0.5
	if e[0] != 2 || e[1] != 3 || !math.IsNaN(e[2]) || e[3] != 3.5 {
		t.Fatalf("ema %v", e)
	}
	sh := Shift([]float64{1, 2, 3}, 1)
	if !math.IsNaN(sh[0]) || sh[1] != 1 || sh[2] != 2 {
		t.Fatalf("shift %v", sh)
	}
}

// 动态周 RSI：每周最后一天的值 = 用"每周收盘价"直接算的 RSI；周中某天的值 = 把当日收盘当作本周收盘的结果。
func TestWeeklyRSIMatchesWeeklyCloses(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	var dates []string
	var closes []float64
	d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // 周一
	price := 100.0
	for len(dates) < 300 {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday && r.IntN(15) != 0 { // 随机缺几天（节假日）
			price *= 1 + (r.Float64()-0.5)*0.04
			dates = append(dates, d.Format("2006-01-02"))
			closes = append(closes, price)
		}
		d = d.AddDate(0, 0, 1)
	}
	got := WeeklyRSI(dates, closes, 14)

	var weekly []float64 // 已结束各周的收盘价
	for i := range dates {
		last := i == len(dates)-1 || isoWeek(dates[i+1]) != isoWeek(dates[i])
		want := RSI(append(append([]float64(nil), weekly...), closes[i]), 14)
		w := want[len(want)-1]
		if math.IsNaN(w) != math.IsNaN(got[i]) || (!math.IsNaN(w) && !near(w, got[i], 1e-9)) {
			t.Fatalf("day %d (%s): got %v want %v", i, dates[i], got[i], w)
		}
		if last {
			weekly = append(weekly, closes[i])
		}
	}
	if len(weekly) < 40 {
		t.Fatalf("test data too short: %d weeks", len(weekly))
	}
}
