package plugin

import (
	"strings"
	"testing"
)

func TestManifestValidate(t *testing.T) {
	ok := Manifest{Name: "csv_source", Kind: KindMetric, Version: "0.1.0",
		Config: []Field{{Key: "path"}}, Metrics: []MetricDef{{ID: "x.demo"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := Manifest{Name: "Bad Name", Kind: "nope", Config: []Field{{Key: "a"}, {Key: "a"}}, Metrics: []MetricDef{{ID: "a b"}}}
	err := bad.Validate()
	for _, want := range []string{"name", "类型", "version", "重复", "指标 id"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
}

func TestRegisterDuplicate(t *testing.T) {
	m := Manifest{Name: "dup_test", Kind: KindMetric, Version: "1"}
	if err := Register(m); err != nil {
		t.Fatal(err)
	}
	if err := Register(m); err == nil {
		t.Fatal("duplicate accepted")
	}
	m.Kind = KindNotifier // 同名不同类型可以并存
	if err := Register(m); err != nil {
		t.Fatal(err)
	}
	if got, ok := Get(KindMetric, "dup_test"); !ok || got.Label != "dup_test" {
		t.Fatalf("get: %+v", got)
	}
}
