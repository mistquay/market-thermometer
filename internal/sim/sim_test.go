package sim

import (
	"math"
	"strings"
	"testing"

	ds "market-thermometer/internal/datasource"
)

var cal = []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09", "2026-01-12", "2026-01-13", "2026-01-14"}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func flat(price float64, skip ...string) []ds.StockBar {
	var out []ds.StockBar
	for _, d := range cal {
		if !strings.Contains(strings.Join(skip, ","), d) {
			out = append(out, ds.StockBar{Date: d, Close: price, Mult: 1})
		}
	}
	return out
}

func noSlip() Params { p := DefaultParams(); p.SlippageBps = 0; return p }

func TestBuyFeesAndTPlus1(t *testing.T) {
	in := Input{Start: "2026-01-05", Cash: 100000, Params: DefaultParams(), Calendar: cal,
		Bars: map[string][]ds.StockBar{"600000": flat(10)},
		Orders: []Order{
			{ID: 1, Code: "600000", Kind: "stock", Side: Buy, Qty: 1000, PlacedDate: "2026-01-05", Status: StatusPending},
			{ID: 2, Code: "600000", Kind: "stock", Side: Sell, Qty: 1000, PlacedDate: "2026-01-05", Status: StatusPending}, // 同日成交：T+1 不可卖
			{ID: 3, Code: "600000", Kind: "stock", Side: Sell, Qty: 500, PlacedDate: "2026-01-06", Status: StatusPending},
		}}
	orders, st := Replay(in)
	b, s1, s2 := orders[0], orders[1], orders[2]
	// 买入：01-06 收盘 10 × (1+5bp) = 10.005，金额 10005，佣金 max(2.50, 5) = 5
	if b.Status != StatusFilled || b.FillDate != "2026-01-06" || !near(b.FillPrice, 10.005) || !near(b.Fee, 5) {
		t.Fatalf("buy: %+v", b)
	}
	if s1.Status != StatusRejected || !strings.Contains(s1.Reason, "可卖数量不足") {
		t.Fatalf("T+1 sell: %+v", s1)
	}
	// 卖出：01-07 9.995 × 500 = 4997.5，佣金 5 + 印花税 2.49875 → 7.50
	if s2.Status != StatusFilled || s2.FillDate != "2026-01-07" || !near(s2.Fee, 7.5) {
		t.Fatalf("sell: %+v", s2)
	}
	if len(st.Positions) != 1 || st.Positions[0].Qty != 500 {
		t.Fatalf("positions: %+v", st.Positions)
	}
	wantCash := 100000 - 10005 - 5 + 4997.5 - 7.5
	if !near(st.Cash, wantCash) || !near(st.Equity, wantCash+500*10) {
		t.Fatalf("cash %v equity %v", st.Cash, st.Equity)
	}
	// 已实现：卖出所得 4990 − 一半成本 5005 = −15
	if !near(st.Realized, 4997.5-7.5-5005) {
		t.Fatalf("realized %v", st.Realized)
	}
	if len(st.Curve) != len(cal) || st.AsOf != "2026-01-14" {
		t.Fatalf("curve %d %s", len(st.Curve), st.AsOf)
	}
}

func TestSuspensionExpireAndReject(t *testing.T) {
	p := noSlip()
	p.ExpireDays = 3
	in := Input{Start: "2026-01-05", Cash: 10000, Params: p, Calendar: cal,
		Bars: map[string][]ds.StockBar{
			"600001": flat(10, "2026-01-06", "2026-01-07"),                             // 停牌两天后复牌：顺延成交
			"600002": flat(10, "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09"), // 停牌超过 3 天：过期
			"600003": flat(10),
		},
		Orders: []Order{
			{ID: 1, Code: "600001", Kind: "stock", Side: Buy, Qty: 100, PlacedDate: "2026-01-05", Status: StatusPending},
			{ID: 2, Code: "600002", Kind: "stock", Side: Buy, Qty: 100, PlacedDate: "2026-01-05", Status: StatusPending},
			{ID: 3, Code: "600003", Kind: "stock", Side: Buy, Qty: 1000, PlacedDate: "2026-01-05", Status: StatusPending}, // 资金不足
			{ID: 4, Code: "600003", Kind: "stock", Side: Buy, Qty: 150, PlacedDate: "2026-01-05", Status: StatusPending},  // 非整手
		}}
	orders, _ := Replay(in)
	if orders[0].Status != StatusFilled || orders[0].FillDate != "2026-01-08" {
		t.Fatalf("suspended then filled: %+v", orders[0])
	}
	if orders[1].Status != StatusExpired {
		t.Fatalf("expired: %+v", orders[1])
	}
	if orders[2].Status != StatusRejected || !strings.Contains(orders[2].Reason, "资金不足") {
		t.Fatalf("no cash: %+v", orders[2])
	}
	if orders[3].Status != StatusRejected || !strings.Contains(orders[3].Reason, "整数倍") {
		t.Fatalf("odd lot: %+v", orders[3])
	}
}

func TestExRightsAndFrozenFills(t *testing.T) {
	bars := flat(10)
	bars[3] = ds.StockBar{Date: "2026-01-08", Close: 5, Mult: 2} // 10 送 10：持股翻倍，价格减半
	for i := 4; i < len(bars); i++ {
		bars[i].Close = 5
	}
	in := Input{Start: "2026-01-05", Cash: 10000, Params: noSlip(), Calendar: cal, Bars: map[string][]ds.StockBar{"600000": bars},
		Orders: []Order{
			{ID: 1, Code: "600000", Kind: "stock", Side: Buy, Qty: 500, PlacedDate: "2026-01-05", Status: StatusPending},
			{ID: 2, Code: "600000", Kind: "stock", Side: Sell, Qty: 1000, PlacedDate: "2026-01-12", Status: StatusPending}, // 送转后全部卖出
		}}
	orders, st := Replay(in)
	if orders[1].Status != StatusFilled || len(st.Positions) != 0 {
		t.Fatalf("sell after split: %+v %+v", orders[1], st.Positions)
	}
	// 除权前后净值连续：01-07 与 01-08 的权益相同（价格没变动，只有费用）
	if !near(st.Curve[2].Equity, st.Curve[3].Equity) {
		t.Fatalf("equity jump on ex-date: %v → %v", st.Curve[2].Equity, st.Curve[3].Equity)
	}
	// 已成交的委托：数据事后修正不改变成交价
	in.Orders = orders
	in.Bars["600000"][1].Close = 99
	again, _ := Replay(in)
	if again[0].FillPrice != orders[0].FillPrice {
		t.Fatalf("filled order re-priced: %v → %v", orders[0].FillPrice, again[0].FillPrice)
	}
}

func TestRebalanceAndChecks(t *testing.T) {
	p := noSlip()
	st := State{Cash: 50000, Equity: 100000, Positions: []Position{
		{Code: "600000", Kind: "stock", Qty: 3000, Price: 10, Value: 30000},
		{Code: "600001", Kind: "stock", Qty: 2050, Price: 10, Value: 20500},
	}}
	prices := map[string]float64{"600000": 10, "600001": 10, "510300": 4}
	orders, err := Rebalance(st, []Target{{Code: "600000", Kind: "stock", Weight: 0.1}, {Code: "510300", Kind: "fund", Weight: 0.5}}, prices, p)
	if err != nil {
		t.Fatal(err)
	}
	// 先卖：600001 全部（含零股）、600000 卖到约 10%；再买 510300
	if len(orders) != 3 || orders[0].Side != Sell || orders[1].Side != Sell || orders[2].Code != "510300" {
		t.Fatalf("orders: %+v", orders)
	}
	if orders[1].Code != "600001" || orders[1].Qty != 2050 || orders[0].Qty != 2000 || orders[2].Qty != 12400 {
		t.Fatalf("qty: %+v", orders)
	}
	if _, err := Rebalance(st, []Target{{Code: "600000", Weight: 0.7}, {Code: "510300", Weight: 0.5}}, prices, p); err == nil {
		t.Fatal("weights over 100% accepted")
	}

	pending := []Order{{Code: "600000", Side: Sell, Qty: 2500}, {Code: "510300", Side: Buy, Qty: 10000}}
	cases := []struct {
		o    Order
		want string
	}{
		{Order{Code: "600000", Side: Sell, Qty: 600}, "可卖数量不足"}, // 3000 − 待卖 2500 = 500
		{Order{Code: "600000", Side: Sell, Qty: 500}, ""},       // 卖出全部可卖（不是整手倍数也行）
		{Order{Code: "600001", Side: Sell, Qty: 150}, "整数倍"},    // 部分卖出须整手
		{Order{Code: "600000", Side: Buy, Qty: 1100}, "可用资金不足"}, // 50000 − 待买 40000 = 10000
		{Order{Code: "600000", Side: Buy, Qty: 900}, ""},
		{Order{Code: "600009", Side: Buy, Qty: 100}, "没有该证券的日线"},
		{Order{Code: "600000", Side: "x", Qty: 100}, "方向"},
	}
	for _, c := range cases {
		err := CheckAll(NewCheckInput(c.o, st, pending, prices), p)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%+v: want %q, got %v", c.o, c.want, err)
		}
	}
	p.MaxOrderAmount = 5000
	if err := CheckAll(NewCheckInput(Order{Code: "600000", Side: Buy, Qty: 600}, st, nil, prices), p); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("max amount: %v", err)
	}
}
