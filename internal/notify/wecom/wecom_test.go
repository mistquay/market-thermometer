package wecom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"market-thermometer/internal/notify"
)

func TestSend(t *testing.T) {
	var got struct {
		MsgType  string            `json:"msgtype"`
		Markdown map[string]string `json:"markdown"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()
	b := &Bot{webhook: srv.URL, http: srv.Client()}
	long := strings.Repeat("温", 2000) // 6000 字节，需截断
	if err := b.Send(context.Background(), notify.Message{Title: "T", Lines: []string{long}}); err != nil {
		t.Fatal(err)
	}
	c := got.Markdown["content"]
	if got.MsgType != "markdown" || len(c) > maxBytes || !utf8.ValidString(c) || !strings.HasPrefix(c, "**T**") {
		t.Fatalf("content: %d bytes, valid=%v", len(c), utf8.ValidString(c))
	}
}

func TestSendBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errcode":93000,"errmsg":"invalid webhook url"}`))
	}))
	defer srv.Close()
	b := &Bot{webhook: srv.URL, http: srv.Client()}
	if err := b.Send(context.Background(), notify.Message{Title: "T"}); err == nil || !strings.Contains(err.Error(), "93000") {
		t.Fatalf("want business error, got %v", err)
	}
}
