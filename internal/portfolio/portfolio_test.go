package portfolio

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAnalyzeBuyAndHold(t *testing.T) {
	cal := []string{"2025-12-30", "2025-12-31", "2026-01-05", "2026-01-06", "2026-01-07"}
	bench := map[string]float64{"2025-12-30": 100, "2025-12-31": 100, "2026-01-05": 110, "2026-01-06": 110, "2026-01-07": 121}
	a := Asset{Key: "stock:600000", Weight: 3, Prices: map[string]float64{ // 翻倍
		"2025-12-30": 10, "2025-12-31": 10, "2026-01-05": 15, "2026-01-07": 20}} // 01-06 停牌：沿用前值
	b := Asset{Key: "ofund:110022", Weight: 1, Prices: map[string]float64{
		"2025-12-31": 1, "2026-01-05": 1, "2026-01-06": 0.5, "2026-01-07": 0.5}} // 从 12-31 起才有数据
	c := Asset{Key: "fund:510300", Weight: 1} // 没有数据
	out := Analyze(Input{Calendar: cal, Bench: bench, Assets: []Asset{a, b, c}})

	if len(out.Missing) != 1 || out.Missing[0] != "fund:510300" {
		t.Fatalf("missing: %v", out.Missing)
	}
	if out.From != "2025-12-31" || out.To != "2026-01-07" || len(out.NAV) != 4 {
		t.Fatalf("range: %s ~ %s, %d", out.From, out.To, len(out.NAV))
	}
	// 权重 0.75 / 0.25：期末 0.75×2 + 0.25×0.5 = 1.625
	if !near(out.NAV[3], 1.625) || !near(out.Stats.TotalRet, 0.625) {
		t.Fatalf("nav: %v", out.NAV)
	}
	// 停牌日（01-06）该持仓收益为 0：NAV = 0.75×1.5 + 0.25×0.5
	if !near(out.NAV[2], 1.25) {
		t.Fatalf("suspension: %v", out.NAV)
	}
	// 贡献合计等于组合收益；期末权重之和为 1
	sumC, sumW := 0.0, 0.0
	for _, s := range out.Assets {
		sumC += s.Contribution
		sumW += s.EndWeight
	}
	if !near(sumC, out.Stats.TotalRet) || !near(sumW, 1) {
		t.Fatalf("contribution %v, end weights %v", sumC, sumW)
	}
	if !near(out.Bench.TotalRet, 0.21) {
		t.Fatalf("bench: %+v", out.Bench)
	}
	// 回撤：12-31 → 01-05 涨到 1.375，01-06 跌到 1.25
	if !near(out.Drawdown[2], 1.25/1.375-1) || !near(out.Stats.MaxDD, 1.25/1.375-1) {
		t.Fatalf("drawdown: %v", out.Drawdown)
	}
	// 分年度：2025 年只有 12-31 一天（收益 0，不完整），2026 年 = 1.625
	if len(out.Years) != 2 || !near(out.Years[1].Ret, 0.625) || out.Years[1].Year != "2026（不完整）" {
		t.Fatalf("years: %+v", out.Years)
	}
	if len(out.Corr) != 2 || out.Corr[0][0] == nil || !near(*out.Corr[0][0], 1) {
		t.Fatalf("corr: %v", out.Corr)
	}
}

func TestAnalyzeStartAndEmpty(t *testing.T) {
	cal := []string{"2026-01-05", "2026-01-06", "2026-01-07"}
	bench := map[string]float64{"2026-01-05": 1, "2026-01-06": 1, "2026-01-07": 1}
	a := Asset{Key: "x", Weight: 1, Prices: map[string]float64{"2026-01-05": 1, "2026-01-06": 2, "2026-01-07": 4}}
	if out := Analyze(Input{Calendar: cal, Bench: bench, Assets: []Asset{a}, Start: "2026-01-06"}); out.From != "2026-01-06" || !near(out.Stats.TotalRet, 1) {
		t.Fatalf("start: %+v", out)
	}
	if out := Analyze(Input{Calendar: cal, Bench: bench}); out.Note == "" {
		t.Fatal("want note for empty portfolio")
	}
}

func TestHFQ(t *testing.T) {
	// 除权日 10 派 1 元：前收 10 → 参考价 9.9，乘数 10/9.9；当日收 9.9 → 后复权价不变
	h := HFQ([]string{"a", "b", "c"}, []float64{10, 9.9, 10.89}, []float64{1, 10 / 9.9, 1})
	if !near(h["b"], 10) || !near(h["c"], 11) {
		t.Fatalf("hfq: %v", h)
	}
}
