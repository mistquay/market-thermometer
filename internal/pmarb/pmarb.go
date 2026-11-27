// Package pmarb 是 Polymarket 套利 / 错价扫描器（纯函数）。
//
// 支持两类结构性套利检测（均按真实订单簿逐档吃单并扣除吃单手续费）：
//  1. 二元市场（Binary）：同时买入 Up + Down（或 Yes + No）相同股数，若总成本 + 手续费 < 1.00 / 股，则锁定无风险套利；
//  2. 互斥多结果事件（NegRisk）：同时买入所有子市场的 Yes 各 1 股（互斥完备），若合计卖一（逐档）+ 手续费 < 1.00 / 份，则存在多结果买全套利。
package pmarb

import (
	"math"
	"sort"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/pmsim"
)

// Leg 是套利组合中的一条腿。
type Leg struct {
	Cond     string  `json:"conditionId"`
	Question string  `json:"question"`
	Outcome  string  `json:"outcome"`
	BestAsk  float64 `json:"bestAsk"`
	AvgAsk   float64 `json:"avgAsk"`
	AskSize  float64 `json:"askSize"`
	FeeRate  float64 `json:"feeRate"`
}

// Opportunity 是一条扫描出的套利 / 错价机会（按净收益排序）。
type Opportunity struct {
	Kind       string  `json:"kind"` // binary | negrisk
	Slug       string  `json:"slug"`
	Title      string  `json:"title"`
	Series     string  `json:"series,omitempty"`
	Shares     float64 `json:"shares"`     // 按目标规模在当前盘口可匹配的份数
	RawSumAsk  float64 `json:"rawSumAsk"`  // 买齐 1 份的卖一价格合计（不含手续费）
	TotalCost  float64 `json:"totalCost"`  // 按目标份数逐档吃单总成本（含手续费）
	Payout     float64 `json:"payout"`     // 到期确定兑付 = Shares * 1.0
	NetProfit  float64 `json:"netProfit"`  // Payout - TotalCost
	NetPerUnit float64 `json:"netPerUnit"` // 每份净收益（美元）
	ROI        float64 `json:"roi"`        // NetProfit / TotalCost
	Legs       []Leg   `json:"legs"`
}

// ScanBinary 检测单个二元市场的 Up+Down（Yes+No）买全套利。book 为第一个代币的盘口（第二个代币取镜像）。
// targetShares 为试算份数（例如 50 股），自动受限于双边盘口最小深度。
func ScanBinary(ev ds.PMEvent, m ds.PMMarket, book ds.Book, targetShares float64) (Opportunity, bool) {
	if len(m.Tokens) < 2 || len(m.Outcomes) < 2 || m.Closed {
		return Opportunity{}, false
	}
	if targetShares <= 0 {
		targetShares = 50
	}
	ob0 := pmsim.OutcomeBook(book, 0)
	ob1 := pmsim.OutcomeBook(book, 1)
	if len(ob0.Asks) == 0 || len(ob1.Asks) == 0 {
		return Opportunity{}, false
	}
	capShares := min(targetShares, min(totalDepth(ob0.Asks), totalDepth(ob1.Asks)))
	capShares = math.Floor(capShares*100) / 100
	if capShares < max(m.MinSize, 5) {
		return Opportunity{}, false
	}
	fee := pmsim.Fee{Rate: m.FeeRate, Exp: m.FeeExp}
	f0 := pmsim.Match(ob0, pmsim.Buy, capShares, 0, 0, fee)
	f1 := pmsim.Match(ob1, pmsim.Buy, capShares, 0, 0, fee)
	matched := min(f0.Shares, f1.Shares)
	if matched < max(m.MinSize, 5) {
		return Opportunity{}, false
	}
	// 如果双边成交股数不等，按较小份数重算
	if f0.Shares != matched {
		f0 = pmsim.Match(ob0, pmsim.Buy, matched, 0, 0, fee)
	}
	if f1.Shares != matched {
		f1 = pmsim.Match(ob1, pmsim.Buy, matched, 0, 0, fee)
	}
	cost := f0.Gross + f0.Fee + f1.Gross + f1.Fee
	profit := matched - cost
	opp := Opportunity{
		Kind:       "binary",
		Slug:       ev.Slug,
		Title:      ev.Title,
		Series:     ev.Series,
		Shares:     round2(matched),
		RawSumAsk:  round4(ob0.Asks[0].Price + ob1.Asks[0].Price),
		TotalCost:  round4(cost),
		Payout:     round2(matched),
		NetProfit:  round4(profit),
		NetPerUnit: round4(profit / matched),
		ROI:        round4(profit / max(cost, 1e-9)),
		Legs: []Leg{
			{Cond: m.ConditionID, Question: m.Question, Outcome: m.Outcomes[0], BestAsk: ob0.Asks[0].Price, AvgAsk: round4(f0.Avg()), AskSize: round2(totalDepth(ob0.Asks)), FeeRate: m.FeeRate},
			{Cond: m.ConditionID, Question: m.Question, Outcome: m.Outcomes[1], BestAsk: ob1.Asks[0].Price, AvgAsk: round4(f1.Avg()), AskSize: round2(totalDepth(ob1.Asks)), FeeRate: m.FeeRate},
		},
	}
	return opp, true
}

// ScanNegRisk 检测互斥多结果事件（negRisk=true）的“买全 Yes”套利：每个未关闭子市场均须有有效卖盘。
func ScanNegRisk(ev ds.PMEvent, books map[string]ds.Book, targetShares float64) (Opportunity, bool) {
	if !ev.NegRisk || ev.Closed || len(ev.Markets) < 2 {
		return Opportunity{}, false
	}
	if targetShares <= 0 {
		targetShares = 50
	}
	capShares := targetShares
	var active []ds.PMMarket
	for _, m := range ev.Markets {
		if m.Closed {
			continue
		}
		if len(m.Tokens) == 0 {
			return Opportunity{}, false
		}
		b, ok := books[m.Tokens[0]]
		if !ok || len(b.Asks) == 0 {
			return Opportunity{}, false // 缺任一腿卖一不能误报套利
		}
		active = append(active, m)
		capShares = min(capShares, totalDepth(b.Asks))
	}
	if len(active) < 2 {
		return Opportunity{}, false
	}
	capShares = math.Floor(capShares*100) / 100
	if capShares < 5 {
		return Opportunity{}, false
	}
	var rawSum, cost float64
	legs := make([]Leg, 0, len(active))
	for _, m := range active {
		b := books[m.Tokens[0]]
		fee := pmsim.Fee{Rate: m.FeeRate, Exp: m.FeeExp}
		f := pmsim.Match(b, pmsim.Buy, capShares, 0, 0, fee)
		if f.Shares < capShares-0.01 {
			return Opportunity{}, false
		}
		rawSum += b.Asks[0].Price
		cost += f.Gross + f.Fee
		name := m.GroupTitle
		if name == "" {
			name = m.Question
		}
		legs = append(legs, Leg{
			Cond: m.ConditionID, Question: name, Outcome: "Yes",
			BestAsk: b.Asks[0].Price, AvgAsk: round4(f.Avg()), AskSize: round2(totalDepth(b.Asks)), FeeRate: m.FeeRate,
		})
	}
	profit := capShares - cost
	return Opportunity{
		Kind:       "negrisk",
		Slug:       ev.Slug,
		Title:      ev.Title,
		Series:     ev.Series,
		Shares:     round2(capShares),
		RawSumAsk:  round4(rawSum),
		TotalCost:  round4(cost),
		Payout:     round2(capShares),
		NetProfit:  round4(profit),
		NetPerUnit: round4(profit / capShares),
		ROI:        round4(profit / max(cost, 1e-9)),
		Legs:       legs,
	}, true
}

// SortOpportunities 按每份净收益从高到低排序。
func SortOpportunities(opps []Opportunity) {
	sort.Slice(opps, func(i, j int) bool {
		if opps[i].NetPerUnit != opps[j].NetPerUnit {
			return opps[i].NetPerUnit > opps[j].NetPerUnit
		}
		return opps[i].NetProfit > opps[j].NetProfit
	})
}

func totalDepth(ls []ds.BookLevel) float64 {
	s := 0.0
	for _, l := range ls {
		s += l.Size
	}
	return s
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
