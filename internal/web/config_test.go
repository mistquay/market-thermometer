package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"market-thermometer/internal/config"
	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
	"market-thermometer/internal/store"
)

func TestZeroConfigSetupAndHotReload(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "zero.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub, err := ds.NewHub(ds.Env{Now: time.Now}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := job.NewRunner(st, hub, market.CN, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	shadow := job.NewPMShadow(st, 0.03).WithDF(0)
	def := config.Default()
	def.Plugins = []config.Plugin{{Dir: "/safe/plugin", Command: []string{"python", "main.py"}}}
	if err := st.SaveAppConfig(t.Context(), def, "default", time.Now()); err != nil {
		t.Fatal(err)
	}

	srv := New(Options{PMShadow: shadow, Config: def, ConfigSource: "default"}, st, hub, runner)
	h := srv.Handler()

	// 1. 首次启动：initialized=false
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
	}
	var stResp struct {
		Initialized   bool `json:"initialized"`
		Authenticated bool `json:"authenticated"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &stResp)
	if stResp.Initialized || stResp.Authenticated {
		t.Fatalf("expected uninitialized, got %+v", stResp)
	}

	// 2. 通过 /api/auth/setup 初始化管理员口令并自动获得登录 Cookie
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewBufferString(`{"password":"zeroconfig123"}`))
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup=%d %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie after setup")
	}
	cookie := cookies[0]

	// 二次 setup 必须被拒绝
	req2 := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewBufferString(`{"password":"anotherpass1"}`))
	req2.Header.Set("Origin", "http://example.com")
	req2.Host = "example.com"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 on repeat setup, got %d", rec2.Code)
	}

	// 3. 修改配置（含热更新 minEdge/df，且尝试篡改 plugins 必须被忽略）
	next := def.Clone()
	next.Polymarket.Strategy.DF = 6
	next.Polymarket.Strategy.MinEdge = 0.05
	next.Plugins = []config.Plugin{{Dir: "/evil", Command: []string{"sh", "-c", "rm -rf /"}}}
	body, _ := json.Marshal(next)
	putReq := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(body))
	putReq.Header.Set("Origin", "http://example.com")
	putReq.Host = "example.com"
	putReq.AddCookie(cookie)
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put config=%d %s", putRec.Code, putRec.Body.String())
	}

	// 验证 PMShadow 已实时热更新
	ss := shadow.Status()
	if ss.DF != 6 || ss.MinEdge != 0.05 {
		t.Fatalf("expected hot-reloaded shadow df=6 minEdge=0.05, got df=%v minEdge=%v", ss.DF, ss.MinEdge)
	}

	// 验证外部插件未被 Web 篡改
	loaded, _, _, ok, err := st.LoadAppConfig(t.Context())
	if err != nil || !ok {
		t.Fatalf("load config failed: %v %v", ok, err)
	}
	if len(loaded.Plugins) != 1 || loaded.Plugins[0].Dir != "/safe/plugin" {
		t.Fatalf("plugins should remain immutable from Web, got %+v", loaded.Plugins)
	}
}
