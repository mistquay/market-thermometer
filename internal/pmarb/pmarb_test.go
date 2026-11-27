package pmarb

import (
	"math"
	"testing"

	ds "market-thermometer/internal/datasource"
)

func TestScanBinaryAndNegRisk(t *testing.T) {
	ev := ds.PMEvent{Slug: "btc-test", Title: "BTC Test"}
	m := ds.PMMarket{ConditionID: "c1", Outcomes: []string{"Up", "Down"}, Tokens: []string{"t0", "t1"}, FeeRate: 0.01}
	// Up ask 0.45, Up bid 0.50 -> Down ask = 1 - 0.50 = 0.50; sum = 0.95 < 1.00
	book := ds.Book{
		Bids: []ds.BookLevel{{Price: 0.50, Size: 100}},
		Asks: []ds.BookLevel{{Price: 0.45, Size: 100}},
	}
	opp, ok := ScanBinary(ev, m, book, 50)
	if !ok || opp.Shares != 50 || opp.RawSumAsk != 0.95 || opp.NetProfit <= 0 {
		t.Fatalf("binary arb %+v %v", opp, ok)
	}

	// NegRisk 3 个互斥结果，卖一合计 0.92，无手续费 -> 每份净利 0.08
	nrEv := ds.PMEvent{
		Slug: "fed-rate", Title: "Fed Rate", NegRisk: true,
		Markets: []ds.PMMarket{
			{ConditionID: "m1", GroupTitle: "Cut", Tokens: []string{"a1"}},
			{ConditionID: "m2", GroupTitle: "Hold", Tokens: []string{"a2"}},
			{ConditionID: "m3", GroupTitle: "Hike", Tokens: []string{"a3"}},
		},
	}
	books := map[string]ds.Book{
		"a1": {Asks: []ds.BookLevel{{Price: 0.30, Size: 40}}},
		"a2": {Asks: []ds.BookLevel{{Price: 0.50, Size: 60}}},
		"a3": {Asks: []ds.BookLevel{{Price: 0.12, Size: 80}}},
	}
	nr, ok := ScanNegRisk(nrEv, books, 50)
	if !ok || nr.Shares != 40 || math.Abs(nr.NetPerUnit-0.08) > 1e-6 {
		t.Fatalf("negrisk arb %+v %v", nr, ok)
	}
	// 缺一条腿的盘口不能误报
	delete(books, "a3")
	if _, ok := ScanNegRisk(nrEv, books, 50); ok {
		t.Fatal("should reject incomplete negrisk books")
	}
}
