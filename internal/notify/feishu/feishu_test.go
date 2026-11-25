package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"market-thermometer/internal/notify"
)

func TestSignVector(t *testing.T) {
	// 固定向量：HMAC-SHA256(key="1732500000\nSECf", msg="") 的 base64（由 openssl 独立计算）
	if got := Sign("SECf", 1732500000); got != "Q12ZxoATIUmht0440Qdc0cQx5doaxX2YfryuCBESnyc=" {
		t.Fatalf("sign = %s", got)
	}
}

func TestSend(t *testing.T) {
	now := time.Unix(1732500000, 0)
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"code":0,"msg":"success","data":{}}`))
	}))
	defer srv.Close()
	b := &Bot{webhook: srv.URL, secret: "SECf", http: srv.Client(), now: func() time.Time { return now }}
	if err := b.Send(context.Background(), notify.Message{Title: "T", Lines: []string{"**a**", "b"}}); err != nil {
		t.Fatal(err)
	}
	if got["timestamp"] != "1732500000" || got["sign"] != Sign("SECf", 1732500000) || got["msg_type"] != "interactive" {
		t.Fatalf("body: %v", got)
	}
	card, _ := json.Marshal(got["card"])
	if !strings.Contains(string(card), `"content":"**a**\nb"`) || !strings.Contains(string(card), `"content":"T"`) {
		t.Fatalf("card: %s", card)
	}
}

func TestSendBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":19021,"msg":"sign match fail or timestamp is not within one hour from current time"}`))
	}))
	defer srv.Close()
	b := &Bot{webhook: srv.URL, http: srv.Client(), now: time.Now}
	if err := b.Send(context.Background(), notify.Message{Title: "T"}); err == nil || !strings.Contains(err.Error(), "19021") {
		t.Fatalf("want business error, got %v", err)
	}
}
