package datasource

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// 证券类别。
const (
	KindStock = "stock" // 沪深 A 股
	KindFund  = "fund"  // 场内基金（ETF、LOF 等）
)

// ErrNotSupported 表示数据源不支持该操作（如腾讯不提供证券清单），调用方应换用其他来源。
var ErrNotSupported = errors.New("数据源不支持该操作")

// Stock 是一只证券（含已退市）。名称沿用 Stock，Kind 区分股票与场内基金。
type Stock struct {
	Code     string `json:"code"`
	Kind     string `json:"kind"`
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

// StockSource 是个股 / 场内基金日线数据插件。数据同步进本地库后长期保存。
type StockSource interface {
	Name() string
	// ListStocks 返回全部 A 股与场内基金代码，含已退市（避免幸存者偏差）。不支持时返回 ErrNotSupported。
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

// KindOf 按代码判断证券类别；既不是 A 股也不是场内基金时返回 ""。
func KindOf(code string) string {
	switch {
	case IsAShare(code):
		return KindStock
	case IsExchangeFund(code):
		return KindFund
	}
	return ""
}

// IsExchangeFund 判断是否为沪深场内基金代码（深市 15/16/18 开头，沪市 50/51/52/56/58 开头）。
func IsExchangeFund(code string) bool {
	if len(code) != 6 {
		return false
	}
	switch code[:2] {
	case "15", "16", "18", "50", "51", "52", "56", "58":
		return true
	}
	return false
}

// MarketPrefix 返回沪深代码的交易所前缀（sh / sz），无法判断时返回 ""。
func MarketPrefix(code string) string {
	if len(code) != 6 {
		return ""
	}
	switch code[0] {
	case '5', '6':
		return "sh"
	case '0', '1', '2', '3':
		return "sz"
	}
	return ""
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
