package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/engine"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPointsMultiSource(t *testing.T) {
	s, ctx, now := openTemp(t), context.Background(), time.Now()
	m := ds.MetricID("cn.hs300.close")
	if _, err := s.UpsertPoints(ctx, m, "a", []ds.Point{{Date: "2026-11-20", Value: 1}, {Date: "2026-11-23", Value: 2}}, now); err != nil {
		t.Fatal(err)
	}
	// 覆盖写 + 另一来源并存
	if _, err := s.UpsertPoints(ctx, m, "a", []ds.Point{{Date: "2026-11-23", Value: 3}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertPoints(ctx, m, "b", []ds.Point{{Date: "2026-11-24", Value: 9}}, now); err != nil {
		t.Fatal(err)
	}
	pts, err := s.Series(ctx, m, "a", "", "")
	if err != nil || len(pts) != 2 || pts[1].Value != 3 {
		t.Fatalf("series a: %v %v", pts, err)
	}
	if last, _ := s.LastDate(ctx, m); last != "2026-11-24" {
		t.Fatalf("last %s", last)
	}
	cov, _ := s.Coverage(ctx)
	if len(cov) != 2 || cov[0].Source != "a" || cov[0].Count != 2 || cov[0].First != "2026-11-20" {
		t.Fatalf("coverage %+v", cov)
	}
	if last, _ := s.LastDate(ctx, "none"); last != "" {
		t.Fatalf("empty metric last=%q", last)
	}
}

func TestSessions(t *testing.T) {
	s, ctx, now := openTemp(t), context.Background(), time.Now()
	if err := s.CreateSession(ctx, "tok", now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.ValidSession(ctx, "tok", now); !ok {
		t.Fatal("session should be valid")
	}
	if ok, _ := s.ValidSession(ctx, "tok", now.Add(2*time.Hour)); ok {
		t.Fatal("session should be expired")
	}
	if ok, _ := s.ValidSession(ctx, "other", now); ok {
		t.Fatal("unknown token accepted")
	}
	_ = s.DeleteSession(ctx, "tok")
	if ok, _ := s.ValidSession(ctx, "tok", now); ok {
		t.Fatal("deleted session accepted")
	}
}

func TestJobs(t *testing.T) {
	s, ctx, now := openTemp(t), context.Background(), time.Now()
	id, err := s.JobStart(ctx, "sync", now)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.FailStaleJobs(ctx, now)
	jobs, _ := s.RecentJobs(ctx, 5)
	if len(jobs) != 1 || jobs[0].ID != id || jobs[0].Status != "interrupted" {
		t.Fatalf("jobs %+v", jobs)
	}
}

func TestScoresRoundTripWithNaN(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	nan := math.NaN()
	days := []engine.Day{
		{Date: "2026-11-23", Close: 3958, ERP: nan, V: nan, T: [4]float64{nan, nan, nan, nan}, TBase: nan, TPct: nan, TS: nan,
			Heat: nan, Leverage: nan, RSID: nan, RSIW: nan, RMix: nan},
		{Date: "2026-11-24", Close: 3963, ERP: 0.05, V: 22, Tier: engine.TierCheap, T: [4]float64{10, 20, nan, 30},
			TBase: 20, TPct: 15, TS: 18, NLow: 2, Degraded: true, Heat: 1.8, Leverage: 7, RSID: 40, RSIW: 45, RMix: 43},
	}
	if err := s.ReplaceScores(ctx, "CN", "v1", days); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceScores(ctx, "CN", "v1", days[1:]); err != nil { // 整体替换
		t.Fatal(err)
	}
	got, err := s.Scores(ctx, "CN", "v1", "", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v %v", got, err)
	}
	d := got[0]
	if d.TS != 18 || !math.IsNaN(d.T[2]) || !d.Degraded || d.Tier != engine.TierCheap || d.NLow != 2 {
		t.Fatalf("round trip %+v", d)
	}
	p, err := s.ActiveParams(ctx, time.Now())
	if err != nil || p.Version() != engine.DefaultParams().Version() {
		t.Fatalf("active params %v %v", p, err)
	}
}
