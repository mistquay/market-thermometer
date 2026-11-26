package job

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/market"
	"market-thermometer/internal/sim"
	"market-thermometer/internal/store"
)

func TestSimFlow(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub, _ := ds.NewHub(ds.Env{}, []string{}, nil, nil)
	days := []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08"}
	var bench []ds.Point
	var bars []ds.StockBar
	for i, d := range days {
		bench = append(bench, ds.Point{Date: d, Value: 4000 + float64(i)})
		bars = append(bars, ds.StockBar{Date: d, Close: 10 + float64(i), Mult: 1})
	}
	if _, err := st.UpsertPoints(ctx, ds.CNHS300Close, "csindex", bench, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveStockBars(ctx, "x", map[string][]ds.StockBar{"600000": bars[:2]}); err != nil { // 截至 01-06
		t.Fatal(err)
	}
	at := func(d string) time.Time {
		tt, _ := time.ParseInLocation("2006-01-02 15:04", d+" 20:00", ds.Shanghai)
		return tt
	}
	id, err := st.CreateSimAccount(ctx, store.SimAccount{Name: "a", Cash: 100000, Start: "2026-01-05", Params: sim.DefaultParams()}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// 01-06 晚上下单：第二笔超出资金，整批拒绝
	_, err = PlaceSimOrders(ctx, st, hub, market.CN, id, []sim.Order{{Code: "600000", Side: sim.Buy, Qty: 1000}, {Code: "600000", Side: sim.Buy, Qty: 9000}}, "manual", at("2026-01-06"))
	if err == nil || !strings.Contains(err.Error(), "第 2 笔") || !strings.Contains(err.Error(), "资金不足") {
		t.Fatalf("want batch rejection, got %v", err)
	}
	if _, err := PlaceSimOrders(ctx, st, hub, market.CN, id, []sim.Order{{Code: "510999", Side: sim.Buy, Qty: 100}}, "manual", at("2026-01-06")); err == nil {
		t.Fatal("order without local bars accepted")
	}
	ids, err := PlaceSimOrders(ctx, st, hub, market.CN, id, []sim.Order{{Code: "600000", Side: sim.Buy, Qty: 1000}}, "manual", at("2026-01-06"))
	if err != nil || len(ids) != 1 {
		t.Fatalf("place: %v %v", ids, err)
	}
	// 01-07 当天：日线已到，但只撮合到今天之前 → 仍待成交
	if err := st.SaveStockBars(ctx, "x", map[string][]ds.StockBar{"600000": bars}); err != nil {
		t.Fatal(err)
	}
	v, _, err := LoadSim(ctx, st, hub, market.CN, id, nil, at("2026-01-07"))
	if err != nil || v.Orders[0].Status != sim.StatusPending || v.Cutoff != "2026-01-07" {
		t.Fatalf("same day: %+v %v", v, err)
	}
	// 01-08：按 01-07 收盘 12 成交并落库
	v, _, err = LoadSim(ctx, st, hub, market.CN, id, nil, at("2026-01-08"))
	if err != nil || v.Orders[0].Status != sim.StatusFilled || v.Orders[0].FillDate != "2026-01-07" || len(v.State.Positions) != 1 {
		t.Fatalf("fill: %+v %v", v.Orders, err)
	}
	if saved, _ := st.SimOrders(ctx, id); saved[0].Status != sim.StatusFilled || saved[0].FillPrice == 0 {
		t.Fatalf("not persisted: %+v", saved)
	}
	if len(v.Bench) != len(v.State.Curve) || v.Bench[0] != 4000 {
		t.Fatalf("bench: %v", v.Bench)
	}
	// 撤单：已成交的不能撤
	if err := st.CancelSimOrder(ctx, id, ids[0], time.Now()); err == nil {
		t.Fatal("cancelled a filled order")
	}
	// 场外基金不能作为调仓目标；调到 50%：现持仓约 13%（1000 股 × 13 元 / 约 10.1 万）→ 买入 2800 股（整手）
	pid, _ := st.SavePortfolio(ctx, store.Portfolio{Name: "p", Items: []store.PortfolioItem{{Kind: "stock", Code: "600000", Weight: 1}, {Kind: "ofund", Code: "110022", Weight: 1}}}, time.Now())
	p, _ := st.Portfolio(ctx, pid)
	if _, err := PortfolioTargets(p); err == nil {
		t.Fatal("ofund target accepted")
	}
	plan, err := PlanRebalance(ctx, st, hub, market.CN, id, []sim.Target{{Code: "600000", Weight: 0.5}}, "", at("2026-01-09"))
	if err != nil || len(plan.Orders) != 1 || plan.Orders[0].Side != sim.Buy || plan.Orders[0].Qty != 2800 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
}
