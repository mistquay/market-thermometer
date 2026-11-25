package backtest

import (
	"math"
	"testing"

	"market-thermometer/internal/engine"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func mkDays(closes []float64) []engine.Day {
	days := make([]engine.Day, len(closes))
	for i, c := range closes {
		days[i] = engine.Day{Date: "2020-01-" + string(rune('A'+i/26)) + string(rune('a'+i%26)), Close: c,
			V: 50, Tier: engine.TierFair, TS: 50, T: [4]float64{float64(i), float64(2 * i), math.NaN(), float64(-i)}}
	}
	return days
}

func TestForwardReturnsMAEAndEquity(t *testing.T) {
	closes := []float64{100, 90, 80, 100, 120, 110, 132}
	days := mkDays(closes)
	evs := []engine.Event{
		{Date: days[1].Date, Idx: 1, Type: engine.EvBuyDip, Close: 90},
		{Date: days[4].Date, Idx: 4, Type: engine.EvSellPeak, Close: 120},
	}
	cfg := Config{From: days[0].Date, Split: days[3].Date, Horizons: []int{2, 10}, MAEWindow: 2}
	r := Run(days, evs, cfg)

	var buy, sell *Signal
	for i := range r.Signals {
		s := &r.Signals[i]
		if s.Source == "thermometer" && s.Side > 0 {
			buy = s
		}
		if s.Source == "thermometer" && s.Side < 0 {
			sell = s
		}
	}
	// 买入 t=1（90）：2 日后 t=3=100 → +11.1%；10 日后越界 → nil；MAE 窗口 t=2..3 最低 80 → -11.1%
	if buy == nil || !near(*buy.Rets[0], 100.0/90-1) || buy.Rets[1] != nil || !near(*buy.MAE, 80.0/90-1) {
		t.Fatalf("buy %+v", buy)
	}
	// 卖出 t=4（120）：2 日后 t=6=132 → +10%（看空亏损）；MAE 取期间最大涨幅 t=5..6 → +10%
	if sell == nil || !near(*sell.Rets[0], 0.1) || !near(*sell.MAE, 0.1) {
		t.Fatalf("sell %+v", sell)
	}
	// 温度计持仓：t=1 收盘买入，t=2..4 的收益计入（80/90*100/80*120/100 = 120/90），t=4 收盘卖出
	var thermo Strategy
	for _, s := range r.Strategies {
		if s.Name == "thermometer" {
			thermo = s
		}
	}
	if !near(thermo.TotalRet, 120.0/90-1) || thermo.Switches != 2 {
		t.Fatalf("thermo %+v", thermo)
	}
	// 回撤：持仓期间 90→80 为 -11.1%
	if !near(thermo.MaxDD, 80.0/90-1) {
		t.Fatalf("maxDD %v", thermo.MaxDD)
	}
	// 买入持有 = 132/100
	for _, s := range r.Strategies {
		if s.Name == "hold" && !near(s.TotalRet, 0.32) {
			t.Fatalf("hold %v", s.TotalRet)
		}
	}
	// 分组：样本内（< t=3）含买入，样本外含卖出
	got := map[string]int{}
	for _, g := range r.Groups {
		if g.Source == "thermometer" {
			got[g.Period+map[int]string{1: "+", -1: "-"}[g.Side]] = g.N
		}
	}
	if got["all+"] != 1 || got["in+"] != 1 || got["out-"] != 1 || got["in-"] != 0 {
		t.Fatalf("groups %v", got)
	}
	// 无条件基准：2 日前瞻共 5 个样本
	if r.BaseRate[0].N != 5 {
		t.Fatalf("base rate %+v", r.BaseRate[0])
	}
	// 相关矩阵：T1 与 T2 完全正相关，与 T4 完全负相关；T3 全空不出现
	if len(r.CorrNames) != 3 || !near(*r.Corr[0][1], 1) || !near(*r.Corr[0][2], -1) {
		t.Fatalf("corr %v %v", r.CorrNames, r.Corr)
	}
}

func TestObserveSignalExcludedFromStatsAndPosition(t *testing.T) {
	days := mkDays([]float64{100, 100, 110, 121})
	evs := []engine.Event{{Date: days[0].Date, Idx: 0, Type: engine.EvBuyDip, Observe: true, Close: 100}}
	r := Run(days, evs, Config{From: days[0].Date, Split: "9999", Horizons: []int{1}, MAEWindow: 1})
	for _, g := range r.Groups {
		if g.Source == "thermometer" {
			t.Fatalf("observe signal should not be grouped: %+v", g)
		}
	}
	if r.Strategies[0].TotalRet != 0 {
		t.Fatalf("observe signal should not open a position: %+v", r.Strategies[0])
	}
}
