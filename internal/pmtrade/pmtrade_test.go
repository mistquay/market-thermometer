package pmtrade

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/pmsim"
	"market-thermometer/internal/store"
)

type fakeSrc struct{ book ds.Book }

func (f *fakeSrc) Name() string { return "fake" }
func (f *fakeSrc) Events(context.Context, ds.PMEventQuery) ([]ds.PMEvent, error) {
	return nil, nil
}
func (f *fakeSrc) EventsAfter(context.Context, ds.PMEventQuery, string) ([]ds.PMEvent, string, error) {
	return nil, "", nil
}
func (f *fakeSrc) Search(context.Context, string, int) ([]ds.PMEvent, error) { return nil, nil }
func (f *fakeSrc) PriceHistory(context.Context, string, int64, int64, int) ([]ds.PricePoint, error) {
	return nil, nil
}
func (f *fakeSrc) Book(_ context.Context, tok string) (ds.Book, error) {
	b := f.book
	b.Token = tok
	return b, nil
}
func (f *fakeSrc) Books(_ context.Context, toks []string) ([]ds.Book, error) {
	var out []ds.Book
	for _, t := range toks {
		b := f.book
		b.Token = t
		out = append(out, b)
	}
	return out, nil
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestSimBroker(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 26, 8, 5, 0, 0, time.Local)
	mk := ds.PMMarket{ConditionID: "c1", Question: "BTC up?", Outcomes: []string{"Up", "Down"}, Tokens: []string{"up", "dn"},
		Prices: []float64{0.5, 0.5}, Start: now.Add(-5 * time.Minute), End: now.Add(10 * time.Minute), MinSize: 5, FeeRate: 0.05, FeeExp: 1, Winner: -1}
	ev := ds.PMEvent{ID: "e1", Slug: "btc-updown-15m-1", Title: "BTC", Series: "s", End: mk.End, Markets: []ds.PMMarket{mk}}
	if err := st.SavePMEvents(ctx, []ds.PMEvent{ev}, now); err != nil {
		t.Fatal(err)
	}
	src := &fakeSrc{book: ds.Book{Time: now,
		Bids: []ds.BookLevel{{Price: 0.49, Size: 100}},
		Asks: []ds.BookLevel{{Price: 0.50, Size: 20}, {Price: 0.52, Size: 100}}}}
	b := NewSim(st, nil) // 不实时刷新事件（src 为 nil）
	b.src = src
	b.BookOf = src.Book
	b.now = func() time.Time { return now }
	p := pmsim.DefaultParams()
	id, err := st.CreatePMSimAccount(ctx, store.PMSimAccount{Name: "t", Cash: 100, Params: p}, now)
	if err != nil {
		t.Fatal(err)
	}
	// 试算不入账
	o, err := b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 0, Side: pmsim.Buy, Amount: 20, DryRun: true})
	if err != nil || o.ID != 0 || o.Status != pmsim.StatusFilled {
		t.Fatalf("dry %+v %v", o, err)
	}
	// 买 Up 20 美元：20 股 @0.50 = 10，再 10/0.52 = 19.23 股
	o, err = b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 0, Side: pmsim.Buy, Amount: 20})
	if err != nil || o.ID == 0 || o.Status != pmsim.StatusFilled || o.Fill.Shares != 39.23 {
		t.Fatalf("buy %+v %v", o, err)
	}
	// 不在同步系列里的事件：成交后自动关注（让同步器刷新结果）
	if w, _ := st.PMWatched(ctx); len(w) != 1 || w[0] != ev.Slug {
		t.Fatalf("auto watch %v", w)
	}
	// 卖出超过持仓：拒单并记账
	o, err = b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 0, Side: pmsim.Sell, Shares: 50})
	if err != nil || o.Status != pmsim.StatusRejected || o.ID == 0 {
		t.Fatalf("oversell %+v %v", o, err)
	}
	// 买 Down 5 股：Down 卖价 = 1 − 0.49 = 0.51
	o, err = b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 1, Side: pmsim.Buy, Shares: 5})
	if err != nil || !near(o.Fill.Avg(), 0.51) {
		t.Fatalf("buy down %+v %v", o, err)
	}
	// 低于最小下单
	if o, _ := b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 1, Side: pmsim.Buy, Shares: 2}); o.Status != pmsim.StatusRejected || o.Reason != "可成交 2.00 股，低于最小下单 5 股" {
		t.Fatalf("min size %+v", o)
	}
	// 单市场上限 100：已有成本约 22.6，再买 80 超限
	if o, _ := b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 0, Side: pmsim.Buy, Amount: 49}); o.Status != pmsim.StatusFilled {
		t.Fatalf("within limit %+v", o)
	}
	if o, _ := b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 0, Side: pmsim.Buy, Amount: 40}); o.Status != pmsim.StatusRejected {
		t.Fatalf("market limit %+v", o)
	}
	// 全局熔断
	if err := SetGlobalHalt(ctx, st, true); err != nil {
		t.Fatal(err)
	}
	if o, _ := b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 1, Side: pmsim.Buy, Shares: 5}); o.Status != pmsim.StatusRejected {
		t.Fatalf("halt %+v", o)
	}
	_ = SetGlobalHalt(ctx, st, false)
	// 无效请求：返回错误、不记账
	if _, err := b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 5, Side: pmsim.Buy, Shares: 5}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid outcome %v", err)
	}

	v, err := b.Account(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.State.Positions) != 2 || v.State.Orders != 3 || len(v.Orders) != 7 {
		t.Fatalf("view %+v orders %d", v.State, len(v.Orders))
	}
	if math.Abs(v.Equity-(v.State.Cash+v.State.Positions[0].Shares*v.State.Positions[0].Mark+v.State.Positions[1].Shares*v.State.Positions[1].Mark)) > 0.006 {
		// 估值：Up 按买一 0.49，Down 按 1 − 0.50 = 0.50
		t.Fatalf("equity %+v", v)
	}
	cashBefore := v.State.Cash
	upShares := 0.0
	for _, p := range v.State.Positions {
		if p.Outcome == 0 {
			upShares = p.Shares
		}
	}
	// 结算：Up 赢
	mk.Closed, mk.Winner = true, 0
	ev.Closed, ev.Markets = true, []ds.PMMarket{mk}
	if err := st.SavePMEvents(ctx, []ds.PMEvent{ev}, now); err != nil {
		t.Fatal(err)
	}
	v, _ = b.Account(ctx, id)
	if !near(v.State.Cash, cashBefore+upShares) || !v.State.Positions[0].Settled || math.Abs(v.Equity-v.State.Cash) > 0.006 {
		t.Fatalf("settled %+v", v.State)
	}
	if !near(v.State.Cash, 100+v.State.Realized) {
		t.Fatalf("conservation cash %v realized %v", v.State.Cash, v.State.Realized)
	}
	// 已结束的市场不能再下单
	if o, _ := b.Place(ctx, OrderReq{Account: id, Slug: ev.Slug, Outcome: 0, Side: pmsim.Buy, Shares: 5}); o.Status != pmsim.StatusRejected {
		t.Fatalf("closed market %+v", o)
	}
}
