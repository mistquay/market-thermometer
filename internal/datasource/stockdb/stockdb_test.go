package stockdb

import (
	"encoding/json"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
)

// fixture 结构取自 2026-11 实测响应（字段截取）。
func TestParseBars(t *testing.T) {
	var rows []json.RawMessage
	_ = json.Unmarshal([]byte(`[
		{"date":20250715,"code":"600000","close":13.93,"volume":100,"pre_close":13.69},
		null,
		{"date":20250716,"code":"600000","close":13.48,"volume":200},
		{"date":20250717,"code":"600000","close":13.44,"volume":0}]`), &rows)
	var adj [][2]json.RawMessage
	_ = json.Unmarshal([]byte(`[["复权:600000:20250716",{"div":0.41,"mult":1.03,"cum":12.751}]]`), &adj)
	bars, err := parseBars(rows, adj)
	if err != nil {
		t.Fatal(err)
	}
	want := []ds.StockBar{{Date: "2025-07-15", Close: 13.93, Mult: 1}, {Date: "2025-07-16", Close: 13.48, Mult: 1.03}}
	if len(bars) != 2 || bars[0] != want[0] || bars[1] != want[1] { // 成交量为 0 的一根被丢弃
		t.Fatalf("bars %+v", bars)
	}
}

func TestMergeStocksKeepsDelistedAShares(t *testing.T) {
	groups := map[string][]string{"6": {"600000", "600519"}, "5": {"510300"}, "0": {"000001"}, "9": {"920001"}}
	var delisted []json.RawMessage
	_ = json.Unmarshal([]byte(`["600087","000979","159001","600000",123,"831000"]`), &delisted)
	got := mergeStocks(groups, delisted)
	want := []ds.Stock{{Code: "000001", Kind: "stock"}, {Code: "000979", Kind: "stock", Delisted: true}, {Code: "159001", Kind: "fund", Delisted: true},
		{Code: "510300", Kind: "fund"}, {Code: "600000", Kind: "stock"}, {Code: "600087", Kind: "stock", Delisted: true}, {Code: "600519", Kind: "stock"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v", got)
		}
	}
}

func TestClipFinal(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-25T10:00:00+08:00")
	b := clipFinal([]ds.StockBar{{Date: "2026-11-24"}, {Date: "2026-11-25"}}, now)
	if len(b) != 1 {
		t.Fatalf("intraday bar should be dropped: %v", b)
	}
}

func TestLatestName(t *testing.T) {
	rows := []json.RawMessage{
		json.RawMessage(`{"date":20261120,"name":"旧名"}`),
		json.RawMessage(`{"date":20261124,"name":"浦发银行"}`),
		json.RawMessage(`null`),
		json.RawMessage(`{"date":20261123,"name":"中间"}`),
	}
	if got := latestName(rows); got != "浦发银行" {
		t.Fatalf("latestName = %q", got)
	}
}
