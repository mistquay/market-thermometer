package datasource

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	name string
	ms   []MetricID
	err  error
	pts  []Point
}

func (f *fakeSource) Name() string         { return f.name }
func (f *fakeSource) Provides() []MetricID { return f.ms }
func (f *fakeSource) Fetch(context.Context, MetricID, time.Time, time.Time) ([]Point, error) {
	return f.pts, f.err
}

func init() {
	Register("test-broken", func(Env) (Source, error) {
		return &fakeSource{name: "test-broken", ms: []MetricID{"test.m"}, err: errors.New("boom")}, nil
	})
	Register("test-ok", func(Env) (Source, error) {
		return &fakeSource{name: "test-ok", ms: []MetricID{"test.m"}, pts: []Point{{Date: "2026-11-24", Value: 1}}}, nil
	})
}

func TestHubFallback(t *testing.T) {
	h, err := NewHub(Env{}, []string{"test-broken", "test-ok"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := h.Route("test.m"); len(r) != 2 || r[0] != "test-broken" {
		t.Fatalf("default route %v", r)
	}
	src, pts, err := h.Fetch(context.Background(), "test.m", time.Time{}, time.Time{})
	if err != nil || src != "test-ok" || len(pts) != 1 {
		t.Fatalf("src=%s pts=%v err=%v", src, pts, err)
	}
}

func TestHubExplicitRouteAndAllFail(t *testing.T) {
	h, err := NewHub(Env{}, []string{"test-broken", "test-ok"}, map[string][]string{"test.m": {"test-broken"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.Fetch(context.Background(), "test.m", time.Time{}, time.Time{}); err == nil {
		t.Fatal("want error when every routed source fails")
	}
	if _, _, err := h.Fetch(context.Background(), "test.none", time.Time{}, time.Time{}); err == nil {
		t.Fatal("want error for unrouted metric")
	}
}

func TestHubRejectsUnknownRouteSource(t *testing.T) {
	if _, err := NewHub(Env{}, []string{"test-ok"}, map[string][]string{"test.m": {"test-broken"}}, nil); err == nil {
		t.Fatal("route to a disabled plugin must be rejected")
	}
}

func TestLatestFinalDate(t *testing.T) {
	cases := map[string]string{
		"2026-11-25T10:53:00+08:00": "2026-11-24", // 盘中：当日 bar 未定稿
		"2026-11-25T15:29:00+08:00": "2026-11-24",
		"2026-11-25T15:30:00+08:00": "2026-11-25",
		"2026-11-25T01:00:00Z":      "2026-11-24", // UTC 01:00 = 北京 09:00
	}
	for in, want := range cases {
		now, _ := time.Parse(time.RFC3339, in)
		if got := LatestFinalDate(now); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
	now, _ := time.Parse(time.RFC3339, "2026-11-25T10:53:00+08:00")
	pts := ClipFinal([]Point{{Date: "2026-11-24"}, {Date: "2026-11-25"}}, now)
	if len(pts) != 1 || pts[0].Date != "2026-11-24" {
		t.Fatalf("ClipFinal %v", pts)
	}
}

func TestHubEmptyRouteDisablesMetric(t *testing.T) {
	h, err := NewHub(Env{}, []string{"test-ok"}, map[string][]string{"test.m": {}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := h.Route("test.m"); len(r) != 0 {
		t.Fatalf("explicit empty route must disable the metric, got %v", r)
	}
}
