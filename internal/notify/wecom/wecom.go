// Package wecom 是企业微信群机器人渠道（Markdown 消息）。
//
// 企业微信群机器人没有签名机制，webhook 中的 key 即凭证；业务错误在 HTTP 200 的 errcode 中返回。
// Markdown 内容上限 4096 字节，超出时截断。
package wecom

import (
	"context"
	"fmt"
	"net/http"
	"unicode/utf8"

	"market-thermometer/internal/notify"
)

func init() {
	notify.Register(notify.Kind{
		Name:  "wecom",
		Label: "企业微信机器人",
		Fields: []notify.Field{
			{Key: "webhook", Label: "Webhook 地址", Secret: true, Required: true, Help: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…"},
		},
		New: func(cfg map[string]string, env notify.Env) (notify.Notifier, error) {
			if err := notify.CheckURL(cfg["webhook"], "qyapi.weixin.qq.com"); err != nil {
				return nil, err
			}
			return &Bot{webhook: cfg["webhook"], http: env.HTTP}, nil
		},
	})
}

type Bot struct {
	webhook string
	http    *http.Client
}

const maxBytes = 4096

func (b *Bot) Send(ctx context.Context, m notify.Message) error {
	content := m.Markdown()
	for len(content) > maxBytes { // 按字节截断，且不切断 UTF-8 字符
		_, size := utf8.DecodeLastRuneInString(content)
		content = content[:len(content)-size]
	}
	body := map[string]any{"msgtype": "markdown", "markdown": map[string]string{"content": content}}
	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := notify.PostJSON(ctx, b.http, b.webhook, body, &resp); err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("企业微信返回错误 %d：%s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}
