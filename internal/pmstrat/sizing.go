package pmstrat

import "math"

// SizingConfig 描述二元市场单笔下注金额计算参数。
type SizingConfig struct {
	Mode      string  `json:"mode"`      // fixed | kelly
	FixedUSD  float64 `json:"fixedUsd"`  // fixed 模式下的单笔金额，或 kelly 模式下的单笔上限
	KellyFrac float64 `json:"kellyFrac"` // 分数凯利系数（如 0.25）
	MinUSD    float64 `json:"minUsd"`    // 低于该金额不下单（默认 5）
}

// KellyFraction 返回二元结果（赢赔 1，输归 0）在买入价 ask（含手续费）下的理论凯利仓位比例 f* ∈ [0, 1]。
// 成本 c = ask + feePerShare；净赔率 b = (1 - c) / c；f* = (p - c) / (1 - c)。
func KellyFraction(p, ask, feeRate float64) float64 {
	if p <= 0 || p >= 1 || ask <= 0 || ask >= 1 {
		return 0
	}
	fee := 0.0
	if feeRate > 0 {
		fee = feeRate * ask * (1 - ask)
	}
	cost := ask + fee
	if cost <= 0 || cost >= 1 || p <= cost {
		return 0
	}
	f := (p - cost) / (1 - cost)
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return 0
	}
	return min(f, 1)
}

// SizeUSD 计算单笔建议买入金额（USDC，不含手续费）。
func SizeUSD(equity, p, ask, feeRate float64, cfg SizingConfig) float64 {
	if equity <= 0 {
		return 0
	}
	minUSD := cfg.MinUSD
	if minUSD <= 0 {
		minUSD = 5
	}
	var usd float64
	switch cfg.Mode {
	case "kelly":
		frac := cfg.KellyFrac
		if frac <= 0 {
			frac = 0.25
		}
		f := KellyFraction(p, ask, feeRate) * frac
		usd = equity * f
		if cfg.FixedUSD > 0 && usd > cfg.FixedUSD {
			usd = cfg.FixedUSD
		}
	default:
		usd = cfg.FixedUSD
		if usd <= 0 {
			usd = 20
		}
	}
	if usd > equity*0.5 { // 保护：单笔不超过账户权益的 50%
		usd = equity * 0.5
	}
	if usd < minUSD {
		return 0
	}
	return math.Round(usd*100) / 100
}
