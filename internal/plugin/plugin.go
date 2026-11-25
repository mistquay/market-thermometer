// Package plugin 是插件清单与统一目录：每个插件（内置或外部进程）声明名称、类型、版本、能力与配置字段，
// Web 的插件管理页据此展示并按字段生成配置表单。
//
// 各类插件仍在各自的注册表中注册实现（datasource、notify 等），这里只登记清单，不持有实现。
package plugin

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Kind 是插件类型。
type Kind string

const (
	KindMetric   Kind = "metric_source" // 指标数据源：Fetch(metric, from, to) → points
	KindStock    Kind = "stock_source"  // 个股 / 场内基金日线源
	KindFund     Kind = "fund_source"   // 场外基金净值源
	KindNotifier Kind = "notifier"      // 推送渠道
	KindDerived  Kind = "derived"       // 派生指标：只读本地数据，输出时间序列
	KindStrategy Kind = "strategy"      // 策略：输入指标，输出得分、信号与目标仓位
	KindBroker   Kind = "broker"        // 交易通道（只设计接口，暂不实现）
)

// KindLabel 是类型的中文名。
var KindLabel = map[Kind]string{
	KindMetric: "指标数据源", KindStock: "个股日线源", KindFund: "基金净值源", KindNotifier: "推送渠道",
	KindDerived: "派生指标", KindStrategy: "策略", KindBroker: "交易通道",
}

// Field 是一个配置字段。
type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Help     string `json:"help,omitempty"`
	Type     string `json:"type,omitempty"`   // "" 文本 | number | bool
	Secret   bool   `json:"secret,omitempty"` // 敏感：API 响应中打码，错误信息中擦除
	Required bool   `json:"required,omitempty"`
}

// MetricDef 声明插件提供的指标；外部插件可以借此定义新的标准指标。
type MetricDef struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Unit    string `json:"unit,omitempty"`
	Market  string `json:"market,omitempty"`
	LagDays int    `json:"lagDays,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Manifest 是插件清单。外部插件的清单是插件目录下的 plugin.json。
type Manifest struct {
	Name         string      `json:"name"`
	Kind         Kind        `json:"kind"`
	Version      string      `json:"version"`
	Label        string      `json:"label"`
	Description  string      `json:"description,omitempty"`
	Metrics      []MetricDef `json:"metrics,omitempty"`      // 指标源 / 派生指标：提供的指标
	Capabilities []string    `json:"capabilities,omitempty"` // 其他能力说明，如 "list"、"bars"
	Config       []Field     `json:"config,omitempty"`       // 配置字段
	Builtin      bool        `json:"builtin"`
}

// ID 是插件在目录中的唯一标识（同名插件可以属于不同类型，如东财既是指标源也是基金净值源）。
func (m Manifest) ID() string { return string(m.Kind) + "/" + m.Name }

// Validate 检查清单的必填项（外部插件的 plugin.json 是不可信输入）。
func (m Manifest) Validate() error {
	var errs []error
	if !validName(m.Name) {
		errs = append(errs, fmt.Errorf("name %q 须为 1–40 个小写字母、数字、- 或 _", m.Name))
	}
	if _, ok := KindLabel[m.Kind]; !ok {
		errs = append(errs, fmt.Errorf("未知的插件类型 %q", m.Kind))
	}
	if m.Version == "" {
		errs = append(errs, errors.New("缺少 version"))
	}
	keys := map[string]bool{}
	for _, f := range m.Config {
		if f.Key == "" || keys[f.Key] {
			errs = append(errs, fmt.Errorf("配置字段 key %q 为空或重复", f.Key))
		}
		keys[f.Key] = true
	}
	for _, d := range m.Metrics {
		if d.ID == "" || strings.ContainsAny(d.ID, " /\\") {
			errs = append(errs, fmt.Errorf("指标 id %q 不合法", d.ID))
		}
	}
	return errors.Join(errs...)
}

func validName(s string) bool {
	if len(s) == 0 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

var (
	mu      sync.RWMutex
	catalog = map[string]Manifest{}
)

// Register 登记插件清单（内置插件在 init() 中调用；外部插件在启动时由宿主调用）。
func Register(m Manifest) error {
	if m.Label == "" {
		m.Label = m.Name
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("插件 %s：%w", m.Name, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := catalog[m.ID()]; dup {
		return fmt.Errorf("插件 %s 重复登记", m.ID())
	}
	catalog[m.ID()] = m
	return nil
}

// MustRegister 供内置插件在 init() 中使用。
func MustRegister(m Manifest) {
	m.Builtin = true
	if m.Version == "" {
		m.Version = "builtin"
	}
	if err := Register(m); err != nil {
		panic(err)
	}
}

// All 返回全部清单（按类型、名称排序）。
func All() []Manifest {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Manifest, 0, len(catalog))
	for _, m := range catalog {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Get 按类型与名称查找清单。
func Get(kind Kind, name string) (Manifest, bool) {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := catalog[string(kind)+"/"+name]
	return m, ok
}

// ---- 配置字段工具：校验、打码、合并、擦除 ----

// ValidateConfig 检查必填字段。
func ValidateConfig(fields []Field, cfg map[string]string) error {
	for _, f := range fields {
		if f.Required && strings.TrimSpace(cfg[f.Key]) == "" {
			return fmt.Errorf("缺少必填项：%s", f.Label)
		}
	}
	return nil
}

// MaskPrefix 是打码值的前缀；客户端提交的值以它开头时表示“保持原值”。
const MaskPrefix = "••••"

// Mask 返回可安全输出的配置：敏感字段只保留末 4 位（短于 9 个字符的不保留）。
func Mask(fields []Field, cfg map[string]string) map[string]string {
	out := make(map[string]string, len(cfg))
	maps.Copy(out, cfg)
	for _, f := range fields {
		if v := cfg[f.Key]; f.Secret && v != "" {
			tail := ""
			if r := []rune(v); len(r) > 8 {
				tail = string(r[len(r)-4:])
			}
			out[f.Key] = MaskPrefix + tail
		}
	}
	return out
}

// Merge 用客户端提交的配置更新旧配置：只保留声明过的字段；敏感字段为空或仍是打码值时保持原值。
func Merge(fields []Field, old, submitted map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range fields {
		v := strings.TrimSpace(submitted[f.Key])
		if f.Secret && (v == "" || strings.HasPrefix(v, MaskPrefix)) {
			v = old[f.Key]
		}
		if v != "" {
			out[f.Key] = v
		}
	}
	return out
}

// Scrub 把错误信息中的敏感值替换为 ***（Go 的 url.Error 会带上含 token 的完整 URL）。
func Scrub(fields []Field, cfg map[string]string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, f := range fields {
		if v := cfg[f.Key]; f.Secret && len(v) >= 4 {
			msg = strings.ReplaceAll(msg, v, "***")
			msg = strings.ReplaceAll(msg, url.QueryEscape(v), "***")
			if u, perr := url.Parse(v); perr == nil && u.RawQuery != "" { // webhook 中的 access_token 等参数
				msg = strings.ReplaceAll(msg, u.RawQuery, "***")
			}
		}
	}
	return errors.New(msg)
}
