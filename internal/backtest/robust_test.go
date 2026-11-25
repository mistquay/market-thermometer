package backtest

import (
	"strings"
	"testing"

	"market-thermometer/internal/engine"
)

func variant(vs []Variant, group string, level int) Variant {
	for _, v := range vs {
		if v.Group == group && v.Level == level {
			return v
		}
	}
	panic("no variant " + group)
}

func TestPerturb(t *testing.T) {
	base := engine.DefaultParams()
	vs := Perturb(base)
	if len(vs) != 2*len(perturbGroups) {
		t.Fatalf("variants: %d", len(vs))
	}
	// 普通阈值 ±20%
	if v := variant(vs, "armLow", -1); v.Params.Signal.ArmLowCheap != 24 || v.Params.Signal.ArmLowFair != 16 || v.Params.Signal.ArmLowExpensive != 12 {
		t.Fatalf("armLow-: %+v", v.Params.Signal)
	}
	// 上沿阈值按距 100 扰动：85 → 100-15×1.2=82 / 100-15×0.8=88
	if v := variant(vs, "armHigh", +1); v.Params.Signal.ArmHighCheap != 82 || v.Params.Signal.ArmHighFair != 76 || v.Skip != "" {
		t.Fatalf("armHigh+: %+v %s", v.Params.Signal, v.Skip)
	}
	if v := variant(vs, "armHigh", -1); v.Params.Signal.ArmHighCheap != 88 || v.Params.Signal.ArmHighExpensive != 80 {
		t.Fatalf("armHigh-: %+v", v.Params.Signal)
	}
	// 整数：EMASpan 3 → 2 / 4（取整后变化）；RiseDays 2 → 2 不变 → 改为 ±1
	if v := variant(vs, "emaSpan", -1); v.Params.EMASpan != 2 {
		t.Fatalf("ema-: %d", v.Params.EMASpan)
	}
	if lo, hi := variant(vs, "riseDays", -1), variant(vs, "riseDays", +1); lo.Params.Signal.RiseDays != 1 || hi.Params.Signal.RiseDays != 3 {
		t.Fatalf("riseDays: %d %d", lo.Params.Signal.RiseDays, hi.Params.Signal.RiseDays)
	}
	if v := variant(vs, "armTimeout", +1); v.Params.Signal.ArmTimeout != 72 || !strings.Contains(v.Values, "72") {
		t.Fatalf("armTimeout+: %+v", v)
	}
	// 只动一组：其余参数与基准相同
	v := variant(vs, "tier", +1)
	if v.Params.CheapBelow != 36 || v.Params.ExpensiveAbove != 64 || v.Params.Signal != base.Signal || v.Params.EMASpan != base.EMASpan {
		t.Fatalf("tier+: %+v", v.Params)
	}
	// 基准不被修改
	if base != engine.DefaultParams() {
		t.Fatal("base mutated")
	}

	// 不合法的变体标记跳过：滞回 14 时，估值档界线收窄到 36 / 64 后滞回超过间距的一半
	b2 := engine.DefaultParams()
	b2.TierHysteresis = 14
	if v := variant(Perturb(b2), "tier", +1); !strings.Contains(v.Skip, "不合法") {
		t.Fatalf("want skip, got %q", v.Skip)
	}
}

func TestYears(t *testing.T) {
	var days []engine.Day
	for _, d := range []string{"2013-12-30", "2013-12-31", "2014-01-02", "2014-06-30", "2014-12-31", "2015-01-05", "2015-03-02"} {
		days = append(days, engine.Day{Date: d, Close: 100})
	}
	ws := Years(days, nil, Config{From: "2014-01-01", Horizons: []int{20}, MAEWindow: 20})
	if len(ws) != 2 || ws[0].From != "2014-01-02" || ws[0].To != "2014-12-31" || ws[0].Label != "2014" {
		t.Fatalf("windows: %+v", ws)
	}
	if ws[1].Label != "2015（不完整）" || ws[1].To != "2015-03-02" {
		t.Fatalf("last: %+v", ws[1])
	}
}
