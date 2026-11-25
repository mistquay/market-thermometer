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
	"market-thermometer/internal/derived/ma200"
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
	bump  float64 // 模拟数据源事后校对：最近一天收盘价加上该值
}

func (f *fakeStocks) Name() string { return "fake" }
func (f *fakeStocks) ListStocks(context.Context) ([]ds.Stock, error) {
	if f.down {
		return nil, errors.New("source down")
	}
	return []ds.Stock{{Code: "600000", Kind: ds.KindStock}, {Code: "000001", Kind: ds.KindStock}, {Code: "600087", Kind: ds.KindStock, Delisted: true},
		{Code: "510300", Kind: ds.KindFund}}, nil
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
		if i == end-1 {
			c += f.bump
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
	r.SetStocks([]ds.StockSource{src}, 2)

	if err := r.syncStocks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.computeDerived(ctx, false); err != nil {
		t.Fatal(err)
	}
	full, err := st.Series(ctx, ds.CNBreadthMA200, ma200.Source, "", "")
	if err != nil || len(full) == 0 {
		t.Fatalf("no breadth: %v %v", full, err)
	}

	// 增量：再多 50 天；已退市股票不再请求
	src.upTo = 400
	if err := r.syncStocks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.computeDerived(ctx, false); err != nil {
		t.Fatal(err)
	}
	if src.calls["600087"] != 1 {
		t.Fatalf("delisted stock with local data should not be refetched, calls=%d", src.calls["600087"])
	}
	inc, _ := st.Series(ctx, ds.CNBreadthMA200, ma200.Source, "", "")

	// 数据源下线：只用本地日线全量重算，结果与增量一致
	src.down = true
	if err := r.computeDerived(ctx, true); err != nil {
		t.Fatal(err)
	}
	offline, _ := st.Series(ctx, ds.CNBreadthMA200, ma200.Source, "", "")
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
	if stats.Stocks != 3 || stats.WithBars != 3 || stats.Delisted != 1 || stats.Funds != 1 || stats.Last != tradingDays(400)[399] {
		t.Fatalf("stats %+v", stats)
	}
}

// 数据源事后修正近期数据：再次同步应覆盖本地旧值。
func TestSyncRevisitsRecentDays(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub, _ := ds.NewHub(ds.Env{}, []string{}, nil, nil)
	r := NewRunner(st, hub, market.CN, time.Date(2005, 1, 1, 0, 0, 0, 0, time.UTC))
	days := tradingDays(300)
	src := &fakeStocks{dates: days, upTo: 300, calls: map[string]int{}, bump: 0.5}
	r.SetStocks([]ds.StockSource{src}, 1)
	if err := r.syncStocks(ctx); err != nil {
		t.Fatal(err)
	}
	src.bump = 0 // 校对后的数据
	if err := r.syncStocks(ctx); err != nil {
		t.Fatal(err)
	}
	bars, _ := st.StockBars(ctx, "600000", days[298])
	if len(bars) != 1 || bars[0].Close != 10+float64(299%50)/10 {
		t.Fatalf("recent bar not revised: %+v", bars)
	}
}
