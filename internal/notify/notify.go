// Package notify 是推送渠道插件层：Notifier 接口、注册表与各插件共用的工具。
//
// 每个渠道插件在 init() 中 Register，声明配置字段（Field）；Web 按字段生成配置表单，
// 敏感字段（webhook、secret、token）在 API 响应中打码，错误信息里出现时也会被擦除。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"market-thermometer/internal/plugin"
)

// Message 是一条推送消息。Lines 中允许使用 **粗体**，各渠道自行适配格式。
type Message struct {
	Title string
	Lines []string
}

// Markdown 渲染为通用 Markdown：标题加粗，正文每行一段。
func (m Message) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**", m.Title)
	for _, l := range m.Lines {
		b.WriteString("\n\n")
		b.WriteString(l)
	}
	return b.String()
}

// Text 渲染为纯文本（去掉粗体标记）。
func (m Message) Text() string {
	lines := append([]string{m.Title}, m.Lines...)
	return strings.ReplaceAll(strings.Join(lines, "\n"), "**", "")
}

// Notifier 发送一条消息。实现必须检查渠道的业务错误码（各家常在 HTTP 200 中返回错误）。
type Notifier interface {
	Send(ctx context.Context, m Message) error
}

// Field 是渠道的一个配置字段（与插件清单共用）。
type Field = plugin.Field

// Env 是构造渠道时由核心提供的环境。
type Env struct {
	HTTP  *http.Client // 直连
	Proxy *http.Client // 走 config.yaml 的 http_proxy（未配置时同 HTTP），只给需要的渠道用（如 Telegram）
}

// Kind 是一种渠道插件。
type Kind struct {
	Name   string                                                 `json:"name"`
	Label  string                                                 `json:"label"`
	Fields []Field                                                `json:"fields"`
	New    func(cfg map[string]string, env Env) (Notifier, error) `json:"-"`
	// External 表示来自外部进程插件（清单由插件宿主登记）。
	External bool `json:"external,omitempty"`
}

var (
	mu    sync.RWMutex
	kinds = map[string]Kind{}
)

// Register 由渠道插件在 init() 中调用。
func Register(k Kind) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := kinds[k.Name]; dup {
		panic("notify: duplicate kind " + k.Name)
	}
	kinds[k.Name] = k
	if !k.External {
		plugin.MustRegister(plugin.Manifest{Name: k.Name, Kind: plugin.KindNotifier, Label: k.Label, Config: k.Fields})
	}
}

// Kinds 返回全部已注册渠道（按名称排序）。
func Kinds() []Kind {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Kind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup 按名称查找渠道插件。
func Lookup(name string) (Kind, bool) {
	mu.RLock()
	defer mu.RUnlock()
	k, ok := kinds[name]
	return k, ok
}

// Build 校验必填字段后构造渠道；返回的 Notifier 发送失败时，错误信息中的敏感值已被擦除。
func Build(kind string, cfg map[string]string, env Env) (Notifier, error) {
	k, ok := Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("未知的推送渠道 %q", kind)
	}
	if err := k.Validate(cfg); err != nil {
		return nil, err
	}
	n, err := k.New(cfg, env)
	if err != nil {
		return nil, k.scrub(err, cfg)
	}
	return scrubbed{n, k, cfg}, nil
}

// Validate 检查必填字段。
func (k Kind) Validate(cfg map[string]string) error { return plugin.ValidateConfig(k.Fields, cfg) }

// Mask 返回可安全输出的配置：敏感字段只保留末 4 位。
func (k Kind) Mask(cfg map[string]string) map[string]string { return plugin.Mask(k.Fields, cfg) }

// Merge 用客户端提交的配置更新旧配置（敏感字段为空或仍是打码值时保持原值）。
func (k Kind) Merge(old, submitted map[string]string) map[string]string {
	return plugin.Merge(k.Fields, old, submitted)
}

func (k Kind) scrub(err error, cfg map[string]string) error { return plugin.Scrub(k.Fields, cfg, err) }

type scrubbed struct {
	n   Notifier
	k   Kind
	cfg map[string]string
}

func (s scrubbed) Send(ctx context.Context, m Message) error {
	return s.k.scrub(s.n.Send(ctx, m), s.cfg)
}

// CheckURL 校验 webhook：必须是 https 且主机在白名单内（防止把请求发往任意地址）。
func CheckURL(raw string, hosts ...string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("webhook 地址格式不对")
	}
	if u.Scheme != "https" {
		return errors.New("webhook 必须是 https 地址")
	}
	for _, h := range hosts {
		if strings.EqualFold(u.Hostname(), h) {
			return nil
		}
	}
	return fmt.Errorf("webhook 域名必须是 %s", strings.Join(hosts, " / "))
}

// PostJSON 以 JSON 发送 body，HTTP 状态非 2xx 时报错，响应体解码到 out。
func PostJSON(ctx context.Context, c *http.Client, url string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, truncate(raw, 200))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("响应解析失败：%s", truncate(raw, 200))
		}
	}
	return nil
}

func truncate(b []byte, n int) string {
	if r := []rune(string(b)); len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(b)
}
