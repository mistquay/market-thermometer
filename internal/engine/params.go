package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Params 是打分参数集。所有阈值都是初始值，需由回测校准（设计文档 §10）。
type Params struct {
	// 估值层
	ValuationWindow int     `json:"valuationWindow"` // ERP 分位窗口（交易日）
	ValuationMinW   int     `json:"valuationMinW"`
	CheapBelow      float64 `json:"cheapBelow"`     // V ≤ 此值 → 便宜
	ExpensiveAbove  float64 `json:"expensiveAbove"` // V ≥ 此值 → 昂贵
	MaxFillDays     int     `json:"maxFillDays"`    // PE、利率允许前值填充的最多交易日
	TierHysteresis  float64 `json:"tierHysteresis"` // 估值档滞回：离开便宜需 V > 便宜线+此值，离开昂贵需 V < 昂贵线-此值

	// 择时层
	TimingWindow int        `json:"timingWindow"` // T_k、T_pct 分位窗口
	TimingMinW   int        `json:"timingMinW"`
	PreAvgDays   int        `json:"preAvgDays"` // T1、T2 原始值先取 N 日均值
	RSIDailyW    float64    `json:"rsiDailyW"`  // R_mix = a·RSI_d + b·RSI_w
	RSIWeeklyW   float64    `json:"rsiWeeklyW"`
	RSIPeriod    int        `json:"rsiPeriod"`
	Weights      [4]float64 `json:"weights"` // T1..T4 权重；0 表示该维度不启用（不算降级）
	EMASpan      int        `json:"emaSpan"` // T_s = EMA(T_pct, N)
	Extreme      float64    `json:"extreme"` // 子维度 ≤ 此值计入 N_low，≥ 100-此值计入 N_high

	// 状态机
	Signal SignalParams `json:"signal"`
}

// DefaultParams：四维等权。T3 广度依赖个股数据源；未配置时把 weights[2] 设为 0 即不启用（不算降级）。
func DefaultParams() Params {
	return Params{
		ValuationWindow: 2520, ValuationMinW: 500, CheapBelow: 30, ExpensiveAbove: 70, MaxFillDays: 5, TierHysteresis: 5,
		TimingWindow: 750, TimingMinW: 250, PreAvgDays: 5,
		RSIDailyW: 0.4, RSIWeeklyW: 0.6, RSIPeriod: 14,
		Weights: [4]float64{1, 1, 1, 1},
		EMASpan: 3, Extreme: 20,
		Signal: DefaultSignalParams(),
	}
}

// Version 是参数的内容哈希，用作 score_daily.params_version。
func (p Params) Version() string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:6])
}

// Validate 拒绝明显无效的参数（来自 Web 表单时使用）。
func (p Params) Validate() error {
	var errs []error
	check := func(ok bool, msg string) {
		if !ok {
			errs = append(errs, errors.New(msg))
		}
	}
	check(p.ValuationWindow >= 20 && p.ValuationMinW >= 20 && p.ValuationMinW <= p.ValuationWindow, "估值窗口须 ≥20，且最少样本不大于窗口")
	check(p.TimingWindow >= 20 && p.TimingMinW >= 20 && p.TimingMinW <= p.TimingWindow, "择时窗口须 ≥20，且最少样本不大于窗口")
	check(p.CheapBelow > 0 && p.CheapBelow < p.ExpensiveAbove && p.ExpensiveAbove < 100, "须满足 0 < 便宜线 < 昂贵线 < 100")
	check(p.TierHysteresis >= 0 && p.TierHysteresis < (p.ExpensiveAbove-p.CheapBelow)/2, "估值档滞回须 ≥0 且小于两条线间距的一半")
	check(p.MaxFillDays >= 0 && p.MaxFillDays <= 20, "前值填充天数须在 0–20")
	check(p.PreAvgDays >= 1 && p.PreAvgDays <= 60, "均值天数须在 1–60")
	check(p.RSIPeriod >= 2 && p.RSIPeriod <= 100, "RSI 周期须在 2–100")
	check(p.RSIDailyW >= 0 && p.RSIWeeklyW >= 0 && p.RSIDailyW+p.RSIWeeklyW > 0, "RSI 权重须非负且不全为 0")
	check(p.EMASpan >= 1 && p.EMASpan <= 60, "平滑天数须在 1–60")
	check(p.Extreme > 0 && p.Extreme < 50, "极端区阈值须在 0–50")
	sumW := 0.0
	for _, w := range p.Weights {
		check(w >= 0, "维度权重不能为负")
		sumW += w
	}
	check(sumW > 0, "至少启用一个择时维度")
	sp := p.Signal
	check(sp.MinExtremeDims >= 0 && sp.MinExtremeDims <= 4, "极端维度数须在 0–4")
	check(sp.RiseDays >= 1 && sp.RiseDays <= 20, "连续上升天数须在 1–20")
	check(sp.ArmTimeout >= 1, "武装超时须 ≥1")
	check(sp.Rebound > 0 && sp.Pullback > 0 && sp.DivergenceGap > 0, "回暖、回落、背离幅度须为正")
	for _, v := range []float64{sp.ArmLowCheap, sp.ArmLowFair, sp.ArmLowExpensive, sp.ArmHighCheap, sp.ArmHighFair,
		sp.ArmHighExpensive, sp.DisarmMid, sp.CooldownLowEx, sp.CooldownHighEx, sp.RSIExit} {
		check(v >= 0 && v <= 100, "温度类阈值须在 0–100")
	}
	return errors.Join(errs...)
}
