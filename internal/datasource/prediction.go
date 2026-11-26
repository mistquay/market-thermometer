package datasource

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// 预测市场数据插件（如 Polymarket）：事件 → 市场 → 结果代币（每个结果一个代币，价格 0–1，结算时赢家为 1、其余为 0）。

// PMEvent 是一个事件（可包含多个市场，如“12 月美联储决议”下有降息 / 不变 / 加息）。
type PMEvent struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Series    string    `json:"series,omitempty"` // 周期性系列，如 btc-up-or-down-15m
	Tags      []string  `json:"tags"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Closed    bool      `json:"closed"`
	NegRisk   bool      `json:"negRisk"` // 互斥多结果事件：各市场 YES 价之和理论上为 1
	Volume    float64   `json:"volume"`
	Volume24h float64   `json:"volume24h"`
	Liquidity float64   `json:"liquidity"`
	// PriceToBeat 是短期涨跌盘的起始价（计价开始时刻的预言机价格，Up 要求结束价 >= 它）；
	// 只在事件结束后才公布，0 表示没有或还没公布，-1 表示结束很久仍没有（不再补拉）。
	PriceToBeat float64    `json:"priceToBeat,omitempty"`
	Markets     []PMMarket `json:"markets"`
}

// PMMarket 是一个市场（通常是二元：Yes / No，或 Up / Down）。
type PMMarket struct {
	ConditionID string    `json:"conditionId"`
	Slug        string    `json:"slug"`
	Question    string    `json:"question"`
	GroupTitle  string    `json:"groupTitle,omitempty"` // 多结果事件中该市场的简称
	Outcomes    []string  `json:"outcomes"`
	Tokens      []string  `json:"tokens"` // 与 Outcomes 一一对应的代币 ID
	Prices      []float64 `json:"prices"` // 与 Outcomes 一一对应的当前价格（已结算时为 1 / 0）
	BestBid     float64   `json:"bestBid"`
	BestAsk     float64   `json:"bestAsk"`
	LastPrice   float64   `json:"lastPrice"`
	Volume      float64   `json:"volume"`
	Volume24h   float64   `json:"volume24h"`
	Liquidity   float64   `json:"liquidity"`
	Start       time.Time `json:"start"` // 短期涨跌盘：计价区间的开始时间
	End         time.Time `json:"end"`
	Closed      bool      `json:"closed"`
	Accepting   bool      `json:"accepting"` // 是否接受下单
	TickSize    float64   `json:"tickSize"`
	MinSize     float64   `json:"minSize"`
	FeeType     string    `json:"feeType,omitempty"`
	// 吃单手续费（USDC）= 股数 × FeeRate × (p × (1 − p))^FeeExp；挂单不收。FeeRate 为 0 表示不收费
	FeeRate float64 `json:"feeRate,omitempty"`
	FeeExp  float64 `json:"feeExp,omitempty"`
	// 结算来源：ResolutionSource 为市场给出的链接；TwapSeconds > 0 表示按 Chainlink 的 N 秒 TWAP 结算
	// （2026-11-18 起的 5m / 15m / 1h 涨跌盘），为 0 且来源是币安时按币安 K 线开收盘结算（此前的 1h 盘）。
	ResolutionSource string `json:"resolutionSource,omitempty"`
	TwapSeconds      int    `json:"twapSeconds,omitempty"`
	Winner           int    `json:"winner"` // 已结算的赢家结果下标；未结算为 -1
	Resolution       string `json:"resolution,omitempty"`
}

// PMEventQuery 是事件查询条件。
type PMEventQuery struct {
	Slug   string
	Slugs  []string // 一次按多个 slug 取
	Series string
	Tag    string
	Closed *bool
	EndMin time.Time // 结束时间下限（零值不限），用来跳过早该结束却仍挂着 active 的陈旧事件
	Order  string    // 如 volume24hr、endDate
	Asc    bool
	Limit  int // 每页最多 100
	Offset int
}

// PricePoint 是一个价格点（T 为 Unix 秒）。
type PricePoint struct {
	T int64   `json:"t"`
	P float64 `json:"p"`
}

// BookLevel 是盘口一档。
type BookLevel struct {
	Price float64 `json:"price"`
	Size  float64 `json:"size"`
}

// Book 是一个代币的订单簿：Bids 价格从高到低、Asks 从低到高。
type Book struct {
	Token string      `json:"token"`
	Time  time.Time   `json:"time"`
	Bids  []BookLevel `json:"bids"`
	Asks  []BookLevel `json:"asks"`
	Hash  string      `json:"hash,omitempty"` // 订单簿内容摘要，没变化时相同
}

// TakerFee 返回以价格 p 吃单 shares 股的手续费（USDC）。
func (m PMMarket) TakerFee(p, shares float64) float64 {
	if m.FeeRate <= 0 {
		return 0
	}
	exp := m.FeeExp
	if exp == 0 {
		exp = 1
	}
	return shares * m.FeeRate * math.Pow(p*(1-p), exp)
}

// PredictionSource 是预测市场数据插件（只读）。
type PredictionSource interface {
	Name() string
	Events(ctx context.Context, q PMEventQuery) ([]PMEvent, error)
	// EventsAfter 按游标分页（适合深翻页）：cursor 为空取第一页，返回下一页游标（为空表示没有更多）。忽略 q.Offset。
	EventsAfter(ctx context.Context, q PMEventQuery, cursor string) ([]PMEvent, string, error)
	Search(ctx context.Context, text string, limit int) ([]PMEvent, error)
	// PriceHistory 返回代币在 [from, to]（Unix 秒）内的价格，fidelity 为采样间隔（分钟）。
	PriceHistory(ctx context.Context, token string, from, to int64, fidelity int) ([]PricePoint, error)
	Book(ctx context.Context, token string) (Book, error)
	// Books 一次取多个代币的订单簿（顺序不保证与 tokens 一致，按 Book.Token 对应）。
	Books(ctx context.Context, tokens []string) ([]Book, error)
}

// PredictionFactory 构造一个预测市场插件。
type PredictionFactory func(env Env) (PredictionSource, error)

var (
	predMu  sync.RWMutex
	predReg = map[string]PredictionFactory{}
)

// RegisterPrediction 由插件在 init() 中调用。
func RegisterPrediction(name string, f PredictionFactory) {
	predMu.Lock()
	defer predMu.Unlock()
	if _, dup := predReg[name]; dup {
		panic("datasource: duplicate prediction plugin " + name)
	}
	predReg[name] = f
}

// BuildPrediction 按名称构造预测市场插件。
func BuildPrediction(name string, env Env) (PredictionSource, error) {
	predMu.RLock()
	f, ok := predReg[name]
	predMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("datasource: unknown prediction plugin %q", name)
	}
	return f(env)
}
