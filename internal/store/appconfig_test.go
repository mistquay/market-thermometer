package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"market-thermometer/internal/config"
)

func TestAppConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "cfg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	_, _, _, ok, err := st.LoadAppConfig(ctx)
	if err != nil || ok {
		t.Fatalf("expected empty config initially, ok=%v err=%v", ok, err)
	}

	def := config.Default()
	def.Listen = "127.0.0.1:9999"
	def.Polymarket.Strategy.DF = 7
	def.Polymarket.Book.WS = true
	now := time.Now()
	if err := st.SaveAppConfig(ctx, def, "db", now); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAdminPasswordHash(ctx, "$2a$10$dummyhash", now); err != nil {
		t.Fatal(err)
	}

	got, src, updatedAt, ok, err := st.LoadAppConfig(ctx)
	if err != nil || !ok {
		t.Fatalf("load config failed: ok=%v err=%v", ok, err)
	}
	if src != "db" || updatedAt == "" {
		t.Fatalf("unexpected meta: src=%q updatedAt=%q", src, updatedAt)
	}
	if got.Listen != "127.0.0.1:9999" || got.Polymarket.Strategy.DF != 7 || !got.Polymarket.Book.WS {
		t.Fatalf("mismatch config: %+v", got)
	}
	if got.AdminPasswordHash != "$2a$10$dummyhash" {
		t.Fatalf("expected password hash restored, got %q", got.AdminPasswordHash)
	}
}
