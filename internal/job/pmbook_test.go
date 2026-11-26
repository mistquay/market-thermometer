package job

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"market-thermometer/internal/store"
)

func TestPMBookCollector(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 26, 6, 20, 0, 0, time.UTC)
	f := &fakePM{now: now, bookHash: "h1"}
	p := NewPMSyncer(st, f, []string{"btc-up-or-down-15m"}, 1, time.Minute)
	p.now = func() time.Time { return now }
	p.maxHistory = 0
	// fakePM 的事件系列固定为 "s"：直接用 "s" 当系列
	p.series = []string{"s"}
	if err := p.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	spotCalls := 0
	c := NewPMBookCollector(st, f, []string{"s"}, time.Second, 1, 14).WithSpot(func(context.Context, string) (float64, string, error) {
		spotCalls++
		return 60500, "fake", nil
	})
	c.now = func() time.Time { return now }
	if err := c.refreshTargets(ctx); err != nil {
		t.Fatal(err)
	}
	if st := c.Status(); len(st.Targets) != 2 || st.Targets[0].Slug != "btc-updown-15m--1" || st.Targets[0].Symbol != "SUSDT" {
		t.Fatalf("targets %+v", st.Targets)
	}
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(ctx); err != nil { // 摘要没变：不存盘口，现货价照存
		t.Fatal(err)
	}
	s := c.Status()
	if s.Books != 2 || s.Spots != 2 || s.Ticks != 2 || spotCalls != 2 {
		t.Fatalf("status %+v spot calls %d", s, spotCalls)
	}
	snaps, err := st.PMBooks(ctx, "up-1", 0, 0, 0)
	if err != nil || len(snaps) != 1 || len(snaps[0].Bids) != 1 || snaps[0].Bids[0].Price != 0.4 || snaps[0].T != now.UnixMilli() {
		t.Fatalf("snaps %+v %v", snaps, err) // levels=1：只存一档
	}
	f.bookHash = "h2"
	now = now.Add(time.Second)
	f.now = now
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if s := c.Status(); s.Books != 4 {
		t.Fatalf("after change %+v", s)
	}
	f.bookErr = errors.New("boom")
	if err := c.Tick(ctx); err == nil {
		t.Fatal("want error")
	}
	if SpotSymbol("eth-up-or-down-5m") != "ETHUSDT" {
		t.Fatal(SpotSymbol("eth-up-or-down-5m"))
	}
}
