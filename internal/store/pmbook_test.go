package store

import (
	"context"
	"database/sql"
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
		[]PMSpot{{Symbol: "BTCUSDT", T: 1000, P: 60500.5}, {Symbol: "BTCUSDT", T: 3500, P: 60510}, {Symbol: "BTCUSDT", T: 20000, P: 60520}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.PMBooks(ctx, "111", 0, 0, 0)
	if err != nil || len(got) != 2 || got[0].Bids[1] != bids[1] || got[0].Asks[0] != asks[0] || len(got[1].Bids) != 0 {
		t.Fatalf("books %+v %v", got, err)
	}
	if snap, ok, err := st.PMBookAtOrAfter(ctx, "111", 1500, 5000); err != nil || !ok || snap.T != 3000 {
		t.Fatalf("after %+v %v %v", snap, ok, err)
	}
	if snap, ok, err := st.PMBookAtOrBefore(ctx, "111", 2500); err != nil || !ok || snap.T != 1000 {
		t.Fatalf("before %+v %v %v", snap, ok, err)
	}
	if sp, ok, err := st.PMSpotBefore(ctx, "BTCUSDT", 5000); err != nil || !ok || sp.T != 3500 {
		t.Fatalf("spot before %+v %v %v", sp, ok, err)
	}
	h, err := st.PMTickHealthStats(ctx, "BTCUSDT", 0)
	if err != nil || h.Samples != 3 || h.GapsOver10s != 1 || h.MaxGapMs != 16500 {
		t.Fatalf("health %+v %v", h, err)
	}
	if sp, _ := st.PMSpots(ctx, "BTCUSDT", 0, 0); len(sp) != 3 || sp[0].P != 60500.5 {
		t.Fatalf("spots %+v", sp)
	}
	if n, err := st.PrunePMBooks(ctx, 2000); err != nil || n != 1 {
		t.Fatalf("prune %d %v", n, err)
	}
	if x, _ := st.PMBookStatsAll(ctx); x.Books != 1 || x.Tokens != 1 || x.Spots != 2 || x.First != 3000 {
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

func TestLegacyPMBookMigrationToTickDB(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "legacy.db")
	// 模拟旧版本：pm_book / pm_spot 存在于主库
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(mainPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(pmBookSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO pm_book_token(id, token) VALUES(7, 'tokA')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO pm_book(k, t, bids, asks) VALUES(7, 12345, '0.4:10', '0.5:20')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO pm_spot(symbol, t, p) VALUES('BTCUSDT', 12345, 95000.5)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	st, err := Open(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	books, err := st.PMBooks(ctx, "tokA", 0, 0, 10)
	if err != nil || len(books) != 1 || books[0].T != 12345 || books[0].Asks[0].Price != 0.5 {
		t.Fatalf("migrated books %+v %v", books, err)
	}
	spots, err := st.PMSpots(ctx, "BTCUSDT", 0, 0)
	if err != nil || len(spots) != 1 || spots[0].P != 95000.5 {
		t.Fatalf("migrated spots %+v %v", spots, err)
	}
	// 确认主库中旧表已清理
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='pm_book'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("legacy table still exists: %d %v", n, err)
	}
}
