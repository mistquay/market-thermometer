package pmeval

import (
	"math"
	"testing"

	ds "market-thermometer/internal/datasource"
)

func TestEvaluateAndReplay(t *testing.T) {
	baseT := int64(1700000000_000)
	sigs := []Signal{
		// 市场 1：两条信号（去重应只留第一条），exact，赢
		{ID: 1, T: baseT, Slug: "btc-updown-5m-1", Series: "btc-up-or-down-5m", Cond: "c1", Token0: "t1", Outcome: 0, EndT: baseT/1000 + 300,
			P: 0.70, Ask: 0.55, FeeRate: 0.05, Edge: 0.13, KSrc: "exact", Tau: 120, Winner: 0, MinSize: 5},
		{ID: 2, T: baseT + 30_000, Slug: "btc-updown-5m-1", Series: "btc-up-or-down-5m", Cond: "c1", Token0: "t1", Outcome: 0, EndT: baseT/1000 + 300,
			P: 0.72, Ask: 0.56, FeeRate: 0.05, Edge: 0.14, KSrc: "exact", Tau: 90, Winner: 0, MinSize: 5},
		// 市场 2：spot，输
		{ID: 3, T: baseT + 600_000, Slug: "eth-updown-5m-2", Series: "eth-up-or-down-5m", Cond: "c2", Token0: "t2", Outcome: 1, EndT: (baseT+600_000)/1000 + 300,
			P: 0.65, Ask: 0.50, FeeRate: 0.01, Edge: 0.14, KSrc: "spot", Tau: 45, Winner: 0, MinSize: 5},
		// 市场 3：心跳超时应跳过
		{ID: 4, T: baseT + 1_200_000, Slug: "btc-updown-5m-3", Series: "btc-up-or-down-5m", Cond: "c3", Token0: "t3", Outcome: 0, EndT: (baseT+1_200_000)/1000 + 300,
			P: 0.68, Ask: 0.52, FeeRate: 0.05, Edge: 0.14, KSrc: "exact", Tau: 200, Winner: 0, MinSize: 5},
	}
	feed := &Feed{
		LastSpotT: func(sym string, atMs int64) (int64, bool, error) {
			if atMs >= baseT+1_200_000 {
				return atMs - 30_000, true, nil // 心跳滞后 30 秒 → 超时
			}
			return atMs - 1000, true, nil
		},
		NextBook: func(tok string, atMs, endMs int64) (ds.Book, bool, error) {
			switch tok {
			case "t1":
				return ds.Book{Asks: []ds.BookLevel{{Price: 0.56, Size: 100}}}, true, nil
			case "t2":
				// Outcome=1（Down）吃 Up 的 Bids 镜像：1 - 0.50 = 0.50
				return ds.Book{Bids: []ds.BookLevel{{Price: 0.50, Size: 100}}}, true, nil
			}
			return ds.Book{}, false, nil
		},
	}

	cfg := DefaultConfig()
	cfg.OrderUSD = 20
	rep := Evaluate(sigs, cfg, feed)

	if rep.RawSignals != 4 || rep.FilteredSignals != 3 || rep.UniqueMarkets != 3 {
		t.Fatalf("counts %+v", rep)
	}
	if rep.Overall.Resolved != 3 || rep.Overall.Wins != 2 {
		t.Fatalf("overall %+v", rep.Overall)
	}
	if rep.Replay.Executed != 2 || rep.Replay.Wins != 1 || rep.Replay.Losses != 1 || rep.Replay.SkippedStale != 1 {
		t.Fatalf("replay %+v", rep.Replay)
	}
	if math.Abs(rep.Replay.AvgSlippage-0.005) > 1e-6 {
		t.Fatalf("avg slippage=%f want 0.005", rep.Replay.AvgSlippage)
	}
	if len(rep.Replay.Curve) != 2 {
		t.Fatalf("curve len=%d", len(rep.Replay.Curve))
	}
}
