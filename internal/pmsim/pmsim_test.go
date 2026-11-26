package pmsim

import (
	"math"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
)

var up = ds.Book{
	Bids: []ds.BookLevel{{Price: 0.37, Size: 39}, {Price: 0.36, Size: 100}},
	Asks: []ds.BookLevel{{Price: 0.38, Size: 50}, {Price: 0.40, Size: 100}},
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestOutcomeBookMirror(t *testing.T) {
	dn := OutcomeBook(up, 1)
	// Down 的卖价 = 1 − Up 的买价；Down 的买价 = 1 − Up 的卖价
	if !near(dn.Asks[0].Price, 0.63) || dn.Asks[0].Size != 39 || !near(dn.Asks[1].Price, 0.64) {
		t.Fatalf("down asks %+v", dn.Asks)
	}
	if !near(dn.Bids[0].Price, 0.62) || dn.Bids[0].Size != 50 || !near(dn.Bids[1].Price, 0.60) {
		t.Fatalf("down bids %+v", dn.Bids)
	}
}

func TestMatch(t *testing.T) {
	fee := Fee{Rate: 0.05, Exp: 1}
	// 按股数买 80 股：50 @0.38 + 30 @0.40
	f := Match(up, Buy, 80, 0, 0, fee)
	wantFee := 50*0.05*0.38*0.62 + 30*0.05*0.40*0.60
	if f.Shares != 80 || !near(f.Gross, 50*0.38+30*0.40) || !near(f.Fee, wantFee) || len(f.Levels) != 2 {
		t.Fatalf("buy shares %+v want fee %v", f, wantFee)
	}
	// 限价 0.39：只吃第一档（IOC，剩余取消）
	if f := Match(up, Buy, 80, 0, 0.39, fee); f.Shares != 50 {
		t.Fatalf("limit %+v", f)
	}
	// 按金额买 10 美元：10 / 0.38 = 26.31 股（向下取整到 0.01）
	if f := Match(up, Buy, 0, 10, 0, fee); f.Shares != 26.31 || f.Gross > 10 {
		t.Fatalf("amount %+v", f)
	}
	// 深度不够：只成交盘口上的 150 股
	if f := Match(up, Buy, 1000, 0, 0, Fee{}); f.Shares != 150 || f.Fee != 0 {
		t.Fatalf("depth %+v", f)
	}
	// 卖 50 股吃买盘：39 @0.37 + 11 @0.36；限价 0.37 时只成交 39
	if f := Match(up, Sell, 50, 0, 0, Fee{}); f.Shares != 50 || !near(f.Gross, 39*0.37+11*0.36) {
		t.Fatalf("sell %+v", f)
	}
	if f := Match(up, Sell, 50, 0, 0.37, Fee{}); f.Shares != 39 {
		t.Fatalf("sell limit %+v", f)
	}
	// 买 Down = 吃镜像后的卖盘
	if f := Match(OutcomeBook(up, 1), Buy, 10, 0, 0, Fee{}); !near(f.Avg(), 0.63) {
		t.Fatalf("buy down %+v", f)
	}
}

func TestReplaySettle(t *testing.T) {
	t0 := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	o := func(id int64, tok string, outcome int, side Side, shares, gross, fee float64) Order {
		return Order{ID: id, Cond: "c1", Token: tok, Outcome: outcome, Side: side, Status: StatusFilled, Fill: Fill{Shares: shares, Gross: gross, Fee: fee}}
	}
	orders := []Order{
		o(1, "up", 0, Buy, 100, 40, 1.2),             // 成本 41.2
		o(2, "up", 0, Sell, 50, 30, 0.75),            // 卖一半：收入 29.25，成本 20.6，实现 8.65
		o(3, "dn", 1, Buy, 10, 6, 0.12),              // 成本 6.12
		{ID: 4, Token: "up", Status: StatusRejected}, // 拒单不计
	}
	mk := map[string]Market{"c1": {Cond: "c1", End: t0, Winner: -1}}
	st := Replay(100, orders, mk)
	if !near(st.Cash, 100-41.2+29.25-6.12) || !near(st.Realized, 8.65) || len(st.Positions) != 2 || st.Orders != 3 {
		t.Fatalf("open state %+v", st)
	}
	// 出结果：Up 赢，剩余 50 股兑付 50；Down 10 股归零
	mk["c1"] = Market{Cond: "c1", End: t0, Winner: 0}
	st = Replay(100, orders, mk)
	wantRealized := 8.65 + (50 - 20.6) + (0 - 6.12)
	if !near(st.Cash, 100-41.2+29.25-6.12+50) || !near(st.Realized, wantRealized) || !st.Positions[0].Settled {
		t.Fatalf("settled state %+v want realized %v", st, wantRealized)
	}
	// 资金守恒：期末现金 = 初始 + 已实现盈亏（全部结算后没有持仓成本）
	if !near(st.Cash, 100+st.Realized) {
		t.Fatalf("cash %v != 100 + realized %v", st.Cash, st.Realized)
	}
}

func TestCheck(t *testing.T) {
	p := DefaultParams()
	ok := CheckInput{Side: Buy, Amount: 20, Cash: 1000}
	if err := Check(p, ok); err != nil {
		t.Fatal(err)
	}
	cases := []CheckInput{
		{Side: Buy, Amount: 60, Cash: 1000},                   // 单笔
		{Side: Buy, Amount: 20, DailyBought: 490, Cash: 1000}, // 单日
		{Side: Buy, Amount: 20, MarketCost: 90, Cash: 1000},   // 单市场
		{Side: Buy, Amount: 20, Cash: 10},                     // 现金
		{Side: Sell, Shares: 10, Held: 5},                     // 卖空
		{Side: Buy, Amount: 1, Cash: 1000, GlobalHalt: true},  // 全局熔断
	}
	for i, c := range cases {
		if Check(p, c) == nil {
			t.Errorf("case %d should be rejected: %+v", i, c)
		}
	}
	p.Halted = true
	if Check(p, ok) == nil {
		t.Error("account halt")
	}
	if (Params{MaxOrderUSD: 100, MaxDailyUSD: 50, MaxMarketUSD: 10}).Validate() == nil {
		t.Error("order > daily should be invalid")
	}
}
