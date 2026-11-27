package store

import (
	"context"
	"testing"
	"time"
)

func TestAuditLog(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	now := time.Now()
	if err := s.AddAudit(ctx, "web", "pm.halt", "global", map[string]any{"on": true}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAudit(ctx, "web", "params.save", "v1", map[string]any{"activate": true}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	all, err := s.RecentAudits(ctx, "", 10)
	if err != nil || len(all) != 2 || all[0].Action != "params.save" || all[1].Detail["on"] != true {
		t.Fatalf("all audits %+v %v", all, err)
	}
	pmOnly, err := s.RecentAudits(ctx, "pm", 10)
	if err != nil || len(pmOnly) != 1 || pmOnly[0].Action != "pm.halt" {
		t.Fatalf("pm audits %+v %v", pmOnly, err)
	}
}
