package pmstrat

import (
	"math"
	"math/rand/v2"
	"testing"
)

func flat(from, to int64, p float64) []Point {
	var out []Point
	for t := from * 1000; t <= to*1000; t += 2500 {
		out = append(out, Point{T: t, P: p})
	}
	return out
}

func TestProbUpBasic(t *testing.T) {
	sigma := 2e-5 // 每 √秒约 0.2bp
	in := Inputs{Now: 1000, Start: 900, End: 1200, K: 100, Sigma: sigma, Spot: flat(800, 1000, 100)}
	r, ok := ProbUp(in)
	if !ok || math.Abs(r.P-0.5) > 1e-9 || r.Tau != 200 {
		t.Fatalf("at K: %+v %v", r, ok)
	}
	// 现价高出 1 个标准差 → Φ(1)
	sd := sigma * math.Sqrt(200-40)
	in.Spot = flat(800, 1000, 100*math.Exp(sd))
	if r, _ := ProbUp(in); math.Abs(r.P-Phi(1)) > 1e-9 {
		t.Fatalf("1 sd: %+v", r)
	}
	// 基差：币安价高 13bp，换算后等于 K → 0.5
	in.Basis, in.Spot = 0.0013, flat(800, 1000, 100*1.0013)
	if r, _ := ProbUp(in); math.Abs(r.P-0.5) > 1e-9 {
		t.Fatalf("basis: %+v", r)
	}
	// 最后 10 秒：窗口已过去的 50 秒都高于 K → 几乎肯定 Up
	in = Inputs{Now: 1190, Start: 900, End: 1200, K: 100, Sigma: sigma, Spot: flat(1100, 1190, 100.05)}
	if r, _ := ProbUp(in); r.P < 0.999 {
		t.Fatalf("near end: %+v", r)
	}
	// 现价太旧、还没开始、已结束：不估值
	in = Inputs{Now: 1000, Start: 900, End: 1200, K: 100, Sigma: sigma, Spot: flat(800, 980, 100)}
	if _, ok := ProbUp(in); ok {
		t.Fatal("stale spot")
	}
	in.Spot, in.Now = flat(800, 890, 100), 890
	if _, ok := ProbUp(in); ok {
		t.Fatal("not started")
	}
	// K 的误差加大方差：概率向 0.5 收缩
	in = Inputs{Now: 1000, Start: 900, End: 1200, K: 100, Sigma: sigma, Spot: flat(800, 1000, 100*math.Exp(sd))}
	a, _ := ProbUp(in)
	in.KErrBp = 5
	b, _ := ProbUp(in)
	if !(b.P < a.P && b.P > 0.5) {
		t.Fatalf("k error should shrink: %v → %v", a.P, b.P)
	}
}

// 蒙特卡洛：按已知 σ 模拟 2.5 秒一步的布朗路径，结算为结束前 60 秒均值 ≥ K；
// 检查模型在不同剩余时间的概率与实际频率一致（按概率分组的可靠性）与 Brier 分数。
func TestProbUpCalibration(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	const sigma = 1.5e-4 // 每 √秒（约 BTC 5 分钟 25bp 的量级）
	const step = 2.5
	start, end := int64(0), int64(300)
	for _, tau := range []int64{240, 90, 45, 15} {
		var bins [10]struct{ n, up int }
		var brier float64
		n := 4000
		for range n {
			// 路径：起点前 60 秒到结束
			var pts []Point
			x := 0.0
			for ts := -60.0; ts <= float64(end); ts += step {
				pts = append(pts, Point{T: int64(ts * 1000), P: math.Exp(x)})
				x += sigma * math.Sqrt(step) * rng.NormFloat64()
			}
			k, _ := TwapBefore(pts, start, 60)
			final, _ := TwapBefore(pts, end, 60)
			now := end - tau
			var seen []Point
			for _, p := range pts {
				if p.T <= now*1000 {
					seen = append(seen, p)
				}
			}
			r, ok := ProbUp(Inputs{Now: now, Start: start, End: end, K: k, Sigma: sigma, Spot: seen})
			if !ok {
				t.Fatal("not ok")
			}
			up := 0
			if final >= k {
				up = 1
			}
			b := min(int(r.P*10), 9)
			bins[b].n++
			bins[b].up += up
			brier += (r.P - float64(up)) * (r.P - float64(up))
		}
		brier /= float64(n)
		for i, b := range bins {
			if b.n < 150 {
				continue
			}
			freq := float64(b.up) / float64(b.n)
			mid := (float64(i) + 0.5) / 10
			if math.Abs(freq-mid) > 0.09 {
				t.Errorf("tau %d bin %d: predicted ~%.2f, actual %.3f (n=%d)", tau, i, mid, freq, b.n)
			}
		}
		t.Logf("tau %3ds: Brier %.4f", tau, brier)
	}
}

func TestSigmaTwapEdge(t *testing.T) {
	var pts []Point
	p := 100.0
	r := 1e-4
	for i := range 400 { // 1,000 秒，每 2.5 秒交替 ±r
		pts = append(pts, Point{T: int64(i) * 2500, P: p})
		if i%2 == 0 {
			p *= math.Exp(r)
		} else {
			p *= math.Exp(-r)
		}
	}
	if s := Sigma(pts); math.Abs(s-r/math.Sqrt(2.5)) > 1e-9 {
		t.Fatalf("sigma %v want %v", s, r/math.Sqrt(2.5))
	}
	if s := Sigma(pts[:50]); s != 0 {
		t.Fatalf("too short should be 0: %v", s)
	}
	if m, n := TwapBefore(flat(0, 100, 5), 100, 60); m != 5 || n != 24 {
		t.Fatalf("twap %v %d", m, n)
	}
	// 0.5 的卖价、费率 5%：每股手续费 0.0125
	if e := Edge(0.6, 0.5, 0.05); math.Abs(e-0.0875) > 1e-12 {
		t.Fatalf("edge %v", e)
	}
	if Median([]float64{3, 1, 2}) != 2 || Median([]float64{4, 1, 3, 2}) != 2.5 || Median(nil) != 0 {
		t.Fatal("median")
	}
}
