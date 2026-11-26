package sim

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// CheckInput 是风控检查的输入：委托、账户状态、按最新收盘价估算的价格与待成交委托占用。
type CheckInput struct {
	Order       Order
	State       State
	Price       float64            // 该证券最新收盘价
	PendingBuy  float64            // 待成交买单按最新收盘价估算的金额合计
	PendingSell map[string]float64 // 各证券待成交卖出数量
}

// NewCheckInput 由待成交委托与最新收盘价组装检查输入。
func NewCheckInput(o Order, st State, pending []Order, prices map[string]float64) CheckInput {
	in := CheckInput{Order: o, State: st, Price: prices[o.Code], PendingSell: map[string]float64{}}
	for _, q := range pending {
		if q.Side == Buy {
			in.PendingBuy += q.Qty * prices[q.Code]
		} else {
			in.PendingSell[q.Code] += q.Qty
		}
	}
	return in
}

// Checker 是委托提交前的风控检查。核心对每笔委托依次执行全部 Checkers，任何一个返回错误就拒绝提交；
// 插件或配置只能追加检查，不能绕过（设计见 roadmap §3）。
type Checker interface {
	Check(in CheckInput, p Params) error
}

// Checkers 是生效的风控检查（默认只有基础检查）。
var Checkers = []Checker{BasicChecker{}}

// CheckAll 依次执行全部风控检查。
func CheckAll(in CheckInput, p Params) error {
	for _, c := range Checkers {
		if err := c.Check(in, p); err != nil {
			return err
		}
	}
	return nil
}

// BasicChecker：数量合法、单笔金额上限、可用资金（扣除待成交买单）、可卖数量（扣除待成交卖单）。
// 按最新收盘价估算，实际成交价以撮合日收盘价为准，所以撮合时还会再检查一次资金与可卖数量。
type BasicChecker struct{}

func (BasicChecker) Check(in CheckInput, p Params) error {
	o, price := in.Order, in.Price
	if o.Side != Buy && o.Side != Sell {
		return errors.New("买卖方向须为 buy 或 sell")
	}
	if !(o.Qty > 0) || math.IsInf(o.Qty, 0) {
		return errors.New("数量须为正数")
	}
	if price <= 0 {
		return errors.New("本地没有该证券的日线，无法估算金额")
	}
	amount := o.Qty * price
	if p.MaxOrderAmount > 0 && amount > p.MaxOrderAmount {
		return fmt.Errorf("单笔金额约 %.0f 元，超过上限 %.0f 元", amount, p.MaxOrderAmount)
	}
	lot := float64(p.LotSize)
	if o.Side == Buy {
		if math.Mod(o.Qty, lot) != 0 {
			return fmt.Errorf("买入数量须为 %d 的整数倍", p.LotSize)
		}
		need := amount*(1+p.SlippageBps/10000) + math.Max(amount*p.CommissionRate, p.MinCommission)
		if avail := in.State.Cash - in.PendingBuy; need > avail+1e-6 {
			return fmt.Errorf("可用资金不足：约需 %.2f 元，可用 %.2f 元（已扣除待成交买单）", need, avail)
		}
		return nil
	}
	held := 0.0
	for _, ps := range in.State.Positions {
		if ps.Code == o.Code {
			held = ps.Qty
		}
	}
	avail := held - in.PendingSell[o.Code]
	if o.Qty > avail+1e-6 {
		return fmt.Errorf("可卖数量不足：持有 %.0f，待成交卖出 %.0f", held, in.PendingSell[o.Code])
	}
	if math.Mod(o.Qty, lot) != 0 && math.Abs(o.Qty-avail) > 1e-6 {
		return fmt.Errorf("卖出数量须为 %d 的整数倍，或一次卖出全部可卖数量", p.LotSize)
	}
	return nil
}

// Target 是调仓目标：证券与目标权重（占账户总资产，合计不超过 1，其余为现金）。
type Target struct {
	Code   string
	Kind   string
	Weight float64
}

// CashBuffer 是调仓时预留的现金比例（覆盖费用与滑点，避免买单因资金不足被拒）。
const CashBuffer = 0.005

// Rebalance 生成把账户调到目标权重的委托：先卖后买，按最新收盘价估算，数量取整手；
// 目标中没有的持仓全部卖出。prices 为最新收盘价。返回的委托未分配 ID、未经风控检查。
func Rebalance(st State, targets []Target, prices map[string]float64, p Params) ([]Order, error) {
	sum := 0.0
	want := map[string]Target{}
	for _, t := range targets {
		if t.Weight < 0 {
			return nil, fmt.Errorf("%s 的目标权重为负", t.Code)
		}
		sum += t.Weight
		want[t.Code] = t
	}
	if sum > 1+1e-9 {
		return nil, fmt.Errorf("目标权重合计 %.1f%%，超过 100%%", sum*100)
	}
	lot := float64(p.LotSize)
	equity := st.Equity * (1 - CashBuffer)
	var sells, buys []Order
	held := map[string]Position{}
	for _, ps := range st.Positions {
		held[ps.Code] = ps
		t, ok := want[ps.Code]
		price := prices[ps.Code]
		if !ok || t.Weight == 0 {
			sells = append(sells, Order{Code: ps.Code, Kind: ps.Kind, Side: Sell, Qty: ps.Qty, Note: "目标中没有，全部卖出"})
			continue
		}
		if price <= 0 {
			continue
		}
		diff := t.Weight*equity - ps.Qty*price
		if q := math.Floor(-diff/price/lot) * lot; diff < 0 && q > 0 {
			sells = append(sells, Order{Code: ps.Code, Kind: ps.Kind, Side: Sell, Qty: math.Min(q, ps.Qty),
				Note: fmt.Sprintf("调到目标 %.1f%%", t.Weight*100)})
		}
	}
	for _, t := range targets {
		price := prices[t.Code]
		if t.Weight == 0 {
			continue
		}
		if price <= 0 {
			return nil, fmt.Errorf("本地没有 %s 的日线，无法估算调仓数量", t.Code)
		}
		diff := t.Weight*equity - held[t.Code].Qty*price
		if q := math.Floor(diff/price/lot) * lot; q > 0 {
			buys = append(buys, Order{Code: t.Code, Kind: t.Kind, Side: Buy, Qty: q, Note: fmt.Sprintf("调到目标 %.1f%%", t.Weight*100)})
		}
	}
	sort.Slice(sells, func(i, j int) bool { return sells[i].Code < sells[j].Code })
	sort.Slice(buys, func(i, j int) bool { return buys[i].Code < buys[j].Code })
	return append(sells, buys...), nil
}
