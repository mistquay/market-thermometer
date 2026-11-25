package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"market-thermometer/internal/plugin"
)

var testKind = Kind{
	Name: "t",
	Fields: []Field{
		{Key: "webhook", Label: "Webhook", Secret: true, Required: true},
		{Key: "secret", Label: "密钥", Secret: true},
		{Key: "chat", Label: "Chat"},
	},
}

func TestMaskMerge(t *testing.T) {
	old := map[string]string{"webhook": "https://x.com/send?access_token=abcdef123456", "secret": "SECxyz", "chat": "1"}
	m := testKind.Mask(old)
	if m["webhook"] != plugin.MaskPrefix+"3456" || m["secret"] != plugin.MaskPrefix || m["chat"] != "1" {
		t.Fatalf("mask: %v", m)
	}
	// 打码值、空值 → 保持原值；普通字段按提交值；未声明字段丢弃
	got := testKind.Merge(old, map[string]string{"webhook": m["webhook"], "secret": "", "chat": "2", "evil": "x"})
	if got["webhook"] != old["webhook"] || got["secret"] != "SECxyz" || got["chat"] != "2" || got["evil"] != "" {
		t.Fatalf("merge: %v", got)
	}
	if got := testKind.Merge(old, map[string]string{"webhook": "https://new"}); got["webhook"] != "https://new" {
		t.Fatalf("merge new secret: %v", got)
	}
}

func TestValidate(t *testing.T) {
	if err := testKind.Validate(map[string]string{"webhook": " "}); err == nil {
		t.Fatal("blank required accepted")
	}
	if err := testKind.Validate(map[string]string{"webhook": "x"}); err != nil {
		t.Fatal(err)
	}
}

type failing struct{ err error }

func (f failing) Send(context.Context, Message) error { return f.err }

func TestScrub(t *testing.T) {
	cfg := map[string]string{"webhook": "https://x.com/send?access_token=abcdef123456", "secret": "SECxyz"}
	n := scrubbed{failing{errors.New(`Post "https://x.com/send?access_token=abcdef123456&sign=1": dial tcp: SECxyz`)}, testKind, cfg}
	err := n.Send(context.Background(), Message{})
	if strings.Contains(err.Error(), "abcdef123456") || strings.Contains(err.Error(), "SECxyz") {
		t.Fatalf("secret leaked: %v", err)
	}
	if n := (scrubbed{failing{nil}, testKind, cfg}); n.Send(context.Background(), Message{}) != nil {
		t.Fatal("nil error changed")
	}
}

func TestCheckURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://oapi.dingtalk.com/robot/send?access_token=1": true,
		"https://OAPI.dingtalk.com/x":                         true,
		"http://oapi.dingtalk.com/x":                          false,
		"https://oapi.dingtalk.com.evil.com/x":                false,
		"https://127.0.0.1/x":                                 false,
	} {
		if err := CheckURL(raw, "oapi.dingtalk.com"); (err == nil) != ok {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestMessageRender(t *testing.T) {
	m := Message{Title: "标题", Lines: []string{"**a**：1", "b"}}
	if got := m.Markdown(); got != "**标题**\n\n**a**：1\n\nb" {
		t.Fatalf("markdown: %q", got)
	}
	if got := m.Text(); got != "标题\na：1\nb" {
		t.Fatalf("text: %q", got)
	}
}
