package store

import (
	"context"
	"testing"
	"time"
)

func TestResearchNotes(t *testing.T) {
	s, ctx, now := openTemp(t), context.Background(), time.Now()
	id, err := s.SaveNote(ctx, ResearchNote{Target: "stock:600519", Title: "估值观察", Content: "PE 处于历史中枢下方", Tags: "白酒,估值"}, now)
	if err != nil || id <= 0 {
		t.Fatalf("save %d %v", id, err)
	}
	if _, err := s.SaveNote(ctx, ResearchNote{ID: id, Target: "stock:600519", Title: "估值观察 v2", Content: "补充现金流记录"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Notes(ctx, "stock:600519", 10)
	if err != nil || len(got) != 1 || got[0].Title != "估值观察 v2" {
		t.Fatalf("notes %+v %v", got, err)
	}
	if err := s.DeleteNote(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Notes(ctx, "", 10); len(got) != 0 {
		t.Fatalf("after delete %+v", got)
	}
}
