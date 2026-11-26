package job

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/store"
)

type fakeKlines struct {
	name  string
	fail  bool
	calls []int64 // 请求的 from
}

func (f *fakeKlines) Name() string { return f.name }
func (f *fakeKlines) Klines(_ context.Context, _, iv string, from, to int64) ([]ds.Kline, error) {
	f.calls = append(f.calls, from)
	if f.fail {
		return nil, errors.New("down")
	}
	d := ds.KlineIntervals[iv].Milliseconds()
	var out []ds.Kline
	for t := from; t+d <= to; t += d {
		out = append(out, ds.Kline{T: t, Open: 1, High: 2, Low: 1, Close: float64(t / d % 100), Volume: 1})
	}
	return out, nil
}
func (f *fakeKlines) Price(context.Context, string) (float64, error) {
	if f.fail {
		return 0, errors.New("down")
	}
	return 42, nil
}

func TestCryptoSync(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	down, ok := &fakeKlines{name: "a", fail: true}, &fakeKlines{name: "b"}
	now := time.Date(2026, 11, 26, 6, 30, 0, 0, time.UTC)
	c := NewCryptoSyncer(st, []ds.KlineSource{down, ok}, []string{"BTCUSDT"}, map[string]int{"1d": 10, "1h": 2}, time.Minute)
	c.now = func() time.Time { return now }
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Klines(ctx, "BTCUSDT", "1d", 0, 0); len(got) != 10 {
		t.Fatalf("1d bars %d", len(got))
	}
	if got, _ := st.Klines(ctx, "BTCUSDT", "1h", 0, 0); len(got) != 48 {
		t.Fatalf("1h bars %d", len(got))
	}
	// 第二次：从最后一根往回 2 根开始
	now = now.Add(3 * time.Hour)
	ok.calls = nil
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	last, _ := st.KlineLast(ctx, "BTCUSDT", "1h")
	if want := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC).UnixMilli(); last != want {
		t.Fatalf("last %v want %v", time.UnixMilli(last).UTC(), time.UnixMilli(want).UTC())
	}
	if p, src, err := c.Price(ctx, "BTCUSDT"); p != 42 || src != "b" || err != nil {
		t.Fatalf("price %v %s %v", p, src, err)
	}
	if st := c.Status(); st.Errors != 0 || len(st.Log) == 0 {
		t.Fatalf("status %+v", st)
	}
}
