package tdx

import (
	"math"
	"testing"
	"time"

	"github.com/bensema/gotdx/proto"
)

func bar(d string, close, vol float64) proto.SecurityBar {
	t, _ := time.Parse("2006-01-02", d)
	return proto.SecurityBar{DateTime: t, Open: close, High: close, Low: close, Close: close, Vol: vol, Amount: close * vol}
}

func p32(x float32) *float32 { return &x }

func TestEventMult(t *testing.T) {
	// 600000 2026-07-16：前收 9.31，每 10 股派 4.2 元 → 9.31 / (9.31 − 0.42)；stockdb 记为 1.047
	if m := (event{fenhong: 4.2}).mult(9.31); math.Abs(m-1.047244) > 1e-6 {
		t.Fatalf("dividend mult %v", m)
	}
	// 10 送 8 派 1.5：参考价 = (10 − 0.15) / 1.8
	if m := (event{fenhong: 1.5, songzhuan: 8}).mult(10); math.Abs(m-10/(9.85/1.8)) > 1e-9 {
		t.Fatalf("bonus mult %v", m)
	}
	// 10 配 3、配股价 5：参考价 = (10 + 5 × 0.3) / 1.3
	if m := (event{peigujia: 5, peigu: 3}).mult(10); math.Abs(m-10/(11.5/1.3)) > 1e-9 {
		t.Fatalf("rights mult %v", m)
	}
	if m := (event{suogu: 1.1455878}).mult(3); m != 1.1455878 {
		t.Fatalf("split mult %v", m)
	}
}

func TestBuild(t *testing.T) {
	raw := []proto.SecurityBar{
		bar("2026-07-16", 8.9, 100), bar("2026-07-14", 9.2, 100), bar("2026-07-15", 9.31, 100),
		bar("2026-07-17", 9.0, 0),                                // 停牌（无成交）：去掉
		bar("2026-07-20", 9.1, 100), bar("2026-07-20", 9.1, 100), // 重复：去掉
	}
	evs := events([]proto.XDXRItem{
		{Date: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC), Category: 1, Fenhong: p32(4.2)},
		{Date: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC), Category: 1, Fenhong: p32(1)}, // 非交易日除权：计入下一个交易日 07-20
		{Date: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Category: 11, Suogu: p32(2)},   // 第一根之前：忽略
		{Date: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), Category: 5},                  // 股本变化：不影响价格
	})
	bars, ext := build(raw, evs)
	if len(bars) != 4 || len(ext) != 4 || bars[0].Date != "2026-07-14" || bars[3].Date != "2026-07-20" {
		t.Fatalf("bars %+v", bars)
	}
	if bars[0].Mult != 1 || bars[1].Mult != 1 || bars[2].Mult != 1.047244 {
		t.Fatalf("mult %+v", bars)
	}
	if want := round6(8.9 / (8.9 - 0.1)); bars[3].Mult != want {
		t.Fatalf("ex-date on holiday: %v want %v", bars[3].Mult, want)
	}
	if ext[2].Volume != 100 || ext[2].Open != 8.9 {
		t.Fatalf("ext %+v", ext[2])
	}
}

func TestOptString(t *testing.T) {
	if got := optString([]any{"1.1.1.1:7709", "2.2.2.2:7709"}); got != "1.1.1.1:7709,2.2.2.2:7709" {
		t.Fatal(got)
	}
}
