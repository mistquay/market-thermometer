package engine

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/market"
)

func TestAlignFillAndCalendar(t *testing.T) {
	raw := map[market.Role][]ds.Point{
		market.RoleClose:     {{Date: "2026-01-05", Value: 1}, {Date: "2026-01-06", Value: 2}, {Date: "2026-01-07", Value: 3}, {Date: "2026-01-08", Value: 4}},
		market.RolePE:        {{Date: "2026-01-04", Value: 10}, {Date: "2026-01-07", Value: 11}}, // 01-04 为非交易日
		market.RoleMarginBuy: {{Date: "2026-01-05", Value: 5}, {Date: "2026-01-07", Value: 7}},
	}
	in := Align(raw, 1)
	pe, mb := in.Series[market.RolePE], in.Series[market.RoleMarginBuy]
	// PE 是慢变量：01-04 的值在 01-05 读入，可填充 1 天到 01-06；01-07 有新值，01-08 填充
	wantPE := []float64{10, 10, 11, 11}
	for i, w := range wantPE {
		if pe[i] != w {
			t.Fatalf("pe %v", pe)
		}
	}
	// 融资买入额不填充：01-06、01-08 缺失
	if mb[0] != 5 || !math.IsNaN(mb[1]) || mb[2] != 7 || !math.IsNaN(mb[3]) {
		t.Fatalf("margin %v", mb)
	}
	// 慢变量超过填充上限后缺失
	in2 := Align(map[market.Role][]ds.Point{
		market.RoleClose: raw[market.RoleClose],
		market.RolePE:    {{Date: "2026-01-05", Value: 10}},
	}, 1)
	if p := in2.Series[market.RolePE]; p[1] != 10 || !math.IsNaN(p[2]) {
		t.Fatalf("fill limit %v", p)
	}
}

func synthetic(n int) Inputs {
	r := rand.New(rand.NewPCG(7, 8))
	in := Inputs{Series: map[market.Role][]float64{}}
	d := time.Date(2010, 1, 4, 0, 0, 0, 0, time.UTC)
	price, pe := 3000.0, 12.0
	for len(in.Dates) < n {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			price *= 1 + (r.Float64()-0.5)*0.04
			pe *= 1 + (r.Float64()-0.5)*0.02
			in.Dates = append(in.Dates, d.Format("2006-01-02"))
			in.Series[market.RoleClose] = append(in.Series[market.RoleClose], price)
			in.Series[market.RolePE] = append(in.Series[market.RolePE], pe)
			in.Series[market.RoleBondY10] = append(in.Series[market.RoleBondY10], 2+r.Float64())
			in.Series[market.RoleAmount] = append(in.Series[market.RoleAmount], 1e12*(0.5+r.Float64()))
			in.Series[market.RoleFloatMV] = append(in.Series[market.RoleFloatMV], 5e13*(0.9+0.2*r.Float64()))
			in.Series[market.RoleMarginBuy] = append(in.Series[market.RoleMarginBuy], 8e10*(0.5+r.Float64()))
			in.Series[market.RoleBreadth] = append(in.Series[market.RoleBreadth], 100*r.Float64())
		}
		d = d.AddDate(0, 0, 1)
	}
	return in
}

func truncate(in Inputs, n int) Inputs {
	out := Inputs{Dates: in.Dates[:n], Series: map[market.Role][]float64{}}
	for k, v := range in.Series {
		out.Series[k] = v[:n]
	}
	return out
}

func sameF(a, b float64) bool { return (math.IsNaN(a) && math.IsNaN(b)) || math.Abs(a-b) < 1e-9 }

// 无未来函数：只用前 t+1 天数据算出的第 t 天结果，必须与用全量数据算出的第 t 天结果完全一致。
func TestComputeNoLookahead(t *testing.T) {
	p := DefaultParams()
	p.ValuationWindow, p.ValuationMinW, p.TimingWindow, p.TimingMinW = 300, 100, 200, 60
	in := synthetic(900)
	full := Compute(in, p)
	for _, n := range []int{150, 333, 501, 702, 900} {
		part := Compute(truncate(in, n), p)
		a, b := part[n-1], full[n-1]
		if !sameF(a.V, b.V) || !sameF(a.TS, b.TS) || !sameF(a.TBase, b.TBase) || !sameF(a.RSIW, b.RSIW) || a.Degraded != b.Degraded {
			t.Fatalf("day %d differs: partial=%+v full=%+v", n-1, a, b)
		}
	}
	last := full[len(full)-1]
	if math.IsNaN(last.V) || math.IsNaN(last.TS) || last.Tier == "" || last.Degraded {
		t.Fatalf("last day should be fully computed: %+v", last)
	}
	if last.TS < 0 || last.TS > 100 || last.V < 0 || last.V > 100 {
		t.Fatalf("scores out of range: %+v", last)
	}
}

func TestComputeDegradedAndDisabled(t *testing.T) {
	p := DefaultParams()
	p.TimingWindow, p.TimingMinW = 200, 60
	p.Weights[2] = 0 // 不启用 T3：没有广度数据也不算降级
	in := synthetic(400)
	delete(in.Series, market.RoleBreadth)
	in.Series[market.RoleMarginBuy][398] = math.NaN() // T2 用 T-1 数据：第 399 天的 T2 读的是第 398 天
	days := Compute(in, p)
	d := days[399]
	if !math.IsNaN(d.T[1]) || !d.Degraded || math.IsNaN(d.TBase) {
		t.Fatalf("one enabled dim missing should degrade, not drop: %+v", d)
	}
	if !math.IsNaN(d.T[2]) {
		t.Fatal("T3 is disabled and should stay NaN")
	}
	if days[300].Degraded {
		t.Fatal("disabled T3 must not count as degraded")
	}
	// 两个已启用维度缺失 → 当日 T 不计算
	in.Series[market.RoleAmount][399] = math.NaN()
	if d := Compute(in, p)[399]; !math.IsNaN(d.TBase) {
		t.Fatalf("two missing dims should drop T: %+v", d)
	}
}

func TestTierHysteresis(t *testing.T) {
	p := DefaultParams() // 便宜 ≤30，昂贵 ≥70，滞回 5
	seq := []float64{40, 29, 33, 35.5, 32, 71, 66, 64}
	want := []string{TierFair, TierCheap, TierCheap, TierFair, TierFair, TierExpensive, TierExpensive, TierFair}
	prev := ""
	for i, v := range seq {
		prev = nextTier(prev, v, p)
		if prev != want[i] {
			t.Fatalf("step %d v=%v: got %s want %s", i, v, prev, want[i])
		}
	}
	if nextTier(TierCheap, math.NaN(), p) != "" {
		t.Fatal("NaN V should have no tier")
	}
}

func TestDefaultParamsValid(t *testing.T) {
	if err := DefaultParams().Validate(); err != nil {
		t.Fatal(err)
	}
	p := DefaultParams()
	p.TimingMinW = p.TimingWindow + 1
	p.Weights = [4]float64{}
	if err := p.Validate(); err == nil {
		t.Fatal("invalid params accepted")
	}
}
