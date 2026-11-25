package datasource

import (
	"context"
	"fmt"
	"sync"
)

// FundInfo 是一只公募基金（场外）的基本信息。
type FundInfo struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Type string `json:"type"` // 如 "混合型-灵活"、"股票型"、"指数型-股票"
}

// FundNAV 是基金某日净值。Date 为 YYYY-MM-DD。
type FundNAV struct {
	Date   string  `json:"date"`
	NAV    float64 `json:"nav"`    // 单位净值
	AccNAV float64 `json:"accNav"` // 累计净值（含分红，适合算长期收益）
}

// FundSource 是场外基金净值数据插件。
type FundSource interface {
	Name() string
	// ListFunds 返回全部基金清单（用于搜索与名称展示）。
	ListFunds(ctx context.Context) ([]FundInfo, error)
	// NAV 返回日期 ≥ from（空为不限）的净值，按日期升序。
	NAV(ctx context.Context, code, from string) ([]FundNAV, error)
}

type FundFactory func(env Env) (FundSource, error)

var (
	fundMu  sync.RWMutex
	fundReg = map[string]FundFactory{}
)

// RegisterFund 由基金插件在 init() 中调用。
func RegisterFund(name string, f FundFactory) {
	fundMu.Lock()
	defer fundMu.Unlock()
	if _, dup := fundReg[name]; dup {
		panic("datasource: duplicate fund plugin " + name)
	}
	fundReg[name] = f
}

// BuildFund 按名称构造基金插件。
func BuildFund(name string, env Env) (FundSource, error) {
	fundMu.RLock()
	f, ok := fundReg[name]
	fundMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("datasource: unknown fund plugin %q", name)
	}
	return f(env)
}
