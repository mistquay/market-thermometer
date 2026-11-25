package job

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/market"
	"market-thermometer/internal/store"
)

// srcStocks 是一个返回固定日线的数据源；fail 时报错。
type srcStocks struct {
	name string
	bars map[string][]ds.StockBar
	fail bool
}

func (s *srcStocks) Name() string                                   { return s.name }
func (s *srcStocks) ListStocks(context.Context) ([]ds.Stock, error) { return nil, ds.ErrNotSupported }
func (s *srcStocks) Bars(_ context.Context, code, from, _ string) ([]ds.StockBar, error) {
	if s.fail {
		return nil, errors.New("blocked")
	}
	var out []ds.StockBar
	for _, b := range s.bars[code] {
		if b.Date >= from {
			out = append(out, b)
		}
	}
	return out, nil
}

func TestQualityAndCrossCheck(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 10, 20, 0, 0, 0, ds.Shanghai)
	cal := []string{"2026-06-22", "2026-06-23", "2026-06-24", "2026-06-25", "2026-06-26", "2026-06-29", "2026-06-30",
		"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"}
	p, err := st.ActiveParams(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	var days []engine.Day
	for _, d := range cal {
		days = append(days, engine.Day{Date: d})
	}
	if err := st.ReplaceScores(ctx, market.CN.ID, p.Version(), days); err != nil {
		t.Fatal(err)
	}
	full := func(skip func(string) bool, bump float64) []ds.StockBar {
		var out []ds.StockBar
		for i, d := range cal {
			if !skip(d) {
				out = append(out, ds.StockBar{Date: d, Close: 10 + float64(i)/10 + bump*float64(i/14), Mult: 1})
			}
		}
		return out
	}
	none := func(string) bool { return false }
	gap := func(d string) bool { return d >= "2026-06-25" && d <= "2026-07-02" }
	if err := st.UpsertStockList(ctx, []ds.Stock{{Code: "600000", Kind: ds.KindStock}, {Code: "000001", Kind: ds.KindStock}}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveStockBars(ctx, "stockdb", map[string][]ds.StockBar{"600000": full(gap, 0), "000001": full(none, 0)}); err != nil {
		t.Fatal(err)
	}
	hub, err := ds.NewHub(ds.Env{}, []string{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Quality(ctx, st, hub, market.CN, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.GapStocks != 1 || rep.GapDays != 6 || rep.Gaps[0].Code != "600000" || rep.Gaps[0].Runs[0].From != "2026-06-25" {
		t.Fatalf("gaps: %+v", rep)
	}
	var low []string
	for _, c := range rep.Coverage {
		if c.Low {
			low = append(low, c.Date)
		}
	}
	if len(low) != 6 { // 缺口期间只有 1 只股票有日线
		t.Fatalf("coverage low: %v", low)
	}
	for _, f := range rep.Freshness {
		if f.ID == "stock_bar:stock" && (f.Last != "2026-07-10" || f.Behind != 0) {
			t.Fatalf("freshness: %+v", f)
		}
	}

	// 交叉核对：来源 a 有完整日线（补出 600000 的缺口），最后一天收盘价不同（近期）；来源 b 被封
	a := &srcStocks{name: "a", bars: map[string][]ds.StockBar{"600000": full(none, 0), "000001": full(none, 0.5)}}
	b := &srcStocks{name: "b", fail: true}
	r := NewRunner(st, hub, market.CN, now)
	r.now = func() time.Time { return now }
	r.SetStocks([]ds.StockSource{a, b}, 1)
	if _, err := r.begin("crosscheck"); err != nil {
		t.Fatal(err)
	}
	if err := r.crossCheck(ctx, []string{"600000", "000001"}); err != nil {
		t.Fatal(err)
	}
	var res CrossCheckResult
	if ok, err := st.LoadJSON(ctx, SettingCrossCheck, &res); !ok || err != nil {
		t.Fatalf("load: %v %v", ok, err)
	}
	sa, sb := res.Sources[0], res.Sources[1]
	// 近期 = 最后 10 个交易日（06-29 起）：缺口中 06-25、06-26 计入 OnlySource，其余 4 天与 07-10 的收盘价差异计入 Recent
	if sa.Codes != 2 || sa.OnlySource != 2 || sa.Close != 0 || sa.Recent != 5 {
		t.Fatalf("source a: %+v", sa)
	}
	if sb.Codes != 0 || len(sb.Errors) != 2 {
		t.Fatalf("source b: %+v", sb)
	}
	if res.Diffs[0].Src != "a" {
		t.Fatalf("diff src: %+v", res.Diffs[0])
	}
}
