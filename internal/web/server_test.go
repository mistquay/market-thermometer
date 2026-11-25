package web

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
	"market-thermometer/internal/store"
)

func newTestServer(t *testing.T) http.Handler {
	h, _ := newTestServerStore(t)
	return h
}

func newTestServerStore(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub, err := ds.NewHub(ds.Env{}, []string{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct-horse"), bcrypt.MinCost)
	return New(Options{PasswordHash: string(hash)}, st, hub, job.NewRunner(st, hub, market.CN, time.Now())).Handler(), st
}

func do(h http.Handler, method, path, body string, cookies []*http.Cookie, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://mt.local"+path, strings.NewReader(body))
	r.RemoteAddr = "10.0.0.1:1234"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuthFlow(t *testing.T) {
	h := newTestServer(t)
	if w := do(h, "GET", "/api/metrics", "", nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	w := do(h, "POST", "/api/auth/login", `{"password":"correct-horse"}`, nil, "http://mt.local")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie %+v", cookies)
	}
	if w := do(h, "GET", "/api/metrics", "", cookies, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "cn.hs300.close") || strings.Contains(w.Body.String(), `"route":null`) {
		t.Fatalf("metrics: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/auth/logout", "", cookies, "http://evil.example"); w.Code != 403 {
		t.Fatalf("cross-origin write must be rejected: %d", w.Code)
	}
	if w := do(h, "POST", "/api/auth/logout", "", cookies, "http://mt.local"); w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := do(h, "GET", "/api/metrics", "", cookies, ""); w.Code != 401 {
		t.Fatalf("after logout: %d", w.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	h := newTestServer(t)
	for i := range loginMaxFails {
		if w := do(h, "POST", "/api/auth/login", `{"password":"wrong"}`, nil, ""); w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	// 锁定后即使口令正确也拒绝
	if w := do(h, "POST", "/api/auth/login", `{"password":"correct-horse"}`, nil, ""); w.Code != 429 {
		t.Fatalf("want 429, got %d", w.Code)
	}
}

func TestOverviewEncodesNaNAsNull(t *testing.T) {
	h, st := newTestServerStore(t)
	ctx := context.Background()
	p, _ := st.ActiveParams(ctx, time.Now())
	nan := math.NaN()
	_ = st.ReplaceScores(ctx, "CN", p.Version(), []engine.Day{
		{Date: "2026-11-23", Close: 1, ERP: nan, V: nan, T: [4]float64{nan, nan, nan, nan}, TBase: nan, TPct: nan, TS: nan,
			Heat: nan, Leverage: nan, RSID: nan, RSIW: nan, RMix: nan},
		{Date: "2026-11-24", Close: 2, ERP: 0.06, V: 14.5, Tier: "cheap", T: [4]float64{40, 13, nan, 4}, TBase: 19, TPct: 5, TS: 5.2,
			Heat: 1.8, Leverage: 7, RSID: 30, RSIW: 35, RMix: 33},
	})
	login := do(h, "POST", "/api/auth/login", `{"password":"correct-horse"}`, nil, "")
	ck := login.Result().Cookies()
	for _, path := range []string{"/api/overview", "/api/scores"} {
		w := do(h, "GET", path, "", ck, "")
		var v map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	w := do(h, "GET", "/api/overview", "", ck, "")
	if !strings.Contains(w.Body.String(), `"tS":5.2`) || !strings.Contains(w.Body.String(), `"t":[40,13,null,4]`) {
		t.Fatalf("overview body %s", w.Body)
	}
}

func TestBacktestAndParamsAPI(t *testing.T) {
	h, st := newTestServerStore(t)
	ctx := context.Background()
	// 只有收盘价：其余维度缺失，引擎应照常返回（T 为空、无信号），接口不报错
	var pts []ds.Point
	d := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	for len(pts) < 400 {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			pts = append(pts, ds.Point{Date: d.Format("2006-01-02"), Value: 3000 + float64(len(pts)%50)})
		}
		d = d.AddDate(0, 0, 1)
	}
	if _, err := st.UpsertPoints(ctx, ds.CNHS300Close, "csindex", pts, time.Now()); err != nil {
		t.Fatal(err)
	}
	ck := do(h, "POST", "/api/auth/login", `{"password":"correct-horse"}`, nil, "").Result().Cookies()

	w := do(h, "POST", "/api/backtest", `{"config":{"from":"2019-06-01","split":"2020-01-01"}}`, ck, "http://mt.local")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"strategies"`) {
		t.Fatalf("backtest: %d %s", w.Code, w.Body.String()[:min(300, w.Body.Len())])
	}
	if w := do(h, "POST", "/api/backtest", `{"params":{"timingWindow":0}}`, ck, ""); w.Code != 400 {
		t.Fatalf("invalid params should be rejected: %d", w.Code)
	}
	if w := do(h, "POST", "/api/params", `{"name":"","params":{}}`, ck, ""); w.Code != 400 {
		t.Fatalf("empty name should be rejected: %d", w.Code)
	}
	if w := do(h, "POST", "/api/params", `{"name":"试验一","params":{"emaSpan":5},"activate":true}`, ck, ""); w.Code != 200 {
		t.Fatalf("save params: %d %s", w.Code, w.Body)
	}
	w = do(h, "GET", "/api/params", "", ck, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "试验一") {
		t.Fatalf("list params: %d %s", w.Code, w.Body)
	}
	p, _ := st.ActiveParams(ctx, time.Now())
	if p.EMASpan != 5 || p.TimingWindow != 750 { // 未提交的字段保留默认值
		t.Fatalf("active params not merged with defaults: %+v", p)
	}
	if w := do(h, "GET", "/api/signals", "", ck, ""); w.Code != 200 {
		t.Fatalf("signals: %d", w.Code)
	}
}
