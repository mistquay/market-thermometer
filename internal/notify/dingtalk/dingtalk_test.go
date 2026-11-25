package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"market-thermometer/internal/notify"
)

func TestSendSigned(t *testing.T) {
	now := time.UnixMilli(1732500000123)
	var got struct {
		MsgType  string            `json:"msgtype"`
		Markdown map[string]string `json:"markdown"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ts, _ := strconv.ParseInt(q.Get("timestamp"), 10, 64)
		if q.Get("access_token") != "tok" || ts != now.UnixMilli() || q.Get("sign") != Sign("SEC1", ts) {
			t.Errorf("bad query: %s", r.URL.RawQuery)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()
	b := &Bot{webhook: srv.URL + "/robot/send?access_token=tok", secret: "SEC1", http: srv.Client(), now: func() time.Time { return now }}
	if err := b.Send(context.Background(), notify.Message{Title: "T", Lines: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if got.MsgType != "markdown" || got.Markdown["title"] != "T" || !strings.Contains(got.Markdown["text"], "x") {
		t.Fatalf("body: %+v", got)
	}
}

func TestSendBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errcode":310000,"errmsg":"sign not match"}`))
	}))
	defer srv.Close()
	b := &Bot{webhook: srv.URL + "/robot/send?access_token=tok", http: srv.Client(), now: time.Now}
	if err := b.Send(context.Background(), notify.Message{Title: "T"}); err == nil || !strings.Contains(err.Error(), "310000") {
		t.Fatalf("want business error, got %v", err)
	}
}

func TestSignVector(t *testing.T) {
	// 固定向量：HMAC-SHA256(key="SEC1", msg="1732500000123\nSEC1") 的 base64（由 openssl 独立计算）
	if got := Sign("SEC1", 1732500000123); got != "wBdWBcr4AI1j+MdEmOH/PpsjRDyx0kb+CVogUDLOJEg=" {
		t.Fatalf("sign = %s", got)
	}
}
