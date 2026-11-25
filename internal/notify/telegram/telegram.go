// Package telegram 是 Telegram Bot 渠道（sendMessage，纯文本）。
//
// 大陆网络无法直连 api.telegram.org：请求走 config.yaml 的 http_proxy（未配置时直连）。
// Bot token 出现在 URL 路径中，错误信息里会被擦除。业务错误在响应的 ok / description 中返回。
package telegram

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"market-thermometer/internal/notify"
)

func init() {
	notify.Register(notify.Kind{
		Name:  "telegram",
		Label: "Telegram Bot",
		Fields: []notify.Field{
			{Key: "token", Label: "Bot Token", Secret: true, Required: true, Help: "向 @BotFather 申请，形如 123456:ABC…"},
			{Key: "chat_id", Label: "Chat ID", Required: true, Help: "用户、群组或频道的 ID（频道可用 @频道名）"},
		},
		New: func(cfg map[string]string, env notify.Env) (notify.Notifier, error) {
			token := strings.TrimSpace(cfg["token"])
			if strings.ContainsAny(token, "/?#") {
				return nil, fmt.Errorf("Bot Token 格式不对")
			}
			return &Bot{base: "https://api.telegram.org", token: token, chatID: strings.TrimSpace(cfg["chat_id"]), http: env.Proxy}, nil
		},
	})
}

type Bot struct {
	base, token, chatID string
	http                *http.Client
}

func (b *Bot) Send(ctx context.Context, m notify.Message) error {
	body := map[string]any{"chat_id": b.chatID, "text": m.Text(), "disable_web_page_preview": true}
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	// 非 2xx 时（如 chat 不存在返回 400）错误信息里带有响应体中的 description
	if err := notify.PostJSON(ctx, b.http, b.base+"/bot"+b.token+"/sendMessage", body, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("Telegram 返回错误：%s", resp.Description)
	}
	return nil
}
