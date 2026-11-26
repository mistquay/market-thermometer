package job

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/store"
)

// fakePM：一个 15 分钟系列，结束时间每 15 分钟一个，now 之前的已结算。
type fakePM struct {
	now       time.Time
	history   int
	pages     int
	failAfter int          // >0 时已结算事件翻到该偏移量就报错（模拟中途失败）
	late      map[int]bool // 已过结束时间、但数据源还没标记关闭的事件
}

func (f *fakePM) Name() string { return "fake" }
func (f *fakePM) event(i int) ds.PMEvent {
	end := f.now.Truncate(15 * time.Minute).Add(-time.Duration(i) * 15 * time.Minute)
	closed := (end.Before(f.now) || end.Equal(f.now)) && !f.late[i]
	m := ds.PMMarket{ConditionID: fmt.Sprintf("c%d", i), Tokens: []string{fmt.Sprintf("up%d", i), fmt.Sprintf("dn%d", i)},
		Outcomes: []string{"Up", "Down"}, Prices: []float64{0, 1}, Start: end.Add(-15 * time.Minute), End: end, Closed: closed, Winner: -1}
	if closed {
		m.Winner = 1
	}
	return ds.PMEvent{ID: fmt.Sprint(i), Slug: fmt.Sprintf("btc-updown-15m-%d", i), Series: "s", End: end, Closed: closed, Markets: []ds.PMMarket{m}}
}
func (f *fakePM) Events(_ context.Context, q ds.PMEventQuery) ([]ds.PMEvent, error) {
	f.pages++
	var out []ds.PMEvent
	if len(q.Slugs) > 0 {
		for _, s := range q.Slugs {
			var i int
			fmt.Sscanf(s, "btc-updown-15m-%d", &i)
			out = append(out, f.event(i))
		}
		return out, nil
	}
	if q.Closed != nil && !*q.Closed {
		return []ds.PMEvent{f.event(-1), f.event(-2)}, nil
	}
	for i := q.Offset; i < q.Offset+q.Limit && i < 1000; i++ {
		out = append(out, f.event(i))
	}
	return out, nil
}
func (f *fakePM) EventsAfter(ctx context.Context, q ds.PMEventQuery, cursor string) ([]ds.PMEvent, string, error) {
	off := 0
	fmt.Sscan(cursor, &off)
	if f.failAfter > 0 && off >= f.failAfter {
		return nil, "", fmt.Errorf("boom")
	}
	q.Offset = off
	evs, err := f.Events(ctx, q)
	next := ""
	if len(evs) == q.Limit {
		next = fmt.Sprint(off + q.Limit)
	}
	return evs, next, err
}
func (f *fakePM) Search(context.Context, string, int) ([]ds.PMEvent, error) { return nil, nil }
func (f *fakePM) PriceHistory(_ context.Context, token string, from, to int64, _ int) ([]ds.PricePoint, error) {
	f.history++
	return []ds.PricePoint{{T: from, P: 0.5}, {T: to, P: 0.01}}, nil
}
func (f *fakePM) Book(context.Context, string) (ds.Book, error) { return ds.Book{}, nil }

func TestPMSync(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 26, 6, 20, 0, 0, time.UTC)
	f := &fakePM{now: now}
	p := NewPMSyncer(st, f, []string{"s"}, 1, time.Minute) // 回溯 1 天：96 个已结算事件
	p.now = func() time.Time { return now }
	p.maxHistory = 50
	if err := p.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	closed := true
	evs, _ := st.PMEvents(ctx, store.PMEventQuery{Series: "s", Closed: &closed})
	if len(evs) != 96 { // 结束时间在 [now-1d, now]：06:30（前一天）~ 06:15
		t.Fatalf("closed events %d", len(evs))
	}
	if f.history != 50 {
		t.Fatalf("history budget: %d", f.history)
	}
	// 第二轮：没有新结算事件，只补剩余价格历史；只请求一页已结算
	f.history, f.pages = 0, 0
	if err := p.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if f.history != 46 || f.pages != 2 {
		t.Fatalf("second round: history %d pages %d", f.history, f.pages)
	}
	stats, err := st.PMSeriesStatsAll(ctx)
	if err != nil || len(stats) != 1 || stats[0].Closed != 96 || stats[0].WithHist != 96 || stats[0].Events != 98 {
		t.Fatalf("stats %+v %v", stats, err)
	}
	// 关注事件
	if err := st.WatchPM(ctx, "btc-updown-15m-3", now); err != nil {
		t.Fatal(err)
	}
	if err := p.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if pts, _ := st.PMPrices(ctx, "up3", 0, 0); len(pts) < 2 {
		t.Fatalf("watched prices %v", pts)
	}
}

// 结束后晚一轮才标记关闭的事件：第二轮要更新为已结算并有赢家。
func TestPMSyncLateClose(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 26, 6, 20, 0, 0, time.UTC)
	f := &fakePM{now: now, late: map[int]bool{1: true}}
	p := NewPMSyncer(st, f, []string{"s"}, 1, time.Minute)
	p.now = func() time.Time { return now }
	if err := p.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if e, _ := st.PMEventBySlug(ctx, "btc-updown-15m-1"); e.Closed {
		t.Fatal("round 1 should store it as open")
	}
	f.late = nil
	now = now.Add(5 * time.Minute) // 同一个 15 分钟内，没有新结算事件
	if err := p.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	e, err := st.PMEventBySlug(ctx, "btc-updown-15m-1")
	if err != nil || !e.Closed || e.Markets[0].Winner != 1 {
		t.Fatalf("round 2: %+v %v", e, err)
	}
}

// 中途失败不能留下空洞：第一轮只存下最新一页就失败，第二轮要补齐到回溯起点。
func TestPMSyncResumeAfterFailure(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 26, 6, 20, 0, 0, time.UTC)
	f := &fakePM{now: now, failAfter: 100}
	p := NewPMSyncer(st, f, []string{"s"}, 2, time.Minute) // 回溯 2 天：192 个已结算事件
	p.now = func() time.Time { return now }
	p.logf = func(string, ...any) {}
	if err := p.SyncOnce(ctx); err == nil {
		t.Fatal("want error")
	}
	f.failAfter = 0
	if err := p.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	closed := true
	if evs, _ := st.PMEvents(ctx, store.PMEventQuery{Series: "s", Closed: &closed}); len(evs) != 192 {
		t.Fatalf("closed events %d", len(evs))
	}
	since, through, _ := st.PMSeriesSync(ctx, "s")
	if through != now.Truncate(15*time.Minute).Unix() || since != now.Add(-48*time.Hour).Unix() {
		t.Fatalf("watermark %d %d", since, through)
	}
}
