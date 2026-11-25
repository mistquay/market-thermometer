package datasource

import (
	"context"
	"fmt"
	"sync"
)

// Stock 是一只股票（含已退市）。
type Stock struct {
	Code     string `json:"code"`
	Delisted bool   `json:"delisted"`
}

// StockBar 是个股的一根日 K。Close 为不复权收盘价；Mult 为当日除权乘数（除权日前一日收盘 ÷ 除权参考价，
// 无除权为 1），用于链式计算后复权价：hfq_t = hfq_{t-1} × Close_t × Mult_t / Close_{t-1}。
// 只包含有成交的交易日（停牌日不出现）。
type StockBar struct {
	Date  string  `json:"date"`
	Close float64 `json:"close"`
	Mult  float64 `json:"mult"`
}

// StockSource 是个股数据插件，只用于派生全市场指标（如市场广度）。
type StockSource interface {
	Name() string
	// ListStocks 返回全部 A 股代码，含已退市股票（避免幸存者偏差）。
	ListStocks(ctx context.Context) ([]Stock, error)
	// Bars 返回 [from, to]（YYYY-MM-DD，空为不限）内已定稿的日 K，按日期升序。
	Bars(ctx context.Context, code, from, to string) ([]StockBar, error)
}

// StockFactory 构造一个个股数据插件。
type StockFactory func(env Env) (StockSource, error)

var (
	stockMu  sync.RWMutex
	stockReg = map[string]StockFactory{}
)

// RegisterStock 由个股插件在 init() 中调用。
func RegisterStock(name string, f StockFactory) {
	stockMu.Lock()
	defer stockMu.Unlock()
	if _, dup := stockReg[name]; dup {
		panic("datasource: duplicate stock plugin " + name)
	}
	stockReg[name] = f
}

// BuildStock 按名称构造个股插件。
func BuildStock(name string, env Env) (StockSource, error) {
	stockMu.RLock()
	f, ok := stockReg[name]
	stockMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("datasource: unknown stock plugin %q", name)
	}
	return f(env)
}

// IsAShare 判断是否为沪深 A 股代码（排除基金、B 股、北交所）。
func IsAShare(code string) bool {
	if len(code) != 6 {
		return false
	}
	switch code[:3] {
	case "000", "001", "002", "003", "300", "301", "600", "601", "603", "605", "688", "689":
		return true
	}
	return false
}
