package engine

import (
	"fmt"
	"math"
)

// SignalParams 是状态机参数（设计文档 §4，全部为初始值，需回测校准）。
type SignalParams struct {
	// 武装线按估值档调节：便宜时更积极抄底、更不易卖飞。
	ArmLowCheap      float64 `json:"armLowCheap"`
	ArmLowFair       float64 `json:"armLowFair"`
	ArmLowExpensive  float64 `json:"armLowExpensive"`
	ArmHighCheap     float64 `json:"armHighCheap"`
	ArmHighFair      float64 `json:"armHighFair"`
	ArmHighExpensive float64 `json:"armHighExpensive"`
	MinExtremeDims   int     `json:"minExtremeDims"`   // 武装还需至少 N 个子维度处于极端区
	TopOnlyExpensive bool    `json:"topOnlyExpensive"` // 顶部只在估值昂贵时武装（便宜/合理时不卖）

	Rebound        float64 `json:"rebound"`        // 底部右侧回暖：T_s ≥ 本轮最低 + 此值
	RiseDays       int     `json:"riseDays"`       // 且 T_s 连续上升天数
	Pullback       float64 `json:"pullback"`       // 顶部动能衰减：T_s ≤ 本轮最高 − 此值
	RSIExit        float64 `json:"rsiExit"`        // 且日线 RSI 低于此值
	DivergenceGap  float64 `json:"divergenceGap"`  // 背离：价格创本轮新低/新高，T_s 与极值相差至少此值
	ArmTimeout     int     `json:"armTimeout"`     // 武装超过 N 个交易日未触发则失效
	DisarmMid      float64 `json:"disarmMid"`      // 底部武装时 T_s 回到此值以上 / 顶部武装时跌到此值以下即失效
	CooldownLowEx  float64 `json:"cooldownLowEx"`  // 底部信号后，T_s 回到此值以上才允许再次武装
	CooldownHighEx float64 `json:"cooldownHighEx"` // 顶部信号后，T_s 回到此值以下才允许再次武装
}

func DefaultSignalParams() SignalParams {
	return SignalParams{
		ArmLowCheap: 30, ArmLowFair: 20, ArmLowExpensive: 15,
		ArmHighCheap: 85, ArmHighFair: 80, ArmHighExpensive: 75,
		MinExtremeDims: 2,
		Rebound:        8, RiseDays: 2, Pullback: 8, RSIExit: 70, DivergenceGap: 5,
		ArmTimeout: 60, DisarmMid: 50, CooldownLowEx: 35, CooldownHighEx: 65,
	}
}

// Phase 是状态机阶段。
type Phase string

const (
	PhaseIdle           Phase = "IDLE"
	PhaseBottomArmed    Phase = "BOTTOM_ARMED"
	PhaseTopArmed       Phase = "TOP_ARMED"
	PhaseBottomCooldown Phase = "BOTTOM_COOLDOWN"
	PhaseTopCooldown    Phase = "TOP_COOLDOWN"
)

// 事件类型（设计文档 §4.4）。
const (
	EvArmedBottom = "ARMED_BOTTOM"
	EvArmedTop    = "ARMED_TOP"
	EvBuyDip      = "BUY_DIP"
	EvSellPeak    = "SELL_PEAK"
	EvDisarmed    = "DISARMED"
	EvTierChange  = "VALUATION_TIER_CHANGE"
	EvDegraded    = "DATA_DEGRADED"
)

// State 是状态机在某日收盘后的状态。
type State struct {
	Phase      Phase   `json:"phase"`
	ArmedIdx   int     `json:"-"`
	ArmedDate  string  `json:"armedDate,omitempty"`
	ArmedDays  int     `json:"armedDays"`
	MinTS      float64 `json:"minTs"`
	PriceAtMin float64 `json:"priceAtMin"`
	MaxTS      float64 `json:"maxTs"`
	PriceAtMax float64 `json:"priceAtMax"`
	Tier       string  `json:"tier"`
}

// Event 是状态机产生的事件。
type Event struct {
	Date    string  `json:"date"`
	Idx     int     `json:"-"`
	Type    string  `json:"type"`
	Reason  string  `json:"reason"`
	Observe bool    `json:"observe"` // 估值昂贵时的底部信号只作"观察"，不计入持仓
	Close   float64 `json:"close"`
	TS      float64 `json:"tS"`
	V       float64 `json:"v"`
	Tier    string  `json:"tier"`
}

func (p SignalParams) armLow(tier string) float64 {
	switch tier {
	case TierCheap:
		return p.ArmLowCheap
	case TierExpensive:
		return p.ArmLowExpensive
	}
	return p.ArmLowFair
}

func (p SignalParams) armHigh(tier string) float64 {
	switch tier {
	case TierCheap:
		return p.ArmHighCheap
	case TierExpensive:
		return p.ArmHighExpensive
	}
	return p.ArmHighFair
}

// RunSignals 按时间顺序回放状态机，返回每日收盘后的状态和全部事件。
// 纯函数：第 t 天的状态只依赖第 0..t 天的数据，回测与实盘共用。
func RunSignals(days []Day, p SignalParams) ([]State, []Event) {
	states := make([]State, len(days))
	var events []Event
	s := State{Phase: PhaseIdle}
	var hist []float64 // 有效 T_s 的历史，用于判断"连续上升"

	for t, d := range days {
		emit := func(typ, reason string) {
			events = append(events, Event{Date: d.Date, Idx: t, Type: typ, Reason: reason,
				Close: d.Close, TS: d.TS, V: d.V, Tier: d.Tier})
		}
		// 估值档切换（与择时状态无关）
		if d.Tier != "" && s.Tier != "" && d.Tier != s.Tier {
			emit(EvTierChange, fmt.Sprintf("%s → %s", s.Tier, d.Tier))
		}
		if d.Tier != "" {
			s.Tier = d.Tier
		}
		if math.IsNaN(d.TS) {
			states[t] = s
			continue
		}
		hist = append(hist, d.TS)
		if d.Degraded {
			emit(EvDegraded, "有一个维度缺数，本日不武装、不触发")
		}
		rising := risingDays(hist, p.RiseDays)

		switch s.Phase {
		case PhaseBottomCooldown:
			if d.TS > p.CooldownLowEx {
				s.Phase = PhaseIdle
			}
		case PhaseTopCooldown:
			if d.TS < p.CooldownHighEx {
				s.Phase = PhaseIdle
			}
		case PhaseBottomArmed:
			s.ArmedDays = t - s.ArmedIdx
			switch {
			case s.ArmedDays > p.ArmTimeout:
				emit(EvDisarmed, fmt.Sprintf("底部武装超过 %d 个交易日未触发，进入冷却", p.ArmTimeout))
				s = State{Phase: PhaseBottomCooldown, Tier: s.Tier} // 超时后须先回到中性区，避免当日立即重新武装
			case d.TS > p.DisarmMid:
				emit(EvDisarmed, fmt.Sprintf("T_s 回到 %.0f 以上，底部武装失效", p.DisarmMid))
				s = idle(s)
			case !d.Degraded && d.TS >= s.MinTS+p.Rebound && rising:
				ev := Event{Date: d.Date, Idx: t, Type: EvBuyDip, Close: d.Close, TS: d.TS, V: d.V, Tier: d.Tier,
					Reason:  fmt.Sprintf("右侧回暖：T_s 较本轮最低 %.1f 回升 %.1f，且连续 %d 日上升", s.MinTS, d.TS-s.MinTS, p.RiseDays),
					Observe: d.Tier == TierExpensive}
				events = append(events, ev)
				s.Phase = PhaseBottomCooldown
			case !d.Degraded && d.Close < s.PriceAtMin && d.TS >= s.MinTS+p.DivergenceGap:
				ev := Event{Date: d.Date, Idx: t, Type: EvBuyDip, Close: d.Close, TS: d.TS, V: d.V, Tier: d.Tier,
					Reason:  fmt.Sprintf("底背离：价格跌破本轮温度最低日收盘 %.2f，T_s 却高出最低 %.1f", s.PriceAtMin, d.TS-s.MinTS),
					Observe: d.Tier == TierExpensive}
				events = append(events, ev)
				s.Phase = PhaseBottomCooldown
			default:
				if d.TS < s.MinTS {
					s.MinTS, s.PriceAtMin = d.TS, d.Close
				}
			}
		case PhaseTopArmed:
			s.ArmedDays = t - s.ArmedIdx
			switch {
			case s.ArmedDays > p.ArmTimeout:
				emit(EvDisarmed, fmt.Sprintf("顶部武装超过 %d 个交易日未触发，进入冷却", p.ArmTimeout))
				s = State{Phase: PhaseTopCooldown, Tier: s.Tier}
			case d.TS < p.DisarmMid:
				emit(EvDisarmed, fmt.Sprintf("T_s 跌到 %.0f 以下，顶部武装失效", p.DisarmMid))
				s = idle(s)
			case !d.Degraded && d.TS <= s.MaxTS-p.Pullback && d.RSID < p.RSIExit:
				emit(EvSellPeak, fmt.Sprintf("动能衰减：T_s 较本轮最高 %.1f 回落 %.1f，日 RSI %.0f", s.MaxTS, s.MaxTS-d.TS, d.RSID))
				s.Phase = PhaseTopCooldown
			case !d.Degraded && d.Close > s.PriceAtMax && d.TS <= s.MaxTS-p.DivergenceGap:
				emit(EvSellPeak, fmt.Sprintf("顶背离：价格突破本轮温度最高日收盘 %.2f，T_s 却低于最高 %.1f", s.PriceAtMax, s.MaxTS-d.TS))
				s.Phase = PhaseTopCooldown
			default:
				if d.TS > s.MaxTS {
					s.MaxTS, s.PriceAtMax = d.TS, d.Close
				}
			}
		}

		// IDLE（含当日刚从冷却/失效回到 IDLE）时判断是否武装
		if s.Phase == PhaseIdle && !d.Degraded {
			switch {
			case d.TS <= p.armLow(d.Tier) && d.NLow >= p.MinExtremeDims:
				s = State{Phase: PhaseBottomArmed, ArmedIdx: t, ArmedDate: d.Date, MinTS: d.TS, PriceAtMin: d.Close, Tier: s.Tier}
				emit(EvArmedBottom, fmt.Sprintf("T_s %.1f ≤ 武装线 %.0f，%d 个维度处于冰点区", d.TS, p.armLow(d.Tier), d.NLow))
			case d.TS >= p.armHigh(d.Tier) && d.NHigh >= p.MinExtremeDims && (!p.TopOnlyExpensive || d.Tier == TierExpensive):
				s = State{Phase: PhaseTopArmed, ArmedIdx: t, ArmedDate: d.Date, MaxTS: d.TS, PriceAtMax: d.Close, Tier: s.Tier}
				emit(EvArmedTop, fmt.Sprintf("T_s %.1f ≥ 武装线 %.0f，%d 个维度处于过热区", d.TS, p.armHigh(d.Tier), d.NHigh))
			}
		}
		states[t] = s
	}
	return states, events
}

func idle(s State) State { return State{Phase: PhaseIdle, Tier: s.Tier} }

// risingDays：最近 n 天 T_s 严格逐日上升（需要 n+1 个点）。
func risingDays(hist []float64, n int) bool {
	if len(hist) < n+1 {
		return false
	}
	h := hist[len(hist)-n-1:]
	for i := 1; i < len(h); i++ {
		if h[i] <= h[i-1] {
			return false
		}
	}
	return true
}
