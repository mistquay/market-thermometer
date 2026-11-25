package importer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"market-thermometer/internal/store"
)

func TestParseAndImport(t *testing.T) {
	in := "\ufeffcode,date,close,mult\n510300,2026-11-24,4.01,\n510300,20261125,4.05,1\n159915,2026-11-25,2.3,1.02\n"
	bars, err := ParseBars(strings.NewReader(in))
	if err != nil || len(bars) != 2 || bars["510300"][1].Date != "2026-11-25" || bars["510300"][0].Mult != 1 {
		t.Fatalf("parse: %v %v", bars, err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	res, err := ImportBars(ctx, st, bars, "import", time.Now())
	if err != nil || res.Codes != 2 || res.Rows != 3 {
		t.Fatalf("import: %+v %v", res, err)
	}
	list, _ := st.StockList(ctx)
	if len(list) != 2 || list[1].Code != "510300" || list[1].Kind != "fund" || list[1].LastDate != "2026-11-25" {
		t.Fatalf("list: %+v", list)
	}
}

func TestParseErrors(t *testing.T) {
	for in, want := range map[string]string{
		"code,close\n":                                                "date",
		"code,date,close\n123,2026-01-01,1\n":                         "不是沪深",
		"code,date,close\n600000,2026/1/1,1\n":                        "日期",
		"code,date,close\n600000,2026-01-01,0\n":                      "收盘价",
		"code,date,close\n600000,2026-01-01,1\n600000,2026-01-01,2\n": "重复",
	} {
		if _, err := ParseBars(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", in, want, err)
		}
	}
}
