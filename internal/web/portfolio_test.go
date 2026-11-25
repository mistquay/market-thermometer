package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPortfolioAPI(t *testing.T) {
	h, st := newTestServerStore(t)
	const origin = "http://mt.local"
	cookies := do(h, "POST", "/api/auth/login", `{"password":"correct-horse"}`, nil, origin).Result().Cookies()

	for _, bad := range []string{
		`{"name":"","items":[{"kind":"stock","code":"600000","weight":1}]}`,
		`{"name":"a","items":[]}`,
		`{"name":"a","items":[{"kind":"bond","code":"600000","weight":1}]}`,
		`{"name":"a","items":[{"kind":"stock","code":"600000","weight":0}]}`,
		`{"name":"a","start":"2026/1/1","items":[{"kind":"stock","code":"600000","weight":1}]}`,
	} {
		if w := do(h, "POST", "/api/portfolios", bad, cookies, origin); w.Code != 400 {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body)
		}
	}
	w := do(h, "POST", "/api/portfolios", `{"name":"稳健","items":[{"kind":"stock","code":"600000","weight":2},{"kind":"ofund","code":"110022","weight":1}]}`, cookies, origin)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var p struct {
		ID    int64 `json:"id"`
		Items []any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if len(p.Items) != 2 {
		t.Fatalf("items: %s", w.Body)
	}
	if watched, _ := st.WatchedFunds(context.Background()); len(watched) != 1 || watched[0].Code != "110022" {
		t.Fatalf("ofund not watched: %+v", watched)
	}
	id := itoa(p.ID)
	if w := do(h, "PUT", "/api/portfolios/"+id, `{"name":"稳健2","items":[{"kind":"stock","code":"600000","weight":1}]}`, cookies, origin); w.Code != 200 || !strings.Contains(w.Body.String(), "稳健2") {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	// 没有指标数据时分析报错而不是崩溃
	if w := do(h, "GET", "/api/portfolios/"+id+"/analysis", "", cookies, ""); w.Code != 500 || !strings.Contains(w.Body.String(), "先同步指标") {
		t.Fatalf("analysis: %d %s", w.Code, w.Body)
	}
	if w := do(h, "DELETE", "/api/portfolios/"+id, "", cookies, origin); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := do(h, "GET", "/api/portfolios", "", cookies, ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("list after delete: %s", w.Body)
	}
}
