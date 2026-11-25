package job

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/market"
	"market-thermometer/internal/store"
)

// fakeStocks：两只在市股票 + 一只已退市股票，K 线随 upTo 增长，模拟每天有新数据。
type fakeStocks struct {
	dates []string
	upTo  int // 只返回前 upTo 个交易日
	mu    sync.Mutex
	calls map[string]int
	down  bool
}

func (f *fakeStocks) Name() string { return "fake" }
func (f *fakeStocks) ListStocks(context.Context) ([]ds.Stock, error) {
	if f.down {
		return nil, errors.New("source down")
	}
	return []ds.Stock{{Code: "600000"}, {Code: "000001"}, {Code: "600087", Delisted: true}}, nil
}
func (f *fakeStocks) Bars(_ context.Context, code, from, _ string) ([]ds.StockBar, error) {
	f.mu.Lock()
	f.calls[code]++
	f.mu.Unlock()
	end := f.upTo
	if code == "600087" {
		end = min(end, 260) // 第 260 天退市
	}
	var out []ds.StockBar
	for i := 0; i < end; i++ {
		if f.dates[i] < from {
			continue
		}
		c := 10 + float64(i%50)/10 // 锯齿
		if code == "000001" {
			c = 100 - float64(i)/10 // 单边下跌：始终在均线下方
		}
		out = append(out, ds.StockBar{Date: f.dates[i], Close: c, Mult: 1})
	}
	return out, nil
}

func tradingDays(n int) []string {
	var out []string
	for d := time.Date(2009, 1, 5, 0, 0, 0, 0, time.UTC); len(out) < n; d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			out = append(out, d.Format("2006-01-02"))
		}
	}
	return out
}

func TestBreadthSyncIncrementalAndOffline(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub, _ := ds.NewHub(ds.Env{}, []string{}, nil, nil)
	r := NewRunner(st, hub, market.CN, time.Date(2005, 1, 1, 0, 0, 0, 0, time.UTC))
	src := &fakeStocks{dates: tradingDays(400), upTo: 350, calls: map[string]int{}}
	r.SetStocks(src, 2)

	if err := r.syncStocks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.computeBreadth(ctx, false); err != nil {
		t.Fatal(err)
	}
	full, err := st.Series(ctx, ds.CNBreadthMA200, breadthSource, "", "")
	if err != nil || len(full) == 0 {
		t.Fatalf("no breadth: %v %v", full, err)
	}

	// 增量：再多 50 天；已退市股票不再请求
	src.upTo = 400
	if err := r.syncStocks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.computeBreadth(ctx, false); err != nil {
		t.Fatal(err)
	}
	if src.calls["600087"] != 1 {
		t.Fatalf("delisted stock with local data should not be refetched, calls=%d", src.calls["600087"])
	}
	inc, _ := st.Series(ctx, ds.CNBreadthMA200, breadthSource, "", "")

	// 数据源下线：只用本地日线全量重算，结果与增量一致
	src.down = true
	if err := r.computeBreadth(ctx, true); err != nil {
		t.Fatal(err)
	}
	offline, _ := st.Series(ctx, ds.CNBreadthMA200, breadthSource, "", "")
	if fmt.Sprint(inc) != fmt.Sprint(offline) || len(offline) <= len(full) {
		t.Fatalf("incremental (%d pts) and offline full recompute (%d pts) differ", len(inc), len(offline))
	}
	// 600087 退市后只剩两只股票计入：000001 始终在均线下，因此占比只可能是 0 或 50
	for _, p := range offline {
		if p.Date > tradingDays(400)[262] && p.Value != 0 && p.Value != 50 {
			t.Fatalf("%s breadth %v", p.Date, p.Value)
		}
	}
	stats, _ := st.StockStats(ctx)
	if stats.Stocks != 3 || stats.WithBars != 3 || stats.Delisted != 1 || stats.Last != tradingDays(400)[399] {
		t.Fatalf("stats %+v", stats)
	}
}
