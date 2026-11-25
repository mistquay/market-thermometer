package ext

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPythonExample 用真实的 Python 运行 examples/plugins/csv_source，验证 SDK 与协议（本机没有 Python 时跳过）。
func TestPythonExample(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		if py, err = exec.LookPath("python3"); err != nil {
			t.Skip("python not found")
		}
	}
	dir, _ := filepath.Abs("../../../examples/plugins/csv_source")
	p, err := Load(Spec{Dir: dir, Command: []string{py, "-I", "main.py"}, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ms := &metricSource{p: p}
	from, to := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	pts, err := ms.Fetch(context.Background(), "user.csv_demo", from, to)
	if err != nil || len(pts) != 2 || pts[0].Date != "2026-11-23" || pts[1].Value != 1.55 {
		t.Fatalf("fetch: %v %v\nstderr: %v", pts, err, p.Status().Stderr)
	}
	// 配置错误：initialize 失败，错误信息带中文（验证 UTF-8）
	p.SetConfig(map[string]string{"path": "nope.csv"})
	if _, err := ms.Fetch(context.Background(), "user.csv_demo", from, to); err == nil || !strings.Contains(err.Error(), "CSV 文件不存在") {
		t.Fatalf("want init error, got %v", err)
	}
	// 未知方法
	p.SetConfig(nil)
	if err := p.Call(context.Background(), "stocks.bars", map[string]string{}, nil); err == nil || !strings.Contains(err.Error(), "不支持的方法") {
		t.Fatalf("want method error, got %v", err)
	}
}
