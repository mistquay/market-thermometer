package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"market-thermometer/internal/notify"
)

func TestSend(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:ABC/sendMessage" {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()
	b := &Bot{base: srv.URL, token: "123:ABC", chatID: "42", http: srv.Client()}
	if err := b.Send(context.Background(), notify.Message{Title: "T", Lines: []string{"**a**"}}); err != nil {
		t.Fatal(err)
	}
	if got["chat_id"] != "42" || got["text"] != "T\na" {
		t.Fatalf("body: %v", got)
	}
}

type failRT struct{}

func (failRT) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: i/o timeout")
}

func TestErrorHidesToken(t *testing.T) {
	// 连接失败时 url.Error 会带上含 token 的完整 URL，经 notify.Build 包装后必须被擦除
	c := &http.Client{Transport: failRT{}}
	n, err := notify.Build("telegram", map[string]string{"token": "123456:SECRETTOKEN", "chat_id": "1"}, notify.Env{HTTP: c, Proxy: c})
	if err != nil {
		t.Fatal(err)
	}
	err = n.Send(context.Background(), notify.Message{Title: "T"})
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Fatalf("token leaked or no error: %v", err)
	}
}

func TestBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()
	b := &Bot{base: srv.URL, token: "1:A", chatID: "x", http: srv.Client()}
	if err := b.Send(context.Background(), notify.Message{Title: "T"}); err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("want business error, got %v", err)
	}
}
