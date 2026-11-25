package quality

import (
	"reflect"
	"testing"
)

var cal = []string{"2026-06-22", "2026-06-23", "2026-06-24", "2026-06-25", "2026-06-26", "2026-06-29", "2026-06-30",
	"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06"}

func TestGaps600000(t *testing.T) {
	// 回归用例：600000 本地缺 2026-06-25 ~ 07-02（6 个交易日）
	dates := []string{"2026-06-22", "2026-06-23", "2026-06-24", "2026-07-03", "2026-07-06"}
	got := Gaps(cal, dates)
	want := []Run{{From: "2026-06-25", To: "2026-07-02", Days: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %+v", got)
	}
	// 首日之前、末日之后不算缺口（上市前 / 尚未同步）
	if g := Gaps(cal, []string{"2026-06-24", "2026-06-25"}); len(g) != 0 {
		t.Fatalf("edges: %+v", g)
	}
	// 多段
	g := Gaps(cal, []string{"2026-06-22", "2026-06-24", "2026-06-25", "2026-07-01", "2026-07-06"})
	if len(g) != 3 || g[0] != (Run{"2026-06-23", "2026-06-23", 1}) || g[1].Days != 3 || g[2] != (Run{"2026-07-02", "2026-07-03", 2}) {
		t.Fatalf("multi: %+v", g)
	}
}

func TestCoverage(t *testing.T) {
	counts := map[string]int{"2026-06-22": 100, "2026-06-23": 101, "2026-06-24": 60, "2026-06-25": 99, "2026-06-26": 3}
	got := Coverage(cal, counts, "2026-06-25", 0.95)
	if len(got) != 4 || got[2].Low != true || got[0].Low || got[3].Low {
		t.Fatalf("coverage: %+v", got)
	}
}

func TestBehind(t *testing.T) {
	for _, c := range []struct {
		last, expected string
		want           int
	}{
		{"2026-07-06", "2026-07-06", 0},
		{"2026-07-02", "2026-07-06", 2},
		{"2026-06-27", "2026-06-30", 2}, // 周六：落后 06-29、06-30
		{"", "2026-07-06", -1},
	} {
		if got := Behind(cal, c.last, c.expected); got != c.want {
			t.Errorf("Behind(%s, %s) = %d, want %d", c.last, c.expected, got, c.want)
		}
	}
}

func TestCompareBars(t *testing.T) {
	local := []Bar{{"2026-06-22", 10, 1}, {"2026-06-23", 10.1, 1}, {"2026-06-24", 10.2, 1}, {"2026-07-03", 10.5, 1}, {"2026-07-06", 10.6, 1}}
	src := []Bar{{"2026-06-22", 10, 1}, {"2026-06-23", 10.1, 1.05}, {"2026-06-24", 10.2, 1}, {"2026-06-25", 10.3, 1},
		{"2026-07-03", 10.5, 1}, {"2026-07-06", 10.7, 1}}
	n, diffs := CompareBars("600000", local, src, "2026-07-06")
	if n != 5 || len(diffs) != 3 {
		t.Fatalf("n=%d diffs=%+v", n, diffs)
	}
	if diffs[0].Kind != "mult" || diffs[1].Kind != "only_source" || diffs[1].Date != "2026-06-25" ||
		diffs[2].Kind != "close" || !diffs[2].Recent || diffs[0].Recent {
		t.Fatalf("diffs=%+v", diffs)
	}
	// 来源区间之外的本地数据不算 only_local
	if _, d := CompareBars("x", local, src[2:5], "9999"); len(d) != 1 || d[0].Kind != "only_source" {
		t.Fatalf("range: %+v", d)
	}
}
