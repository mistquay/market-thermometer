// Package datasource 是数据插件层：引擎只消费"标准指标"，数据从哪个插件来由配置路由决定。
package datasource

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// MetricID 是标准指标 ID，如 "cn.hs300.close"。
type MetricID string

// Point 是一个日频观测值。Date 为 YYYY-MM-DD，按该指标所属市场的本地时区。
type Point struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// Source 是时间序列类数据源插件。
// 约定：插件只返回已经收盘、已定稿的数据（例如收盘前不返回当日 bar），引擎不做这层判断。
type Source interface {
	Name() string
	Provides() []MetricID
	Fetch(ctx context.Context, m MetricID, from, to time.Time) ([]Point, error)
}

// Env 是插件构造时可用的运行环境。
type Env struct {
	HTTP    *http.Client
	Now     func() time.Time
	Options map[string]any // config.yaml 中该插件的专属配置
}

// Factory 构造一个插件实例。
type Factory func(env Env) (Source, error)

var (
	regMu    sync.RWMutex
	registry = map[string]Factory{}
	regOrder []string
)

// Register 由内置插件在 init() 中调用。
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("datasource: duplicate plugin " + name)
	}
	registry[name] = f
	regOrder = append(regOrder, name)
}

// Registered 按注册顺序返回全部插件名。
func Registered() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	return append([]string(nil), regOrder...)
}

func build(name string, env Env) (Source, error) {
	regMu.RLock()
	f, ok := registry[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("datasource: unknown plugin %q", name)
	}
	return f(env)
}

// Metric 是标准指标目录中的一项。
type Metric struct {
	ID      MetricID `json:"id"`
	Market  string   `json:"market"`
	Name    string   `json:"name"`
	Unit    string   `json:"unit"`
	LagDays int      `json:"lagDays"` // 发布时滞：T 日数据最早在 T+LagDays 日可得
	Note    string   `json:"note,omitempty"`
}

var (
	catMu   sync.RWMutex
	catalog = map[MetricID]Metric{}
)

// Define 向指标目录登记一个标准指标。
func Define(m Metric) {
	catMu.Lock()
	defer catMu.Unlock()
	catalog[m.ID] = m
}

// Lookup 查询指标定义。
func Lookup(id MetricID) (Metric, bool) {
	catMu.RLock()
	defer catMu.RUnlock()
	m, ok := catalog[id]
	return m, ok
}

// Metrics 返回全部指标定义，按 ID 排序。
func Metrics() []Metric {
	catMu.RLock()
	defer catMu.RUnlock()
	out := make([]Metric, 0, len(catalog))
	for _, m := range catalog {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
