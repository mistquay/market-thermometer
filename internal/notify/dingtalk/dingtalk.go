// Package dingtalk 是钉钉群自定义机器人渠道（Markdown 消息，支持“加签”安全设置）。
//
// 加签：timestamp 为毫秒；sign = urlencode(base64(HMAC-SHA256(key=secret, msg=timestamp+"\n"+secret)))，
// 两者作为 query 参数附在 webhook 后。业务错误在 HTTP 200 的 errcode 中返回。
package dingtalk

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"market-thermometer/internal/notify"
)

func init() {
	notify.Register(notify.Kind{
		Name:  "dingtalk",
		Label: "钉钉机器人",
		Fields: []notify.Field{
			{Key: "webhook", Label: "Webhook 地址", Secret: true, Required: true, Help: "https://oapi.dingtalk.com/robot/send?access_token=…"},
			{Key: "secret", Label: "加签密钥", Secret: true, Help: "安全设置选“加签”时填写，以 SEC 开头"},
		},
		New: func(cfg map[string]string, env notify.Env) (notify.Notifier, error) {
			if err := notify.CheckURL(cfg["webhook"], "oapi.dingtalk.com"); err != nil {
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

// Sign 计算加签参数。
func Sign(secret string, tsMillis int64) string {
	ts := strconv.FormatInt(tsMillis, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (b *Bot) Send(ctx context.Context, m notify.Message) error {
	u := b.webhook
	if b.secret != "" {
		ts := b.now().UnixMilli()
		u += "&timestamp=" + strconv.FormatInt(ts, 10) + "&sign=" + url.QueryEscape(Sign(b.secret, ts))
	}
	body := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"title": m.Title, "text": m.Markdown()},
	}
	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := notify.PostJSON(ctx, b.http, u, body, &resp); err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("钉钉返回错误 %d：%s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}
