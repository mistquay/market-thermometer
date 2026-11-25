// Package feishu 是飞书群自定义机器人渠道（消息卡片 + Markdown，支持“签名校验”安全设置）。
//
// 签名：timestamp 为秒；sign = base64(HMAC-SHA256(key=timestamp+"\n"+secret, msg=空))，放在请求体中。
// 业务错误在 HTTP 200 的 code 中返回（非 0 即失败）。
package feishu

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"market-thermometer/internal/notify"
)

func init() {
	notify.Register(notify.Kind{
		Name:  "feishu",
		Label: "飞书机器人",
		Fields: []notify.Field{
			{Key: "webhook", Label: "Webhook 地址", Secret: true, Required: true, Help: "https://open.feishu.cn/open-apis/bot/v2/hook/…"},
			{Key: "secret", Label: "签名密钥", Secret: true, Help: "安全设置选“签名校验”时填写"},
		},
		New: func(cfg map[string]string, env notify.Env) (notify.Notifier, error) {
			if err := notify.CheckURL(cfg["webhook"], "open.feishu.cn", "open.larksuite.com"); err != nil {
				return nil, err
			}
			return &Bot{webhook: cfg["webhook"], secret: cfg["secret"], http: env.HTTP, now: time.Now}, nil
		},
	})
}

type Bot struct {
	webhook, secret string
	http            *http.Client
	now             func() time.Time
}

// Sign 计算签名。
func Sign(secret string, tsSeconds int64) string {
	mac := hmac.New(sha256.New, []byte(strconv.FormatInt(tsSeconds, 10)+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (b *Bot) Send(ctx context.Context, m notify.Message) error {
	body := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": m.Title}},
			"elements": []any{
				map[string]string{"tag": "markdown", "content": strings.Join(m.Lines, "\n")},
			},
		},
	}
	if b.secret != "" {
		ts := b.now().Unix()
		body["timestamp"] = strconv.FormatInt(ts, 10)
		body["sign"] = Sign(b.secret, ts)
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := notify.PostJSON(ctx, b.http, b.webhook, body, &resp); err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("飞书返回错误 %d：%s", resp.Code, resp.Msg)
	}
	return nil
}
