package eastmoney

import "testing"

func TestParseFundList(t *testing.T) {
	body := `var r = [["000001","HXCZHH","华夏成长混合","混合型-灵活","HUAXIA"],["110022","YFDXFHY","易方达消费行业股票","股票型","YI"],["bad"]];`
	got, err := ParseFundList([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Code != "110022" || got[1].Name != "易方达消费行业股票" || got[1].Type != "股票型" {
		t.Fatalf("got %+v", got)
	}
}

// 1282233600000 = 2010-08-20 00:00 北京时间；1795449600000 = 2026-11-24 00:00 北京时间。
func TestParseFundNAV(t *testing.T) {
	body := `var fS_code = "110022";
var Data_netWorthTrend = [{"x":1282233600000,"y":1.0,"equityReturn":0,"unitMoney":""},{"x":1795449600000,"y":2.985,"equityReturn":-0.33,"unitMoney":""}];
var Data_ACWorthTrend = [[1282233600000,1.0],[1795449600000,3.105]];
var Data_grandTotal = [];`
	navs, err := ParseFundNAV([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(navs) != 2 || navs[0].Date != "2010-08-20" || navs[1].Date != "2026-11-24" || navs[1].NAV != 2.985 || navs[1].AccNAV != 3.105 {
		t.Fatalf("navs %+v", navs)
	}
	if _, err := ParseFundNAV([]byte(`var x = 1;`)); err == nil {
		t.Fatal("want error on missing data")
	}
}
