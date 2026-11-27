package portfolio

import (
	"math"
	"testing"
)

func TestRebalanceAndDCA(t *testing.T) {
	cal := []string{
		"2025-01-02", "2025-01-15",
		"2025-02-03", "2025-02-20",
		"2025-03-03", "2025-03-25",
		"2025-04-01",
	}
	a1 := map[string]float64{
		"2025-01-02": 100, "2025-01-15": 120,
		"2025-02-03": 140, "2025-02-20": 130,
		"2025-03-03": 150, "2025-03-25": 160,
		"2025-04-01": 160,
	}
	a2 := map[string]float64{
		"2025-01-02": 100, "2025-01-15": 100,
		"2025-02-03": 100, "2025-02-20": 100,
		"2025-03-03": 100, "2025-03-25": 100,
		"2025-04-01": 100,
	}
	in := Input{
		Calendar:  cal,
		Bench:     a2,
		Rebalance: "monthly",
		DCA:       1000,
		Assets: []Asset{
			{Key: "stock:1", Label: "A", Weight: 0.5, Prices: a1},
			{Key: "stock:2", Label: "B", Weight: 0.5, Prices: a2},
		},
	}
	out := Analyze(in)
	if out.Rebalances != 3 {
		t.Fatalf("monthly rebalances=%d want 3", out.Rebalances)
	}
	if out.DCA == nil || out.DCA.Months != 4 || out.DCA.Invested != 4000 || out.DCA.FinalValue <= 4000 {
		t.Fatalf("dca %+v", out.DCA)
	}
	// 阈值再平衡（偏离 >= 5%）：a1 涨到 140 时权重 = 0.7 / 1.2 = 58.3% 触发再平衡
	in.Rebalance = "threshold"
	outT := Analyze(in)
	if outT.Rebalances < 1 {
		t.Fatalf("threshold rebalances=%d want >=1", outT.Rebalances)
	}
	if math.IsNaN(outT.Stats.TotalRet) {
		t.Fatal("nan totalRet")
	}
}
