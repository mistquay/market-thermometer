package job

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/store"
)

func TestPMShadow(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t0 := int64(1_795_680_000) // 进行中市场的开始时间（Unix 秒）
	const basis = 0.0013
	const k = 60000.0
	// 已结算事件（用来校准基差）：起始价 k，开始前 60 秒的币安价 = k × (1 + basis)
	var evs []ds.PMEvent
	for i := int64(1); i <= 6; i++ {
		s := t0 - 300*i
		evs = append(evs, ds.PMEvent{ID: fmt.Sprint(i), Slug: fmt.Sprintf("btc-updown-5m-%d", s), Title: "x", Series: "btc-up-or-down-5m", Closed: true,
			PriceToBeat: k, End: time.Unix(s+300, 0), Markets: []ds.PMMarket{{ConditionID: fmt.Sprintf("c%d", i), Start: time.Unix(s, 0), End: time.Unix(s+300, 0), Winner: 0}}})
	}
	if err := st.SavePMEvents(ctx, evs, time.Now()); err != nil {
		t.Fatal(err)
	}
	// 现价：t0 − 1 小时到 t0 + 120 秒，每 2.5 秒；开始前在 k×(1+basis) 附近 ±1bp 交替，开始后上涨 20bp
	now := time.Unix(t0+120, 0)
	var spots []store.PMSpot
	for ms := (t0 - 3600) * 1000; ms <= now.UnixMilli(); ms += 2500 {
		p := k * (1 + basis)
		if ms >= t0*1000 {
			p *= 1.002
		}
		if (ms/2500)%2 == 0 {
			p *= 1.0001
		} else {
			p *= 0.9999
		}
		spots = append(spots, store.PMSpot{Symbol: "BTCUSDT", T: ms, P: p})
	}
	if err := st.SavePMSnapshot(ctx, nil, spots); err != nil {
		t.Fatal(err)
	}
	sh := NewPMShadow(st, 0.03)
	sh.logf = func(string, ...any) {}
	target := PMBookTarget{Series: "btc-up-or-down-5m", Slug: fmt.Sprintf("btc-updown-5m-%d", t0), Token: "up", Symbol: "BTCUSDT",
		Start: time.Unix(t0, 0), End: time.Unix(t0+300, 0), Cond: "live", Fee: 0.05}
	next := target
	next.Slug, next.Token, next.Cond, next.Start, next.End = "next", "up2", "live2", time.Unix(t0+300, 0), time.Unix(t0+600, 0)
	books := []ds.Book{{Token: "up", Bids: []ds.BookLevel{{Price: 0.54, Size: 100}}, Asks: []ds.BookLevel{{Price: 0.55, Size: 80}}}}
	sh.Tick(ctx, now, []PMBookTarget{target, next}, books, nil)

	s := sh.Status()
	if s.LastErr != "" {
		t.Fatal(s.LastErr)
	}
	if math.Abs(s.Basis["btc"]-basis) > 2e-5 || s.BasisN["btc"] != 6 {
		t.Fatalf("basis %v n %v", s.Basis, s.BasisN)
	}
	e := s.Evals[0]
	if !e.OK || e.KSrc != "spot" || math.Abs(e.K/k-1) > 3e-5 || e.P < 0.8 || e.Tau != 180 {
		t.Fatalf("eval %+v", e)
	}
	if s.Evals[1].OK || s.Evals[1].Reason != "未开始" {
		t.Fatalf("next %+v", s.Evals[1])
	}
	// Up 卖一 0.55 远低于模型概率：记录一条 Up 信号；Down（卖一 0.46）不记录
	sigs, err := st.PMSignals(ctx, 0, 10)
	if err != nil || len(sigs) != 1 || sigs[0].Outcome != 0 || sigs[0].Ask != 0.55 || sigs[0].AskSize != 80 || sigs[0].KSrc != "spot" {
		t.Fatalf("signals %+v %v", sigs, err)
	}
	// 冷却期内不重复记录
	sh.Tick(ctx, now.Add(2500*time.Millisecond), []PMBookTarget{target}, books, nil)
	if sigs, _ := st.PMSignals(ctx, 0, 10); len(sigs) != 1 {
		t.Fatalf("cooldown %d", len(sigs))
	}
	// 同一时刻有已结算的起始价时用精确值
	exact := ds.PMEvent{ID: "x", Slug: fmt.Sprintf("btc-updown-15m-%d", t0), Title: "x", Series: "btc-up-or-down-15m", Closed: true, PriceToBeat: 60010,
		Markets: []ds.PMMarket{{ConditionID: "cx", Start: time.Unix(t0, 0), End: time.Unix(t0+900, 0), Winner: 0}}}
	if err := st.SavePMEvents(ctx, []ds.PMEvent{exact}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sh.Tick(ctx, now.Add(40*time.Second), []PMBookTarget{target}, books, nil)
	if e := sh.Status().Evals[0]; e.KSrc != "exact" || e.K != 60010 {
		t.Fatalf("exact %+v", e)
	}
}
