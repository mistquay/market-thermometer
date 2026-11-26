// Package sim 是模拟交易的纯函数：按本地日线撮合委托、回放账户得到持仓、资金与净值。
//
// 模型与券商无关：账户只记初始资金与交易参数，委托是唯一的事实记录；持仓与资金从委托逐笔回放得到，
// 所以每一笔“委托 → 成交 → 持仓”都可追溯。已成交 / 已拒绝 / 已过期的委托保持记录的结果不变
// （数据源事后修正不会改写历史成交），只有待成交的委托参与撮合。
//
// 默认假设（均为 Params 参数，见 docs/sim.md）：
//   - D 日提交的委托在 D 之后的第一个交易日按该证券当日收盘价（不复权）成交，买入加、卖出减滑点；
//   - 该证券当日没有日线（停牌）则顺延，提交后超过 ExpireDays 个交易日仍未成交则过期；
//   - 买入须为整手（LotSize）；卖出须为整手，或一次卖光全部可卖数量（零股）；
//   - T+1：当日买入成交的数量当日不可卖；
//   - 佣金按成交额 × 费率、不足最低佣金按最低收；印花税只对股票卖出收取；
//   - 持仓期间的分红送转按除权乘数折算持股数（相当于红利再投资，不计红利税）；
//   - 不处理涨跌停、成交量约束（按收盘价全部成交）。
package sim

import (
	"errors"
	"fmt"
	"math"
	"sort"

	ds "market-thermometer/internal/datasource"
)

// Params 是交易参数。
type Params struct {
	CommissionRate float64 `json:"commissionRate"` // 佣金费率，0.00025 = 万 2.5
	MinCommission  float64 `json:"minCommission"`  // 最低佣金（元）
	StampDuty      float64 `json:"stampDuty"`      // 印花税（股票卖出），0.0005 = 万 5
	SlippageBps    float64 `json:"slippageBps"`    // 滑点（基点），买入价上浮、卖出价下浮
	LotSize        int     `json:"lotSize"`        // 每手股数
	ExpireDays     int     `json:"expireDays"`     // 委托提交后最多等待的交易日数
	MaxOrderAmount float64 `json:"maxOrderAmount"` // 单笔委托金额上限（元），0 为不限（风控）
}

// DefaultParams 是常见 A 股零售费率。
func DefaultParams() Params {
	return Params{CommissionRate: 0.00025, MinCommission: 5, StampDuty: 0.0005, SlippageBps: 5, LotSize: 100, ExpireDays: 5}
}

// Validate 检查参数。
func (p Params) Validate() error {
	switch {
	case p.CommissionRate < 0 || p.CommissionRate > 0.01, p.StampDuty < 0 || p.StampDuty > 0.01:
		return errors.New("费率须在 0–1% 之间")
	case p.MinCommission < 0, p.SlippageBps < 0 || p.SlippageBps > 500:
		return errors.New("最低佣金须非负，滑点须在 0–500 基点之间")
	case p.LotSize < 1, p.ExpireDays < 1 || p.ExpireDays > 60:
		return errors.New("每手股数须 ≥1，过期天数须在 1–60 之间")
	case p.MaxOrderAmount < 0:
		return errors.New("单笔上限须非负")
	}
	return nil
}

// 委托状态与方向。
const (
	StatusPending  = "pending"
	StatusFilled   = "filled"
	StatusRejected = "rejected"
	StatusExpired  = "expired"
	StatusCanceled = "canceled"

	Buy  = "buy"
	Sell = "sell"
)

// Order 是一笔委托（同时记录撮合结果）。
type Order struct {
	ID         int64   `json:"id"`
	Code       string  `json:"code"`
	Kind       string  `json:"kind"` // stock | fund（场内基金）
	Side       string  `json:"side"`
	Qty        float64 `json:"qty"`
	PlacedDate string  `json:"placedDate"` // 提交日（北京时间），在其后的第一个交易日撮合
	Source     string  `json:"source"`     // manual | rebalance | strategy
	Note       string  `json:"note,omitempty"`
	Status     string  `json:"status"`
	FillDate   string  `json:"fillDate,omitempty"`
	FillPrice  float64 `json:"fillPrice,omitempty"`
	Amount     float64 `json:"amount,omitempty"` // 成交金额（不含费用）
	Fee        float64 `json:"fee,omitempty"`
	Reason     string  `json:"reason,omitempty"` // 拒绝 / 过期原因
}

// Position 是一只证券的持仓。
type Position struct {
	Code      string  `json:"code"`
	Kind      string  `json:"kind"`
	Qty       float64 `json:"qty"`
	Cost      float64 `json:"cost"`      // 持仓成本合计（含买入费用）
	Price     float64 `json:"price"`     // 最新收盘价
	PriceDate string  `json:"priceDate"` // 最新收盘价的日期
	Value     float64 `json:"value"`
	PnL       float64 `json:"pnl"` // 浮动盈亏 = 市值 − 成本
}

// Point 是净值曲线的一天。
type Point struct {
	Date   string  `json:"date"`
	Equity float64 `json:"equity"`
	Cash   float64 `json:"cash"`
}

// State 是回放后的账户状态。
type State struct {
	Cash      float64    `json:"cash"`
	Positions []Position `json:"positions"`
	Equity    float64    `json:"equity"`
	Realized  float64    `json:"realized"` // 已实现盈亏（卖出所得 − 对应成本 − 卖出费用）
	Fees      float64    `json:"fees"`
	Curve     []Point    `json:"curve"`
	AsOf      string     `json:"asOf"` // 回放到的最后一个交易日
}

// Input 是一次回放的输入。
type Input struct {
	Start    string // 账户起始日：只回放不早于它的交易日
	Cash     float64
	Params   Params
	Calendar []string                 // 交易日历（升序）
	Bars     map[string][]ds.StockBar // 证券 → 本地日线（升序，不复权收盘 + 除权乘数）
	Orders   []Order                  // 按 ID 升序
}

type pos struct {
	kind        string
	qty, cost   float64
	boughtToday float64
	lastPrice   float64
	lastDate    string
}

// Replay 回放账户：按交易日处理除权、撮合待成交委托、计算净值。返回更新后的委托（状态可能变化）与账户状态。
func Replay(in Input) ([]Order, State) {
	orders := append([]Order(nil), in.Orders...)
	sort.Slice(orders, func(i, j int) bool { return orders[i].ID < orders[j].ID })
	bar := map[string]map[string]ds.StockBar{}
	for code, bs := range in.Bars {
		m := make(map[string]ds.StockBar, len(bs))
		for _, b := range bs {
			m[b.Date] = b
		}
		bar[code] = m
	}
	cash := in.Cash
	ps := map[string]*pos{}
	st := State{}
	waited := map[int]int{} // 委托下标 → 已等待的交易日数

	for _, d := range in.Calendar {
		if d < in.Start {
			continue
		}
		// 1. 除权：持股数 × 乘数；更新最新价
		for code, p := range ps {
			p.boughtToday = 0
			if b, ok := bar[code][d]; ok {
				if b.Mult > 0 && b.Mult != 1 {
					p.qty *= b.Mult
				}
				p.lastPrice, p.lastDate = b.Close, d
			}
		}
		// 2. 委托：已成交的按记录回放；待成交的撮合
		for i := range orders {
			o := &orders[i]
			switch {
			case o.Status == StatusFilled && o.FillDate == d:
				dc, dr := applyFill(ps, o)
				cash, st.Realized = cash+dc, st.Realized+dr
			case o.Status == StatusPending && o.PlacedDate < d:
				b, ok := bar[o.Code][d]
				if !ok {
					waited[i]++
					if waited[i] >= in.Params.ExpireDays {
						o.Status, o.Reason = StatusExpired, fmt.Sprintf("提交后 %d 个交易日没有该证券的日线（停牌或本地缺数），已过期", waited[i])
					}
					continue
				}
				if reason := match(o, b, d, cash, ps, in.Params); reason != "" {
					o.Status, o.Reason, o.FillDate = StatusRejected, reason, d
					continue
				}
				dc, dr := applyFill(ps, o)
				cash, st.Realized = cash+dc, st.Realized+dr
			}
		}
		// 3. 净值
		eq := cash
		for _, p := range ps {
			eq += p.qty * p.lastPrice
		}
		st.Curve = append(st.Curve, Point{Date: d, Equity: eq, Cash: cash})
		st.AsOf = d
	}

	st.Cash, st.Equity = cash, cash
	for _, o := range orders {
		if o.Status == StatusFilled {
			st.Fees += o.Fee
		}
	}
	codes := make([]string, 0, len(ps))
	for c := range ps {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		p := ps[c]
		if p.qty <= 1e-9 {
			continue
		}
		v := p.qty * p.lastPrice
		st.Equity += v
		st.Positions = append(st.Positions, Position{Code: c, Kind: p.kind, Qty: round(p.qty, 4), Cost: p.cost, Price: p.lastPrice,
			PriceDate: p.lastDate, Value: v, PnL: v - p.cost})
	}
	return orders, st
}

// match 按当日收盘价撮合一笔待成交委托；成交时填写成交字段并返回 ""，否则返回拒绝原因。
func match(o *Order, b ds.StockBar, d string, cash float64, ps map[string]*pos, p Params) string {
	slip := p.SlippageBps / 10000
	lot := float64(p.LotSize)
	price := b.Close * (1 + slip)
	if o.Side == Sell {
		price = b.Close * (1 - slip)
	}
	amount := price * o.Qty
	fee := math.Max(amount*p.CommissionRate, p.MinCommission)
	switch o.Side {
	case Buy:
		if o.Qty <= 0 || math.Mod(o.Qty, lot) != 0 {
			return fmt.Sprintf("买入数量须为 %d 的整数倍", p.LotSize)
		}
		if amount+fee > cash+1e-6 {
			return fmt.Sprintf("资金不足：需要 %.2f，可用 %.2f", amount+fee, cash)
		}
	case Sell:
		avail := 0.0
		if q := ps[o.Code]; q != nil {
			avail = q.qty - q.boughtToday
		}
		if o.Qty <= 0 || o.Qty > avail+1e-6 {
			return fmt.Sprintf("可卖数量不足：委托 %.0f，可卖 %.0f", o.Qty, avail)
		}
		if math.Mod(o.Qty, lot) != 0 && math.Abs(o.Qty-avail) > 1e-6 {
			return fmt.Sprintf("卖出数量须为 %d 的整数倍，或一次卖出全部可卖数量", p.LotSize)
		}
		if o.Kind == "stock" {
			fee += amount * p.StampDuty
		}
	default:
		return "未知的买卖方向"
	}
	o.Status, o.FillDate, o.FillPrice, o.Amount, o.Fee, o.Reason = StatusFilled, d, round(price, 4), round(amount, 2), round(fee, 2), ""
	return ""
}

// applyFill 把一笔已成交委托计入持仓，返回资金变化与已实现盈亏（平均成本法，成本随除权折算的持股数分摊）。
func applyFill(ps map[string]*pos, o *Order) (cashDelta, realized float64) {
	p := ps[o.Code]
	if p == nil {
		p = &pos{kind: o.Kind, lastPrice: o.FillPrice, lastDate: o.FillDate}
		ps[o.Code] = p
	}
	if o.Side == Buy {
		p.qty += o.Qty
		p.boughtToday += o.Qty
		p.cost += o.Amount + o.Fee
		return -(o.Amount + o.Fee), 0
	}
	part := 0.0
	if p.qty > 0 {
		part = p.cost * math.Min(o.Qty/p.qty, 1)
	}
	p.cost -= part
	p.qty -= o.Qty
	if p.qty < 1e-6 {
		p.qty, p.cost = 0, 0
	}
	return o.Amount - o.Fee, o.Amount - o.Fee - part
}

func round(x float64, digits int) float64 {
	f := math.Pow(10, float64(digits))
	return math.Round(x*f) / f
}
