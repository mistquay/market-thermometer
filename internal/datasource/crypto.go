package datasource

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// 加密货币 K 线数据插件（7×24 交易，时间一律用 UTC 毫秒）。数据同步进本地库长期保存。

// Kline 是一根 K 线。T 为开盘时间（UTC 毫秒）。
type Kline struct {
	T      int64   `json:"t"`
	Open   float64 `json:"o"`
	High   float64 `json:"h"`
	Low    float64 `json:"l"`
	Close  float64 `json:"c"`
	Volume float64 `json:"v"` // 基础币数量
	Quote  float64 `json:"q"` // 计价币成交额（如 USDT）
}

// 支持的周期。
var KlineIntervals = map[string]time.Duration{
	"1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute,
	"1h": time.Hour, "4h": 4 * time.Hour, "1d": 24 * time.Hour,
}

// KlineSource 是加密货币 K 线数据插件。symbol 用统一写法，如 BTCUSDT。
type KlineSource interface {
	Name() string
	// Klines 返回开盘时间在 [from, to)（UTC 毫秒）内、已收盘的 K 线，按时间升序。
	Klines(ctx context.Context, symbol, interval string, from, to int64) ([]Kline, error)
	// Price 返回最新成交价（实时，用于策略与模拟盘）。
	Price(ctx context.Context, symbol string) (float64, error)
}

// KlineFactory 构造一个加密 K 线插件。
type KlineFactory func(env Env) (KlineSource, error)

var (
	klineMu  sync.RWMutex
	klineReg = map[string]KlineFactory{}
)

// RegisterKline 由插件在 init() 中调用。
func RegisterKline(name string, f KlineFactory) {
	klineMu.Lock()
	defer klineMu.Unlock()
	if _, dup := klineReg[name]; dup {
		panic("datasource: duplicate kline plugin " + name)
	}
	klineReg[name] = f
}

// BuildKline 按名称构造加密 K 线插件。
func BuildKline(name string, env Env) (KlineSource, error) {
	klineMu.RLock()
	f, ok := klineReg[name]
	klineMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("datasource: unknown kline plugin %q", name)
	}
	return f(env)
}

// ClosedKlines 去掉尚未收盘的最后一根、按时间去重排序。
func ClosedKlines(ks []Kline, interval string, now time.Time) []Kline {
	d := KlineIntervals[interval]
	sort.Slice(ks, func(i, j int) bool { return ks[i].T < ks[j].T })
	out := ks[:0]
	for _, k := range ks {
		if d > 0 && k.T+d.Milliseconds() > now.UnixMilli() {
			continue
		}
		if len(out) > 0 && out[len(out)-1].T == k.T {
			continue
		}
		out = append(out, k)
	}
	return out
}
