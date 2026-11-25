package csindex

import "testing"

func TestParsePerf(t *testing.T) {
	body := `{"code":"200","msg":"Success","data":[
		{"tradeDate":"20000101","indexCode":"000300","close":1000,"peg":13.0,"tradingValue":1},
		{"tradeDate":"20110629","indexCode":"000300","close":3000,"peg":null,"tradingValue":null},
		{"tradeDate":"20261124","indexCode":"000300","close":3963.82,"peg":13.06,"tradingValue":5068.66}]}`
	rows, err := ParsePerf([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	// 20000101 是周六（非交易日），应丢弃
	if len(rows) != 2 || rows[1].Date != "2026-11-24" {
		t.Fatalf("rows %+v", rows)
	}
	pe := Points(rows, func(r Row) *float64 { return r.PE })
	if len(pe) != 1 || pe[0].Value != 13.06 { // 2011-06-29 PE 为空，跳过
		t.Fatalf("pe %+v", pe)
	}
	cl := Points(rows, func(r Row) *float64 { return r.Close })
	if len(cl) != 2 || cl[0].Date != "2011-06-29" {
		t.Fatalf("close %+v", cl)
	}
}

func TestParsePerfError(t *testing.T) {
	if _, err := ParsePerf([]byte(`{"code":"500","msg":"服务器异常","data":null}`)); err == nil {
		t.Fatal("want error")
	}
}
