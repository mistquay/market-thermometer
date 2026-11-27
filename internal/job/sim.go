package job

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/market"
	"market-thermometer/internal/sim"
	"market-thermometer/internal/store"
)

// SimStats 是模拟账户的绩效统计指标。
type SimStats struct {
	TotalRet     float64  `json:"totalRet"`
	CAGR         *float64 `json:"cagr"`
	Vol          *float64 `json:"vol"`
	MaxDD        float64  `json:"maxDD"`
	Sharpe       *float64 `json:"sharpe"`
	BenchRet     *float64 `json:"benchRet"`
	ExcessRet    *float64 `json:"excessRet"`
	FilledOrders int      `json:"filledOrders"`
	TradingDays  int      `json:"tradingDays"`
}

// SimView 是一个模拟账户回放后的完整视图。
type SimView struct {
	Account store.SimAccount  `json:"account"`
	State   sim.State         `json:"state"`
	Stats   SimStats          `json:"stats"`
	Orders  []sim.Order       `json:"orders"`
	Bench   []float64         `json:"bench"` // 与 State.Curve 对齐的沪深300 收盘价
	Names   map[string]string `json:"names"`
	Cutoff  string            `json:"cutoff"` // 撮合只用到这一天（不含）之前的日线
}

// simInputs 读取回放所需的本地数据：交易日历与基准（沪深300）、委托涉及证券的日线。
func simInputs(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, acc store.SimAccount, orders []sim.Order, extra []string, now time.Time) (sim.Input, map[string]float64, map[string]float64, string, error) {
	bench, err := loadMetric(ctx, st, hub, mkt.Roles[market.RoleClose])
	if err != nil {
		return sim.Input{}, nil, nil, "", err
	}
	// 只撮合到今天之前：数据源当天的收盘价可能是临时值（stockdb 24:00 校对），成交价一旦记录就不再改写
	cutoff := now.In(ds.Shanghai).Format("2006-01-02")
	in := sim.Input{Start: acc.Start, Cash: acc.Cash, Params: acc.Params, Orders: orders, Bars: map[string][]ds.StockBar{}}
	benchAt := map[string]float64{}
	for _, p := range bench {
		if p.Date < cutoff {
			in.Calendar = append(in.Calendar, p.Date)
			benchAt[p.Date] = p.Value
		}
	}
	prices := map[string]float64{}
	codes := append([]string(nil), extra...)
	for _, o := range orders {
		codes = append(codes, o.Code)
	}
	for _, c := range codes {
		if _, ok := in.Bars[c]; ok {
			continue
		}
		bars, err := st.StockBars(ctx, c, "")
		if err != nil {
			return sim.Input{}, nil, nil, "", err
		}
		in.Bars[c] = bars
		if n := len(bars); n > 0 {
			prices[c] = bars[n-1].Close
		}
	}
	return in, prices, benchAt, cutoff, nil
}

// LoadSim 回放账户，保存新产生的撮合结果，返回视图。extra 为额外需要最新价的证券（下单、调仓时用）。
func LoadSim(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, id int64, extra []string, now time.Time) (*SimView, map[string]float64, error) {
	acc, err := st.SimAccount(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	orders, err := st.SimOrders(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	in, prices, benchAt, cutoff, err := simInputs(ctx, st, hub, mkt, acc, orders, extra, now)
	if err != nil {
		return nil, nil, err
	}
	updated, state := sim.Replay(in)
	var changed []sim.Order
	for i, o := range updated {
		if orders[i].Status == sim.StatusPending && o.Status != sim.StatusPending {
			changed = append(changed, o)
		}
	}
	if err := st.UpdateSimOrders(ctx, changed, now); err != nil {
		return nil, nil, err
	}
	if updated == nil {
		updated = []sim.Order{} // JSON 输出 [] 而不是 null
	}
	v := &SimView{Account: acc, State: state, Orders: updated, Cutoff: cutoff, Bench: []float64{}}
	for _, p := range state.Curve {
		v.Bench = append(v.Bench, benchAt[p.Date])
	}
	v.Stats = computeSimStats(acc.Cash, state.Equity, state.Curve, v.Bench, updated)
	var items []store.Security
	seen := map[string]bool{}
	for _, o := range updated {
		if !seen[o.Code] {
			seen[o.Code] = true
			items = append(items, store.Security{Kind: store.SecStock, Code: o.Code})
		}
	}
	names, err := st.SecurityNames(ctx, items)
	if err != nil {
		return nil, nil, err
	}
	v.Names = map[string]string{}
	for k, n := range names {
		v.Names[strings.TrimPrefix(k, store.SecStock+":")] = n
	}
	return v, prices, nil
}

// settleSims 回放全部模拟账户，保存撮合结果（每日同步后调用）。
func (r *Runner) settleSims(ctx context.Context) {
	accs, err := r.store.SimAccounts(ctx)
	if err != nil || len(accs) == 0 {
		return
	}
	for _, a := range accs {
		if _, _, err := LoadSim(ctx, r.store, r.hub, r.mkt, a.ID, nil, r.now()); err != nil {
			r.logf("✗ 模拟账户 %s 撮合：%v", a.Name, err)
		}
	}
	r.logf("✓ 模拟账户撮合：%d 个账户", len(accs))
}

// PlaceSimOrders 校验并提交一批委托：任何一笔未通过风控检查则整批不提交，返回全部错误。
func PlaceSimOrders(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, id int64, orders []sim.Order, source string, now time.Time) ([]int64, error) {
	if len(orders) == 0 || len(orders) > 100 {
		return nil, errors.New("一次须提交 1–100 笔委托")
	}
	var codes []string
	for i := range orders {
		o := &orders[i]
		o.Code = strings.TrimSpace(o.Code)
		o.Kind = ds.KindOf(o.Code)
		if o.Kind == "" {
			return nil, fmt.Errorf("%q 不是沪深 A 股或场内基金代码（场外基金不支持模拟交易）", o.Code)
		}
		o.Source, o.PlacedDate, o.Status = source, now.In(ds.Shanghai).Format("2006-01-02"), sim.StatusPending
		o.Qty = math.Round(o.Qty*10000) / 10000
		codes = append(codes, o.Code)
	}
	v, prices, err := LoadSim(ctx, st, hub, mkt, id, codes, now)
	if err != nil {
		return nil, err
	}
	var pending []sim.Order
	for _, o := range v.Orders {
		if o.Status == sim.StatusPending {
			pending = append(pending, o)
		}
	}
	var errs []error
	for i, o := range orders {
		if err := sim.CheckAll(sim.NewCheckInput(o, v.State, pending, prices), v.Account.Params); err != nil {
			errs = append(errs, fmt.Errorf("第 %d 笔（%s %s %.0f）：%w", i+1, sideText(o.Side), o.Code, o.Qty, err))
			continue
		}
		pending = append(pending, o) // 同一批后面的委托要扣除前面的占用
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return st.InsertSimOrders(ctx, id, orders, now)
}

func sideText(s string) string {
	if s == sim.Buy {
		return "买入"
	}
	return "卖出"
}

// RebalancePlan 是调仓预览：目标与生成的委托（未提交）。
type RebalancePlan struct {
	Targets []sim.Target `json:"targets"`
	Orders  []sim.Order  `json:"orders"`
	Note    string       `json:"note"`
}

// PlanRebalance 按目标权重生成调仓委托预览。
func PlanRebalance(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, id int64, targets []sim.Target, note string, now time.Time) (*RebalancePlan, error) {
	var codes []string
	for i := range targets {
		targets[i].Kind = ds.KindOf(targets[i].Code)
		if targets[i].Kind == "" {
			return nil, fmt.Errorf("%s 不是沪深 A 股或场内基金（场外基金不支持模拟交易）", targets[i].Code)
		}
		codes = append(codes, targets[i].Code)
	}
	v, prices, err := LoadSim(ctx, st, hub, mkt, id, codes, now)
	if err != nil {
		return nil, err
	}
	// 待成交委托会让预览失真：要求先等它们成交或撤单
	for _, o := range v.Orders {
		if o.Status == sim.StatusPending {
			return nil, errors.New("还有待成交的委托，等它们成交或撤单后再调仓")
		}
	}
	orders, err := sim.Rebalance(v.State, targets, prices, v.Account.Params)
	if err != nil {
		return nil, err
	}
	return &RebalancePlan{Targets: targets, Orders: orders, Note: note}, nil
}

// PortfolioTargets 把组合的权重归一化为调仓目标（场外基金不支持）。
func PortfolioTargets(p store.Portfolio) ([]sim.Target, error) {
	total := 0.0
	for _, it := range p.Items {
		if it.Kind == store.SecOFund {
			return nil, fmt.Errorf("组合中的场外基金 %s 不能用于模拟交易", it.Code)
		}
		total += it.Weight
	}
	if total <= 0 {
		return nil, errors.New("组合没有持仓")
	}
	out := make([]sim.Target, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, sim.Target{Code: it.Code, Weight: it.Weight / total})
	}
	return out, nil
}

// StrategyPosition 是温度计策略在最新交易日的分层仓位：估值档底仓 ± 最近一次信号的加减仓（与回测“估值底仓 + 温度计加减仓”同口径）。
type StrategyPosition struct {
	Date       string  `json:"date"`
	Tier       string  `json:"tier"`
	Base       float64 `json:"base"`
	Overlay    float64 `json:"overlay"`
	Weight     float64 `json:"weight"`
	LastSignal string  `json:"lastSignal,omitempty"`
}

// ThermometerPosition 按生效参数计算最新的分层仓位。
func ThermometerPosition(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, now time.Time) (StrategyPosition, error) {
	p, err := st.ActiveParams(ctx, now)
	if err != nil {
		return StrategyPosition{}, err
	}
	res, _, err := Run(ctx, st, hub, mkt, p)
	if err != nil {
		return StrategyPosition{}, err
	}
	if len(res.Days) == 0 {
		return StrategyPosition{}, errors.New("还没有得分数据")
	}
	sp := StrategyPosition{}
	for _, e := range res.Events {
		switch {
		case e.Type == engine.EvBuyDip && !e.Observe:
			sp.Overlay, sp.LastSignal = p.Position.Step, e.Date+" 底部信号"
		case e.Type == engine.EvSellPeak:
			sp.Overlay, sp.LastSignal = -p.Position.Step, e.Date+" 顶部信号"
		}
	}
	last := res.Days[len(res.Days)-1]
	sp.Date, sp.Tier = last.Date, last.Tier
	sp.Base = p.Position.Base(last.Tier)
	sp.Weight = math.Max(0, math.Min(1, sp.Base+sp.Overlay))
	return sp, nil
}

func computeSimStats(initCash, equity float64, curve []sim.Point, bench []float64, orders []sim.Order) SimStats {
	st := SimStats{TradingDays: len(curve)}
	if initCash > 0 {
		st.TotalRet = equity/initCash - 1
	}
	for _, o := range orders {
		if o.Status == sim.StatusFilled {
			st.FilledOrders++
		}
	}
	if len(curve) < 2 {
		return st
	}
	peak := curve[0].Equity
	var rets []float64
	for i, pt := range curve {
		if pt.Equity > peak {
			peak = pt.Equity
		}
		if peak > 0 {
			dd := pt.Equity/peak - 1
			if dd < st.MaxDD {
				st.MaxDD = dd
			}
		}
		if i > 0 && curve[i-1].Equity > 0 {
			rets = append(rets, pt.Equity/curve[i-1].Equity-1)
		}
	}
	n := len(rets)
	if n >= 5 && initCash > 0 && equity > 0 {
		cagr := math.Pow(equity/initCash, 252.0/float64(n)) - 1
		st.CAGR = &cagr
		mean := 0.0
		for _, r := range rets {
			mean += r
		}
		mean /= float64(n)
		var vsum float64
		for _, r := range rets {
			d := r - mean
			vsum += d * d
		}
		if n > 1 && vsum > 0 {
			vol := math.Sqrt(vsum/float64(n-1)) * math.Sqrt(252)
			st.Vol = &vol
			if vol > 1e-9 {
				sh := (cagr - 0.02) / vol
				st.Sharpe = &sh
			}
		}
	}
	if len(bench) == len(curve) && bench[0] > 0 && bench[len(bench)-1] > 0 {
		br := bench[len(bench)-1]/bench[0] - 1
		ex := st.TotalRet - br
		st.BenchRet = &br
		st.ExcessRet = &ex
	}
	return st
}
