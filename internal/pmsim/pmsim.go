// Package pmsim 是预测市场（二元结果）模拟盘的核心逻辑：盘口换边、逐档吃单撮合、手续费、委托回放、结算与风控。
// 纯函数，不访问网络与数据库；编排（取盘口、存委托）在 internal/pmtrade。
//
// 成交模型（默认假设，见 docs/polymarket.md §7）：
//   - 只模拟吃单（IOC）：按盘口逐档吃到数量 / 金额 / 限价为止，吃不完的部分取消，不假设档外还有量；不模拟挂单排队；
//   - 第二个结果（Down / No）的盘口是第一个结果的镜像：卖价 = 1 − 对方买价，数量相同；
//   - 手续费按每一档的成交价分别计算（股数 × rate × (p(1−p))^exp），以 USDC 从现金扣除；
//   - 不能卖空：看空就买另一个结果；
//   - 结算只认本地市场的赢家：赢的结果每股兑付 1 美元，输的为 0；出结果前持仓按买一估值。
package pmsim

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	ds "market-thermometer/internal/datasource"
)

// Side 是买卖方向。
type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// OutcomeBook 由第一个结果代币的盘口得到第 outcome 个结果（0 或 1）的盘口。
func OutcomeBook(first ds.Book, outcome int) ds.Book {
	if outcome == 0 {
		return first
	}
	out := ds.Book{Time: first.Time, Hash: first.Hash}
	for _, l := range first.Asks { // 对方卖价从低到高 → 本方买价从高到低
		out.Bids = append(out.Bids, ds.BookLevel{Price: round(1-l.Price, 6), Size: l.Size})
	}
	for _, l := range first.Bids { // 对方买价从高到低 → 本方卖价从低到高
		out.Asks = append(out.Asks, ds.BookLevel{Price: round(1-l.Price, 6), Size: l.Size})
	}
	return out
}

// Fee 是吃单手续费参数（与 ds.PMMarket.FeeRate / FeeExp 相同）。
type Fee struct {
	Rate float64
	Exp  float64
}

// Of 返回以价格 p 成交 shares 股的手续费。
func (f Fee) Of(p, shares float64) float64 {
	if f.Rate <= 0 {
		return 0
	}
	exp := f.Exp
	if exp == 0 {
		exp = 1
	}
	return shares * f.Rate * math.Pow(p*(1-p), exp)
}

// Fill 是一次吃单的结果。
type Fill struct {
	Shares float64        `json:"shares"`
	Gross  float64        `json:"gross"` // 成交金额（不含手续费）
	Fee    float64        `json:"fee"`
	Levels []ds.BookLevel `json:"levels"` // 各档成交（价、股数）
}

// Avg 返回成交均价。
func (f Fill) Avg() float64 {
	if f.Shares == 0 {
		return 0
	}
	return f.Gross / f.Shares
}

// Match 逐档吃单（IOC）。
//   - 买：吃 book.Asks，到 limit（最高可接受价，0 表示不限）为止；amount > 0 时按金额（不含手续费）买，否则按 shares 股；
//   - 卖：吃 book.Bids，到 limit（最低可接受价）为止，卖 shares 股。
//
// 股数按 0.01 股向下取整。
func Match(book ds.Book, side Side, shares, amount, limit float64, fee Fee) Fill {
	var f Fill
	levels := book.Asks
	if side == Sell {
		levels = book.Bids
	}
	for _, l := range levels {
		if l.Size <= 0 {
			continue
		}
		if side == Buy && limit > 0 && l.Price > limit+1e-9 {
			break
		}
		if side == Sell && l.Price < limit-1e-9 {
			break
		}
		q := l.Size
		if side == Buy && amount > 0 {
			q = min(q, (amount-f.Gross)/l.Price)
		} else {
			q = min(q, shares-f.Shares)
		}
		q = math.Floor(q*100+1e-9) / 100
		if q <= 0 {
			break
		}
		f.Shares = round(f.Shares+q, 2)
		f.Gross += q * l.Price
		f.Fee += fee.Of(l.Price, q)
		f.Levels = append(f.Levels, ds.BookLevel{Price: l.Price, Size: q})
	}
	f.Gross, f.Fee = round(f.Gross, 6), round(f.Fee, 6)
	return f
}

// Order 是一笔模拟委托（只增不删；状态由下单时一次撮合决定）。
type Order struct {
	ID          int64     `json:"id"`
	Account     int64     `json:"account"`
	Time        time.Time `json:"time"`
	Slug        string    `json:"slug"`        // 事件
	Cond        string    `json:"conditionId"` // 市场
	Token       string    `json:"token"`       // 成交的结果代币
	Outcome     int       `json:"outcome"`
	OutcomeName string    `json:"outcomeName"`
	Question    string    `json:"question"`
	Side        Side      `json:"side"`
	ReqShares   float64   `json:"reqShares,omitempty"`
	ReqAmount   float64   `json:"reqAmount,omitempty"`
	Limit       float64   `json:"limit,omitempty"`
	Source      string    `json:"source"` // manual 或 strategy:名称
	Status      string    `json:"status"` // filled / partial / rejected
	Reason      string    `json:"reason,omitempty"`
	Fill        Fill      `json:"fill"`
	BookTime    time.Time `json:"bookTime,omitzero"` // 撮合所用盘口的时间
}

const (
	StatusFilled   = "filled"
	StatusPartial  = "partial"
	StatusRejected = "rejected"
)

// Market 是回放与估值需要的市场信息。
type Market struct {
	Cond     string
	Slug     string
	Question string
	Outcomes []string
	Tokens   []string
	End      time.Time
	Winner   int // −1 表示未出结果
}

// Position 是一个结果代币的持仓。
type Position struct {
	Cond        string    `json:"conditionId"`
	Slug        string    `json:"slug"`
	Question    string    `json:"question"`
	Token       string    `json:"token"`
	Outcome     int       `json:"outcome"`
	OutcomeName string    `json:"outcomeName"`
	Shares      float64   `json:"shares"`
	Cost        float64   `json:"cost"` // 剩余持仓的成本（含买入手续费）
	End         time.Time `json:"end"`
	Settled     bool      `json:"settled"`
	Payout      float64   `json:"payout,omitempty"` // 结算兑付
	Mark        float64   `json:"mark,omitempty"`   // 估值价（买一），由调用方填
}

// State 是账户回放结果。
type State struct {
	Cash      float64    `json:"cash"`
	Positions []Position `json:"positions"` // 未结算在前，按结束时间
	Realized  float64    `json:"realized"`  // 已实现盈亏（卖出、结算；已扣手续费）
	Fees      float64    `json:"fees"`
	Volume    float64    `json:"volume"` // 累计成交金额
	Orders    int        `json:"orders"`
}

// Replay 从初始资金与委托（按时间升序）回放出资金与持仓；markets 中已出结果的市场按赢家结算。
func Replay(cash0 float64, orders []Order, markets map[string]Market) State {
	st := State{Cash: cash0}
	pos := map[string]*Position{}
	var keys []string
	for _, o := range orders {
		if o.Status == StatusRejected || o.Fill.Shares <= 0 {
			continue
		}
		st.Orders++
		p := pos[o.Token]
		if p == nil {
			p = &Position{Cond: o.Cond, Slug: o.Slug, Question: o.Question, Token: o.Token, Outcome: o.Outcome, OutcomeName: o.OutcomeName}
			pos[o.Token] = p
			keys = append(keys, o.Token)
		}
		st.Fees += o.Fill.Fee
		st.Volume += o.Fill.Gross
		if o.Side == Buy {
			st.Cash -= o.Fill.Gross + o.Fill.Fee
			p.Shares = round(p.Shares+o.Fill.Shares, 2)
			p.Cost += o.Fill.Gross + o.Fill.Fee
			continue
		}
		q := min(o.Fill.Shares, p.Shares)
		basis := 0.0
		if p.Shares > 0 {
			basis = p.Cost * q / p.Shares
		}
		proceeds := o.Fill.Gross - o.Fill.Fee
		st.Cash += proceeds
		st.Realized += proceeds - basis
		p.Shares = round(p.Shares-q, 2)
		p.Cost -= basis
	}
	for _, k := range keys {
		p := pos[k]
		m, ok := markets[p.Cond]
		if ok {
			p.End = m.End
		}
		if ok && m.Winner >= 0 && p.Shares > 0 {
			p.Settled = true
			if m.Winner == p.Outcome {
				p.Payout = p.Shares
			}
			st.Cash += p.Payout
			st.Realized += p.Payout - p.Cost
		}
		if p.Shares > 0 {
			st.Positions = append(st.Positions, *p)
		}
	}
	sort.SliceStable(st.Positions, func(i, j int) bool {
		a, b := st.Positions[i], st.Positions[j]
		if a.Settled != b.Settled {
			return !a.Settled
		}
		return a.End.After(b.End)
	})
	st.Cash, st.Realized, st.Fees, st.Volume = round(st.Cash, 6), round(st.Realized, 6), round(st.Fees, 6), round(st.Volume, 6)
	return st
}

// Params 是账户风控参数（核心强制执行）。
type Params struct {
	MaxOrderUSD  float64 `json:"maxOrderUsd"`  // 单笔买入金额上限
	MaxDailyUSD  float64 `json:"maxDailyUsd"`  // 单日（本地时区）买入金额上限
	MaxMarketUSD float64 `json:"maxMarketUsd"` // 单个市场的持仓成本上限
	MaxAssetUSD  float64 `json:"maxAssetUsd"`  // 单资产（如 BTC / ETH 跨 5m/15m/1h）同时在途成本上限，0 不限
	Halted       bool    `json:"halted"`       // 账户熔断：拒绝一切新委托
}

func DefaultParams() Params {
	return Params{MaxOrderUSD: 50, MaxDailyUSD: 500, MaxMarketUSD: 100, MaxAssetUSD: 150}
}

func (p Params) Validate() error {
	if p.MaxOrderUSD <= 0 || p.MaxDailyUSD <= 0 || p.MaxMarketUSD <= 0 || p.MaxAssetUSD < 0 {
		return errors.New("风控上限须合法（单笔/单日/单市场为正数，单资产非负）")
	}
	if p.MaxOrderUSD > p.MaxDailyUSD {
		return errors.New("单笔上限不能超过单日上限")
	}
	return nil
}

// CheckInput 是下单前风控检查的输入。
type CheckInput struct {
	Side        Side
	Amount      float64 // 买入时预计成交金额（不含手续费）
	Shares      float64 // 卖出股数
	DailyBought float64 // 今日已买入金额
	MarketCost  float64 // 该市场现有持仓成本（两个结果合计）
	AssetCost   float64 // 同资产（如 BTC 跨周期）未结算持仓成本合计
	Held        float64 // 该结果代币现有股数
	Cash        float64
	GlobalHalt  bool
}

// Check 执行风控：熔断、单笔 / 单日 / 单市场 / 单资产上限、现金、不可卖空。
func Check(p Params, in CheckInput) error {
	switch {
	case in.GlobalHalt:
		return errors.New("全局熔断中：拒绝一切新委托")
	case p.Halted:
		return errors.New("账户熔断中")
	}
	if in.Side == Sell {
		if in.Shares > in.Held+1e-9 {
			return fmt.Errorf("持仓不足：持有 %.2f 股，卖出 %.2f 股（不能卖空，看空请买另一个结果）", in.Held, in.Shares)
		}
		return nil
	}
	switch {
	case in.Amount > p.MaxOrderUSD+1e-9:
		return fmt.Errorf("超过单笔上限 %.2f 美元", p.MaxOrderUSD)
	case in.DailyBought+in.Amount > p.MaxDailyUSD+1e-9:
		return fmt.Errorf("超过单日上限 %.2f 美元（今日已买 %.2f）", p.MaxDailyUSD, in.DailyBought)
	case in.MarketCost+in.Amount > p.MaxMarketUSD+1e-9:
		return fmt.Errorf("超过单市场上限 %.2f 美元（已持有成本 %.2f）", p.MaxMarketUSD, in.MarketCost)
	case p.MaxAssetUSD > 0 && in.AssetCost+in.Amount > p.MaxAssetUSD+1e-9:
		return fmt.Errorf("超过单资产跨周期上限 %.2f 美元（同资产已持有 %.2f）", p.MaxAssetUSD, in.AssetCost)
	case in.Amount > in.Cash+1e-9:
		return fmt.Errorf("现金不足：%.2f 美元", in.Cash)
	}
	return nil
}

func round(x float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(x*p) / p
}
