package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestNotifyChannels(t *testing.T) {
	s, ctx := openTemp(t), context.Background()
	id, err := s.SaveNotifyChannel(ctx, NotifyChannel{Kind: "dingtalk", Name: "群", Config: map[string]string{"webhook": "w", "secret": "s"}, Enabled: true})
	if err != nil || id == 0 {
		t.Fatalf("insert: %d %v", id, err)
	}
	if _, err := s.SaveNotifyChannel(ctx, NotifyChannel{ID: id, Kind: "dingtalk", Name: "群2", Config: map[string]string{"webhook": "w2"}}); err != nil {
		t.Fatal(err)
	}
	c, err := s.NotifyChannel(ctx, id)
	if err != nil || c.Name != "群2" || c.Config["webhook"] != "w2" || c.Enabled {
		t.Fatalf("get: %+v %v", c, err)
	}
	if _, err := s.SaveNotifyChannel(ctx, NotifyChannel{ID: 999, Kind: "x", Config: map[string]string{}}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("update missing: %v", err)
	}
	if err := s.DeleteNotifyChannel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if list, err := s.NotifyChannels(ctx); err != nil || len(list) != 0 {
		t.Fatalf("list after delete: %v %v", list, err)
	}
}

func TestNotifyLogAndSettings(t *testing.T) {
	s, ctx, now := openTemp(t), context.Background(), time.Now()
	if sent, err := s.NotifySent(ctx, "k"); err != nil || sent {
		t.Fatalf("sent before log: %v %v", sent, err)
	}
	if err := s.LogNotify(ctx, NotifyLog{Key: "k", Title: "t", Result: "ok"}, now); err != nil {
		t.Fatal(err)
	}
	if sent, _ := s.NotifySent(ctx, "k"); !sent {
		t.Fatal("not marked sent")
	}
	if logs, err := s.RecentNotifyLogs(ctx, 10); err != nil || len(logs) != 1 || logs[0].Title != "t" {
		t.Fatalf("logs: %v %v", logs, err)
	}
	if v, err := s.Setting(ctx, "notify.signals"); err != nil || v != "" {
		t.Fatalf("default setting: %q %v", v, err)
	}
	if err := s.SetSetting(ctx, "notify.signals", "on"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Setting(ctx, "notify.signals"); v != "on" {
		t.Fatalf("setting: %q", v)
	}
}
