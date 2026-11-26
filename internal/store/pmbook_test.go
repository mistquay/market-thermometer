package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
)

func TestPMBookAndPTB(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	k, err := st.PMBookKey(ctx, "111")
	if err != nil {
		t.Fatal(err)
	}
	if k2, _ := st.PMBookKey(ctx, "111"); k2 != k {
		t.Fatalf("key not stable: %d %d", k, k2)
	}
	bids := []ds.BookLevel{{Price: 0.37, Size: 39}, {Price: 0.36, Size: 120.5}}
	asks := []ds.BookLevel{{Price: 0.38, Size: 276.65}}
	if err := st.SavePMSnapshot(ctx, []PMBookRow{{Key: k, T: 1000, Bids: bids, Asks: asks}, {Key: k, T: 3000, Bids: nil, Asks: asks}},
		[]PMSpot{{Symbol: "BTCUSDT", T: 1000, P: 60500.5}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.PMBooks(ctx, "111", 0, 0, 0)
	if err != nil || len(got) != 2 || got[0].Bids[1] != bids[1] || got[0].Asks[0] != asks[0] || len(got[1].Bids) != 0 {
		t.Fatalf("books %+v %v", got, err)
	}
	if sp, _ := st.PMSpots(ctx, "BTCUSDT", 0, 0); len(sp) != 1 || sp[0].P != 60500.5 {
		t.Fatalf("spots %+v", sp)
	}
	if n, err := st.PrunePMBooks(ctx, 2000); err != nil || n != 1 {
		t.Fatalf("prune %d %v", n, err)
	}
	if x, _ := st.PMBookStatsAll(ctx); x.Books != 1 || x.Tokens != 1 || x.Spots != 0 || x.First != 3000 {
		t.Fatalf("stats %+v", x)
	}

	// 起始价：事件结束后才给；后来的刷新没给（0）时保留
	ev := ds.PMEvent{ID: "1", Slug: "btc-updown-5m-1", Title: "x", Series: "s", Closed: true, PriceToBeat: 60496.4}
	now := time.Now()
	if err := st.SavePMEvents(ctx, []ds.PMEvent{ev}, now); err != nil {
		t.Fatal(err)
	}
	ev.PriceToBeat = 0
	if err := st.SavePMEvents(ctx, []ds.PMEvent{ev}, now); err != nil {
		t.Fatal(err)
	}
	if e, err := st.PMEventBySlug(ctx, ev.Slug); err != nil || e.PriceToBeat != 60496.4 {
		t.Fatalf("ptb %+v %v", e, err)
	}
}
