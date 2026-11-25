package engine

import (
	"math"
	"testing"
)

// mk 用 T_s 序列和收盘价序列构造输入；子维度个数直接给定。
func mk(ts []float64, closes []float64, nLow, nHigh int) []Day {
	days := make([]Day, len(ts))
	for i := range ts {
		days[i] = Day{Date: dateN(i), TS: ts[i], Close: closes[i], Tier: TierFair, V: 50, RSID: 50, NLow: nLow, NHigh: nHigh}
	}
	return days
}

func dateN(i int) string { return "2020-01-" + string(rune('A'+i/26)) + string(rune('a'+i%26)) }

func types(evs []Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBottomReboundTriggersOnceThenCooldown(t *testing.T) {
	p := DefaultSignalParams()
	ts := []float64{40, 25, 18, 12, 10, 13, 19, 21, 30, 20, 18, 36, 40}
	//            0   1   2   3   4   5   6   7   8   9  10  11  12
	// t=2 武装（≤20）；最低 10；t=6：19 ≥ 10+8 且 13→19 连续 2 日上升（10→13→19）→ BUY
	// 冷却期内 t=9/10 再次 ≤20 不得重新武装；t=11 T_s>35 回到 IDLE
	closes := make([]float64, len(ts))
	for i := range closes {
		closes[i] = 100
	}
	states, evs := RunSignals(mk(ts, closes, 3, 0), p)
	if got := types(evs); !eq(got, []string{EvArmedBottom, EvBuyDip}) {
		t.Fatalf("events %v", got)
	}
	if evs[0].Date != dateN(2) || evs[1].Date != dateN(6) {
		t.Fatalf("dates %v %v", evs[0].Date, evs[1].Date)
	}
	if states[10].Phase != PhaseBottomCooldown || states[11].Phase != PhaseIdle {
		t.Fatalf("cooldown phases %v %v", states[10].Phase, states[11].Phase)
	}
}

func TestBottomDivergence(t *testing.T) {
	p := DefaultSignalParams()
	ts := []float64{15, 10, 12, 16, 15}
	closes := []float64{100, 95, 96, 94, 93}
	// 武装 t=0，最低 T_s=10 时收盘 95；t=3 收盘 94 < 95 且 T_s 16 ≥ 10+5 → 底背离
	_, evs := RunSignals(mk(ts, closes, 2, 0), p)
	if got := types(evs); !eq(got, []string{EvArmedBottom, EvBuyDip}) || evs[1].Date != dateN(3) {
		t.Fatalf("events %v", evs)
	}
}

func TestArmRequiresExtremeDimsAndRespectsTier(t *testing.T) {
	p := DefaultSignalParams()
	ts := []float64{18, 18}
	closes := []float64{1, 1}
	if _, evs := RunSignals(mk(ts, closes, 1, 0), p); len(evs) != 0 {
		t.Fatalf("only 1 cold dim must not arm: %v", types(evs))
	}
	// 估值昂贵时底部武装线收紧到 15：T_s=18 不武装
	days := mk(ts, closes, 3, 0)
	for i := range days {
		days[i].Tier = TierExpensive
	}
	if _, evs := RunSignals(days, p); len(evs) != 0 {
		t.Fatalf("expensive tier should not arm at 18: %v", types(evs))
	}
	// 估值便宜时放宽到 30：T_s=28 武装
	days = mk([]float64{28}, []float64{1}, 2, 0)
	days[0].Tier = TierCheap
	if _, evs := RunSignals(days, p); !eq(types(evs), []string{EvArmedBottom}) {
		t.Fatalf("cheap tier should arm at 28: %v", types(evs))
	}
}

func TestTopPullbackAndTimeout(t *testing.T) {
	p := DefaultSignalParams()
	ts := []float64{85, 90, 88, 81}
	closes := []float64{100, 101, 102, 103}
	days := mk(ts, closes, 0, 2)
	days[3].RSID = 65 // 回落 9 ≥ 8 且 RSI < 70 → SELL
	_, evs := RunSignals(days, p)
	if got := types(evs); !eq(got, []string{EvArmedTop, EvSellPeak}) || evs[1].Date != dateN(3) {
		t.Fatalf("events %v", got)
	}

	p.ArmTimeout = 2
	ts = []float64{85, 86, 87, 88, 89}
	_, evs = RunSignals(mk(ts, []float64{1, 2, 3, 4, 5}, 0, 2), p)
	// 超时失效后进入冷却：T_s 仍 ≥ 80 也不得当日或随后重新武装
	if got := types(evs); !eq(got, []string{EvArmedTop, EvDisarmed}) || evs[1].Date != dateN(3) {
		t.Fatalf("timeout events %v", evs)
	}
}

func TestDegradedDayDoesNotTriggerAndNaNSkipped(t *testing.T) {
	p := DefaultSignalParams()
	ts := []float64{18, 10, 14, 20, math.NaN(), 22}
	days := mk(ts, []float64{1, 1, 1, 1, 1, 1}, 3, 0)
	days[3].Degraded = true // 本应在 t=3 触发（10→14→20）
	_, evs := RunSignals(days, p)
	got := types(evs)
	// t=3 只记降级；t=4 NaN 跳过；t=5：22 ≥ 18 且 14→20→22 连续上升 → BUY
	if !eq(got, []string{EvArmedBottom, EvDegraded, EvBuyDip}) || evs[2].Date != dateN(5) {
		t.Fatalf("events %v", evs)
	}
}

func TestTierChangeEvent(t *testing.T) {
	days := mk([]float64{50, 50}, []float64{1, 1}, 0, 0)
	days[1].Tier = TierCheap
	if _, evs := RunSignals(days, DefaultSignalParams()); !eq(types(evs), []string{EvTierChange}) {
		t.Fatalf("events %v", types(evs))
	}
}

func TestTopOnlyExpensive(t *testing.T) {
	p := DefaultSignalParams()
	p.TopOnlyExpensive = true
	days := mk([]float64{90, 90}, []float64{1, 1}, 0, 3)
	if _, evs := RunSignals(days, p); len(evs) != 0 {
		t.Fatalf("fair tier must not arm top: %v", types(evs))
	}
	days[1].Tier = TierExpensive
	if _, evs := RunSignals(days, p); !eq(types(evs), []string{EvTierChange, EvArmedTop}) {
		t.Fatalf("expensive tier should arm: %v", types(evs))
	}
}
