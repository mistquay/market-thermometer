package pmstrat

import (
	"math"
	"testing"
)

func TestKellySizing(t *testing.T) {
	// 无手续费：p=0.60, ask=0.50 -> f* = (0.6-0.5)/(1-0.5) = 0.20
	if f := KellyFraction(0.60, 0.50, 0); math.Abs(f-0.20) > 1e-9 {
		t.Fatalf("kelly=%f want 0.20", f)
	}
	// 扣手续费（rate=0.05 -> 50c 时 0.0125）：cost=0.5125 -> f*=(0.6-0.5125)/(1-0.5125)=0.179487
	if f := KellyFraction(0.60, 0.50, 0.05); math.Abs(f-0.179487179) > 1e-6 {
		t.Fatalf("kelly with fee=%f", f)
	}
	// 负期望返回 0
	if f := KellyFraction(0.51, 0.50, 0.05); f != 0 {
		t.Fatalf("negative edge kelly=%f want 0", f)
	}
	// 1/4 凯利，equity=1000，上限 50 -> 1000 * 0.20 * 0.25 = 50
	if u := SizeUSD(1000, 0.60, 0.50, 0, SizingConfig{Mode: "kelly", KellyFrac: 0.25, FixedUSD: 50}); u != 50 {
		t.Fatalf("size=%f want 50", u)
	}
	// 上限裁剪
	if u := SizeUSD(10000, 0.60, 0.50, 0, SizingConfig{Mode: "kelly", KellyFrac: 0.25, FixedUSD: 50}); u != 50 {
		t.Fatalf("capped size=%f want 50", u)
	}
}
