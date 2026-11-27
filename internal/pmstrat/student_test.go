package pmstrat

import (
	"math"
	"testing"
)

func TestStudentTAndBlendedSigma(t *testing.T) {
	// df=1 为柯西分布：CDF(1) = 0.5 + atan(1)/pi = 0.75
	if got := StudentTCDF(1, 1); math.Abs(got-0.75) > 1e-6 {
		t.Fatalf("t(df=1, z=1) = %f want 0.75", got)
	}
	if got := StudentTCDF(-1, 1); math.Abs(got-0.25) > 1e-6 {
		t.Fatalf("t(df=1, z=-1) = %f want 0.25", got)
	}
	// 大自由度趋近标准正态 Φ(1.96) ≈ 0.975
	if got := StudentTCDF(1.96, 1000); math.Abs(got-Phi(1.96)) > 1e-3 {
		t.Fatalf("t(df=1000, z=1.96) = %f want ~%f", got, Phi(1.96))
	}
	// 厚尾特性：同等方差下，远端尾部 z=2.5 时 t(df=5) 给出的极端置信度应低于正态（更保守，降低过自信）
	pNorm := Phi(2.5)
	pT := StudentTCDF(2.5*math.Sqrt(5.0/3.0), 5)
	if !(pT < pNorm && pT > 0.95) {
		t.Fatalf("expected conservative tail: pT=%f pNorm=%f", pT, pNorm)
	}
	// BlendedSigma：最后 5 分钟突发高波动时应显著高于 30 分钟平稳波动
	var pts []Point
	base := int64(1700000000_000)
	price := 100000.0
	for i := 0; i < 720; i++ { // 30 分钟，每 2.5s
		step := 0.00005
		if i >= 600 { // 最后 5 分钟放大 4 倍波动
			step = 0.00025
		}
		if i%2 == 0 {
			price *= 1 + step
		} else {
			price *= 1 - step
		}
		pts = append(pts, Point{T: base + int64(i)*2500, P: price})
	}
	nowMs := pts[len(pts)-1].T
	s30 := Sigma(pts)
	sb := BlendedSigma(pts, nowMs)
	if !(sb > s30*1.2) {
		t.Fatalf("expected blended sigma (%f) > 1.2 * s30 (%f)", sb, s30)
	}
}
