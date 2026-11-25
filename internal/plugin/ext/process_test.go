package ext

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/notify"
)

// TestHelperProcess 不是真正的测试：被测代码把测试二进制当作插件进程启动（GO_EXT_HELPER=1）。
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_EXT_HELPER") != "1" {
		t.Skip("helper process")
	}
	fmt.Println("plugin starting (stray stdout line)") // 非协议输出：宿主应忽略
	in := bufio.NewScanner(os.Stdin)
	cfg := map[string]string{}
	for in.Scan() {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(in.Bytes(), &req)
		reply := func(result any, errMsg string) {
			resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
			if errMsg != "" {
				resp["error"] = map[string]any{"code": -32000, "message": errMsg}
			} else {
				resp["result"] = result
			}
			b, _ := json.Marshal(resp)
			fmt.Println(string(b))
		}
		switch req.Method {
		case "initialize":
			var p struct {
				Config map[string]string `json:"config"`
			}
			_ = json.Unmarshal(req.Params, &p)
			cfg = p.Config
			fmt.Fprintln(os.Stderr, "initialized with mode="+cfg["mode"])
			reply(map[string]any{}, "")
		case "metric.fetch":
			switch cfg["mode"] {
			case "slow":
				time.Sleep(5 * time.Second)
			case "crash":
				os.Exit(3)
			case "bad":
				reply(map[string]any{"points": []map[string]any{{"date": "2026/01/02", "value": 1}}}, "")
				continue
			case "fail":
				reply(nil, "upstream down")
				continue
			}
			reply(map[string]any{"points": []map[string]any{
				{"date": "2026-01-05", "value": 2}, {"date": "2026-01-02", "value": 1}, {"date": "2025-12-01", "value": 9},
			}}, "")
		case "notify.send":
			var p struct {
				Config map[string]string `json:"config"`
				Title  string            `json:"title"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Config["token"] == "" {
				reply(nil, "missing token")
				continue
			}
			reply(map[string]any{}, "")
		case "shutdown":
			reply(map[string]any{}, "")
			os.Exit(0)
		default:
			reply(nil, "method not found")
		}
	}
	os.Exit(0)
}

func helperSpec(t *testing.T, manifest string, timeout time.Duration) Spec {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return Spec{Dir: dir, Command: []string{os.Args[0], "-test.run=^TestHelperProcess$"}, Timeout: timeout,
		Env: []string{"GO_EXT_HELPER=1"}}
}

const metricManifest = `{"name":"demo_%s","kind":"metric_source","version":"0.1.0","metrics":[{"id":"ext.demo_%s","name":"演示"}]}`

func TestMetricPlugin(t *testing.T) {
	p, err := Install(helperSpec(t, fmt.Sprintf(metricManifest, "ok", "ok"), 10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if m, ok := ds.Lookup("ext.demo_ok"); !ok || m.Name != "演示" {
		t.Fatalf("metric not defined: %+v", m)
	}
	hub, err := ds.NewHub(ds.Env{}, []string{"demo_ok"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	src, pts, err := hub.Fetch(context.Background(), "ext.demo_ok", from, to)
	if err != nil || src != "demo_ok" || len(pts) != 2 || pts[0].Date != "2026-01-02" { // 区间外的点被丢弃，按日期排序
		t.Fatalf("fetch: %s %v %v", src, pts, err)
	}
	st := p.Status()
	if !st.Running || st.Starts != 1 || !strings.Contains(strings.Join(st.Stderr, "\n"), "stray stdout") {
		t.Fatalf("status: %+v", st)
	}
	// 业务错误不重启
	p.SetConfig(map[string]string{"mode": "fail"})
	if _, err := (&metricSource{p: p}).Fetch(context.Background(), "ext.demo_ok", from, to); err == nil || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("want rpc error, got %v", err)
	}
	if !p.Status().Running {
		t.Fatal("process stopped on business error")
	}
	// 坏数据被拒绝
	p.SetConfig(map[string]string{"mode": "bad"})
	if _, err := (&metricSource{p: p}).Fetch(context.Background(), "ext.demo_ok", from, to); err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestTimeoutCrashAndRestartLimit(t *testing.T) {
	p, err := Load(helperSpec(t, fmt.Sprintf(metricManifest, "slow", "slow"), 500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ms := &metricSource{p: p}
	from, to := time.Now().AddDate(0, -1, 0), time.Now()

	p.SetConfig(map[string]string{"mode": "slow"})
	if _, err := ms.Fetch(context.Background(), "x", from, to); err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("want timeout, got %v", err)
	}
	if st := p.Status(); st.Running || st.LastError == "" {
		t.Fatalf("after timeout: %+v", st)
	}
	p.SetConfig(map[string]string{"mode": "crash"})
	if _, err := ms.Fetch(context.Background(), "x", from, to); err == nil || !strings.Contains(err.Error(), "退出") {
		t.Fatalf("want crash, got %v", err)
	}
	// 恢复正常后自动重启
	p.SetConfig(map[string]string{})
	if _, err := ms.Fetch(context.Background(), "x", from, to); err != nil {
		t.Fatalf("restart: %v", err)
	}
	// 10 分钟内启动超过 5 次后暂停（改配置引起的重启不计数，所以从 0 开始）
	p.SetConfig(map[string]string{"mode": "crash"})
	var last error
	for range 6 {
		_, last = ms.Fetch(context.Background(), "x", from, to)
	}
	if last == nil || !strings.Contains(last.Error(), "暂停调用") {
		t.Fatalf("want restart limit, got %v", last)
	}
}

func TestNotifierPlugin(t *testing.T) {
	_, err := Install(helperSpec(t, `{"name":"demo_notify","kind":"notifier","version":"1","config":[{"key":"token","label":"Token","secret":true}]}`, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer CloseAll()
	k, ok := notify.Lookup("demo_notify")
	if !ok || !k.External || len(k.Fields) != 1 {
		t.Fatalf("kind: %+v", k)
	}
	n, err := notify.Build("demo_notify", map[string]string{}, notify.Env{})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), notify.Message{Title: "t"}); err == nil || !strings.Contains(err.Error(), "missing token") {
		t.Fatalf("want plugin error, got %v", err)
	}
	n, _ = notify.Build("demo_notify", map[string]string{"token": "abc"}, notify.Env{})
	if err := n.Send(context.Background(), notify.Message{Title: "t"}); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsBadManifest(t *testing.T) {
	for _, m := range []string{`{`, `{"name":"x","kind":"strategy_x","version":"1"}`, `{"name":"Bad","kind":"metric_source","version":"1"}`} {
		if _, err := Load(helperSpec(t, m, time.Second)); err == nil {
			t.Errorf("accepted %s", m)
		}
	}
	if _, err := Install(helperSpec(t, `{"name":"nometrics","kind":"metric_source","version":"1"}`, time.Second)); err == nil {
		t.Error("metric plugin without metrics accepted")
	}
}
