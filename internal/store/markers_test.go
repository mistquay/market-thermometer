package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
)

func TestPMSignalsBySlugAndOrders(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Unix(1730000000, 0).UTC()
	if err := st.SavePMEvents(ctx, []ds.PMEvent{{
		ID: "ev-1", Slug: "btc-updown-5m-1", Title: "BTC 5m", Series: "btc-up-or-down-5m",
		Start: now, End: now.Add(5 * time.Minute), Closed: true,
		Markets: []ds.PMMarket{{
			ConditionID: "cond-1", Start: now, End: now.Add(5 * time.Minute),
			Closed: true, Winner: 0,
		}},
	}}, now); err != nil {
		t.Fatal(err)
	}

	if err := st.AddPMSignals(ctx, []PMSignal{{
		T: now.UnixMilli(), Strategy: "updown-fair", Slug: "btc-updown-5m-1", Cond: "cond-1",
		Outcome: 0, P: 0.62, Ask: 0.54, AskSize: 100, FeeRate: 0.05, Edge: 0.067,
		K: 90000, KSrc: "exact", SdBp: 18.5, Tau: 180, Spot: 90120, Basis: 0.0001,
	}}); err != nil {
		t.Fatal(err)
	}

	sigs, err := st.PMSignalsBySlug(ctx, "btc-updown-5m-1")
	if err != nil {
		t.Fatalf("PMSignalsBySlug failed: %v", err)
	}
	if len(sigs) != 1 || sigs[0].Winner != 0 || sigs[0].Cond != "cond-1" {
		t.Fatalf("unexpected signals: %+v", sigs)
	}

	orders, err := st.PMOrdersBySlug(ctx, "btc-updown-5m-1")
	if err != nil {
		t.Fatalf("PMOrdersBySlug failed: %v", err)
	}
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders, got %d", len(orders))
	}

	fills, err := st.SimFillsByCode(ctx, "600519")
	if err != nil {
		t.Fatalf("SimFillsByCode failed: %v", err)
	}
	if len(fills) != 0 {
		t.Fatalf("expected 0 fills, got %d", len(fills))
	}
}
