package tencent

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestExRightsMult(t *testing.T) {
	cases := []struct {
		prev float64
		fh   string
		want float64
	}{
		{13.93, "10派4.1元", 13.93 / (13.93 - 0.41)}, // 600000 2025-07-16，stockdb 记为 1.03
		{20, "10送3转2派1元", 20 / ((20 - 0.1) / 1.5)}, // 送转 + 派现
		{10, "10转增4股", 10 / (10.0 / 1.4)},          // 只有转增
		{10, "不分配不转增", 1},                          // 无法解析
	}
	for _, c := range cases {
		if got := ExRightsMult(c.prev, c.fh); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%q: got %v want %v", c.fh, got, c.want)
		}
	}
	if m := ExRightsMult(13.93, "10派4.1元"); math.Abs(m-1.03) > 0.001 {
		t.Fatalf("should match stockdb mult 1.03, got %v", m)
	}
}

// fixture 结构取自 2026-11 实测响应（截取）。
const fixture = `{"code":0,"msg":"","data":{"sh600000":{"day":[
["2025-07-15","14.150","13.930","14.200","13.830","766732.000"],
["2025-07-16","13.540","13.480","13.600","13.260","814269.000",{"nd":"2024","fh_sh":"4.1","djr":"2025-07-15","cqr":"2025-07-16","FHcontent":"10派4.1元"}],
["2025-07-17","13.480","13.440","13.500","13.300","0.000"]]}}}`

func TestParseKlineAndBars(t *testing.T) {
	raw, err := parseKline([]byte(fixture), "sh600000")
	if err != nil {
		t.Fatal(err)
	}
	bars := toBars(raw)
	if len(bars) != 2 { // 成交量为 0 的一根丢弃
		t.Fatalf("bars %+v", bars)
	}
	if bars[0].Mult != 1 || math.Abs(bars[1].Mult-13.93/13.52) > 1e-9 || bars[1].Close != 13.48 {
		t.Fatalf("bars %+v", bars)
	}
}

// 被 WAF 拦截（501）后只请求一次，随后熔断、直接失败，不再打到服务端。
func TestBlockedCircuitBreaker(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
	s := &Source{http: client, now: time.Now}
	ctx := context.Background()
	if _, err := s.Bars(ctx, "600000", "2026-01-01", "2026-02-01"); err == nil {
		t.Fatal("want error")
	}
	if _, err := s.Bars(ctx, "600001", "2026-01-01", "2026-02-01"); err == nil {
		t.Fatal("want fast failure while blocked")
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 request, got %d", calls)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
