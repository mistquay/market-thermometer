package eastmoney

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// fixture 取自 2026-11 实测响应的结构（数值截取）。
const klineFixture = `{"rc":0,"rt":17,"data":{"code":"000300","market":1,"name":"沪深300","klines":[
"2026-11-23,3950.00,3958.29,3965.10,3940.01,19000000,500000000000.00,0.20",
"2026-11-24,4004.04,3963.82,4011.69,3957.92,19738123,506866000000.00,0.21"]}}`

func TestParseKlines(t *testing.T) {
	bars, err := ParseKlines([]byte(klineFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 {
		t.Fatalf("got %d bars", len(bars))
	}
	b := bars[1]
	if b.Date != "2026-11-24" || b.Close != 3963.82 || b.Amount != 506866000000 || b.Turnover != 0.21 {
		t.Fatalf("bad bar %+v", b)
	}
}

func TestParseKlinesNullData(t *testing.T) {
	if _, err := ParseKlines([]byte(`{"rc":100,"data":null}`)); err == nil {
		t.Fatal("want error on null data")
	}
}

func TestSumAmountIntersectsDates(t *testing.T) {
	sh := []Bar{{Date: "2026-11-20", Amount: 1}, {Date: "2026-11-23", Amount: 2}}
	sz := []Bar{{Date: "2026-11-23", Amount: 10}, {Date: "2026-11-24", Amount: 20}}
	got := SumAmount(sh, sz)
	if len(got) != 1 || got[0].Date != "2026-11-23" || got[0].Value != 12 {
		t.Fatalf("got %+v", got)
	}
}

func TestParseDatacenter(t *testing.T) {
	body := `{"result":{"pages":3,"data":[
		{"SOLAR_DATE":"2026-11-23 00:00:00","EMM00166466":1.5861},
		{"SOLAR_DATE":"2026-11-24 00:00:00","EMM00166466":null}]},"success":true,"message":"ok","code":0}`
	pts, pages, err := ParseDatacenter([]byte(body), "SOLAR_DATE", "EMM00166466")
	if err != nil {
		t.Fatal(err)
	}
	if pages != 3 || len(pts) != 1 || pts[0].Date != "2026-11-23" || pts[0].Value != 1.5861 {
		t.Fatalf("pages=%d pts=%+v", pages, pts)
	}
}

func TestParseDatacenterEmptyAndError(t *testing.T) {
	pts, pages, err := ParseDatacenter([]byte(`{"result":null,"success":false,"message":"返回数据为空","code":9201}`), "D", "V")
	if err != nil || pts != nil || pages != 0 {
		t.Fatalf("empty: pts=%v pages=%d err=%v", pts, pages, err)
	}
	if _, _, err := ParseDatacenter([]byte(`{"result":null,"success":false,"message":"报表配置不存在","code":9501}`), "D", "V"); err == nil {
		t.Fatal("want error on code 9501")
	}
}

func TestKlineCooldownAfterFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		c.Close() // 模拟 push2his 被封时直接断开连接
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)
	s := &Source{http: rewriteClient(srv.URL), now: func() time.Time { return now }}
	ctx := context.Background()
	if _, err := s.klines(ctx, "1.000300", now, now); err == nil {
		t.Fatal("want error")
	}
	if _, err := s.klines(ctx, "1.000985", now, now); err == nil {
		t.Fatal("want fast failure during cooldown")
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 request (no retry, then cooldown), got %d", calls)
	}
	now = now.Add(klineCooldown + time.Second)
	_, _ = s.klines(ctx, "1.000300", now, now)
	if calls != 2 {
		t.Fatalf("want request after cooldown, got %d calls", calls)
	}
}

// rewriteClient 把所有请求转发到测试服务器。
func rewriteClient(target string) *http.Client {
	u, _ := url.Parse(target)
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
