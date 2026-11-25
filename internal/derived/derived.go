// Package derived 是派生指标插件：只读本地数据，计算并写回标准指标序列（如市场广度）。
// 派生指标在个股日线同步之后运行，数据源不可用时也能从本地数据全量重算。
package derived

import (
	"context"
	"sort"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/plugin"
	"market-thermometer/internal/store"
)

// Env 是核心提供给派生指标的环境。
type Env struct {
	Store    *store.Store
	Now      func() time.Time
	Logf     func(format string, args ...any)
	Progress func(done, total int) // 进度上报，可为 nil
}

// Derived 是一个派生指标。
type Derived interface {
	Name() string
	Metric() ds.MetricID
	// Compute 从本地数据更新派生序列；full=true 时清空中间状态后全量重算。
	Compute(ctx context.Context, env Env, full bool) error
}

var (
	mu  sync.RWMutex
	reg = map[string]Derived{}
)

// Register 由内置派生指标在 init() 中调用，同时登记插件清单。
func Register(d Derived, m plugin.Manifest) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := reg[d.Name()]; dup {
		panic("derived: duplicate " + d.Name())
	}
	reg[d.Name()] = d
	m.Name, m.Kind = d.Name(), plugin.KindDerived
	plugin.MustRegister(m)
}

// All 返回全部派生指标（按名称排序）。
func All() []Derived {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Derived, 0, len(reg))
	for _, d := range reg {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
