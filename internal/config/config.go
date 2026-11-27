// Package config 定义应用配置结构与默认值。
// 支持零配置启动（配置持久化在 SQLite app_config 表，可在 Web 设置页可视化修改）；
// 同时兼容旧版 config.yaml：若存在且库内尚无配置，启动时自动一次性导入数据库。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen            string `yaml:"listen" json:"listen"`
	DBPath            string `yaml:"db_path" json:"dbPath"`
	TickDBPath        string `yaml:"tick_db_path" json:"tickDbPath"` // 高频盘口快照独立 SQLite 路径；为空则由 db_path 自动推导（*-tick.db）
	AdminPasswordHash string `yaml:"admin_password_hash" json:"-"`   // 密码哈希单独存库，绝不通过 /api/config 暴露
	// HTTPProxy 仅用于需要代理的出站请求（如 Telegram、海外交易所），A股数据源默认直连。
	HTTPProxy string `yaml:"http_proxy" json:"httpProxy"`
	// BackfillStart 是全量回填的起始日期。
	BackfillStart string  `yaml:"backfill_start" json:"backfillStart"`
	Sources       Sources `yaml:"sources" json:"sources"`
	Stocks        Stocks  `yaml:"stocks" json:"stocks"`
	Funds         Funds   `yaml:"funds" json:"funds"`
	// Plugins 是外部进程插件。可执行文件与参数只能由本机导入或 CLI 配置（Web 只读，防止任意命令执行）。
	Plugins    []Plugin   `yaml:"plugins" json:"plugins"`
	Crypto     Crypto     `yaml:"crypto" json:"crypto"`
	Polymarket Polymarket `yaml:"polymarket" json:"polymarket"`
}

// Polymarket 是预测市场数据配置。Source 为空时不同步。
type Polymarket struct {
	Source      string   `yaml:"source" json:"source"`            // 数据插件名，目前只有 polymarket
	Series      []string `yaml:"series" json:"series"`            // 同步的周期性系列，如 btc-up-or-down-15m
	HistoryDays int      `yaml:"history_days" json:"historyDays"` // 系列已结算事件首次回溯天数，默认 14
	EveryMin    int      `yaml:"every_min" json:"everyMin"`       // 同步间隔（分钟），默认 10
	Proxy       bool     `yaml:"proxy" json:"proxy"`              // 经 http_proxy 访问
	Book        PMBook   `yaml:"book" json:"book"`
	Strategy    PMStrat  `yaml:"strategy" json:"strategy"`
}

// PMStrat 是策略配置。Shadow 为 true 时在盘口采集的每一轮计算短期涨跌盘的公允概率，只记录信号、不下单（需要启用 book）。
type PMStrat struct {
	Shadow  bool    `yaml:"shadow" json:"shadow"`
	MinEdge float64 `yaml:"min_edge" json:"minEdge"` // 记录信号的最小预期收益（每股美元，已扣手续费），默认 0.03
	DF      float64 `yaml:"df" json:"df"`            // Student-t 自由度（0 为正态；推荐 6 启用厚尾校准，缓解尾部过自信）
}

// PMBook 是盘口采集配置：EveryMs 为 0 时不采集。
type PMBook struct {
	EveryMs  int      `yaml:"every_ms" json:"everyMs"`   // 采集间隔（毫秒），如 2500
	Levels   int      `yaml:"levels" json:"levels"`      // 每边保存的档数，默认 10
	KeepDays int      `yaml:"keep_days" json:"keepDays"` // 保留天数，默认 14
	Series   []string `yaml:"series" json:"series"`      // 采集的系列，默认与 series 相同
	WS       bool     `yaml:"ws" json:"ws"`              // 启用 WebSocket 实时盘口与币安现货推送（断线自动回退 REST）
}

// Crypto 是加密货币行情配置。Sources 按顺序回退；为空时不同步。
type Crypto struct {
	Sources  []string                  `yaml:"sources" json:"sources"`
	Symbols  []string                  `yaml:"symbols" json:"symbols"`    // 统一写法，如 BTCUSDT
	History  map[string]int            `yaml:"history" json:"history"`    // 周期 → 首次回填的天数，如 {1d: 3000, 1h: 730}
	EveryMin int                       `yaml:"every_min" json:"everyMin"` // 同步间隔（分钟），默认 15
	Proxy    bool                      `yaml:"proxy" json:"proxy"`        // 经 http_proxy 访问（大陆网络连不上币安等）
	Options  map[string]map[string]any `yaml:"options" json:"options"`
}

// Plugin 是一个外部进程插件：dir 下须有 plugin.json 清单，进程以 dir 为工作目录启动。
type Plugin struct {
	Dir      string            `yaml:"dir" json:"dir"`
	Command  []string          `yaml:"command" json:"command"`    // 可执行文件与参数，如 [python, -I, main.py]
	TimeoutS int               `yaml:"timeout_s" json:"timeoutS"` // 单次调用超时（秒），默认 60
	Config   map[string]string `yaml:"config" json:"config"`      // 下发给插件的配置（Web 上修改后以数据库为准）
	Disabled bool              `yaml:"disabled" json:"disabled"`
}

// Funds 是场外基金净值数据源配置（只同步自选基金）。
type Funds struct {
	Sources []string `yaml:"sources" json:"sources"`
}

// Stocks 是个股 / 场内基金日线数据源配置。Sources 按顺序回退；为空时不同步（本地已有数据仍可用）。
type Stocks struct {
	Sources     []string                  `yaml:"sources" json:"sources"`
	FundSources []string                  `yaml:"fund_sources" json:"fundSources"` // 场内基金单独的数据源顺序，为空同 sources
	SkipFunds   bool                      `yaml:"skip_funds" json:"skipFunds"`     // 不同步场内基金日线（由外部脚本导入等）
	Source      string                    `yaml:"source" json:"source,omitempty"`  // 兼容旧配置：单个数据源
	Concurrency int                       `yaml:"concurrency" json:"concurrency"`
	Options     map[string]map[string]any `yaml:"options" json:"options"` // 插件名 → 插件专属配置
}

// SourceList 返回去重后的数据源顺序（兼容旧的 source 字段）。
func (s Stocks) SourceList() []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range append([]string{s.Source}, s.Sources...) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

type Sources struct {
	// Enabled 为空表示启用全部内置插件。
	Enabled []string `yaml:"enabled" json:"enabled"`
	// Routes：指标 ID → 数据源优先级列表；未配置的指标按"所有声明提供它的插件"路由。
	Routes map[string][]string `yaml:"routes" json:"routes"`
	// Options：插件名 → 插件专属配置。
	Options map[string]map[string]any `yaml:"options" json:"options"`
}

// Default 返回开箱即用的零配置默认参数（所有默认值均在 Web 设置页白盒可观测）。
func Default() *Config {
	return &Config{
		Listen:        "127.0.0.1:8080",
		DBPath:        "data/mt.db",
		BackfillStart: "2005-01-01",
		Sources: Sources{
			Enabled: []string{"eastmoney", "csindex"},
			Routes: map[string][]string{
				"cn.hs300.close":       {"csindex", "eastmoney"},
				"cn.hs300.tr_close":    {"csindex"},
				"cn.hs300.pe_ttm":      {"csindex"},
				"cn.bond.y10":          {"eastmoney"},
				"cn.csiall.amount":     {"csindex"},
				"cn.margin.buy_amount": {"eastmoney"},
				"cn.market.float_mv":   {"eastmoney"},
				"cn.csiall.turnover":   {},
				"cn.market.amount":     {},
			},
			Options: map[string]map[string]any{},
		},
		Stocks: Stocks{
			Sources:     []string{"tdx", "stockdb", "tencent"},
			FundSources: []string{"tdx", "tencent"},
			Concurrency: 4,
			Options: map[string]map[string]any{
				"tdx":     {"connections": 4},
				"stockdb": {"url": "http://127.0.0.1:7899"},
				"tencent": {"interval_ms": 400},
			},
		},
		Funds: Funds{Sources: []string{"eastmoney"}},
		Crypto: Crypto{
			Sources:  []string{"binance", "okx"},
			Symbols:  []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "XRPUSDT"},
			History:  map[string]int{"1d": 3000, "1h": 730, "15m": 180, "5m": 30, "1m": 14},
			EveryMin: 15,
			Options:  map[string]map[string]any{},
		},
		Polymarket: Polymarket{
			Source:      "polymarket",
			Series:      []string{"btc-up-or-down-5m", "btc-up-or-down-15m", "btc-up-or-down-hourly", "eth-up-or-down-5m", "eth-up-or-down-15m", "eth-up-or-down-hourly"},
			HistoryDays: 14,
			EveryMin:    10,
			Book:        PMBook{EveryMs: 2500, Levels: 10, KeepDays: 14, WS: true},
			Strategy:    PMStrat{Shadow: true, MinEdge: 0.03, DF: 6},
		},
	}
}

// Normalize 补全缺省字段并校验合法性。
func (c *Config) Normalize() error {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.DBPath == "" {
		c.DBPath = "data/mt.db"
	}
	if c.BackfillStart == "" {
		c.BackfillStart = "2005-01-01"
	}
	if _, err := time.Parse("2006-01-02", c.BackfillStart); err != nil {
		return fmt.Errorf("backfill_start: %w", err)
	}
	if c.Polymarket.Strategy.DF < 0 || (c.Polymarket.Strategy.DF > 0 && c.Polymarket.Strategy.DF <= 2) {
		return errors.New("polymarket.strategy.df 须为 0（正态）或 > 2（Student-t 厚尾，推荐 5–8）")
	}
	if c.Polymarket.Strategy.MinEdge < 0 || c.Polymarket.Strategy.MinEdge > 0.5 {
		return errors.New("polymarket.strategy.min_edge 须在 0–0.5 之间")
	}
	if c.Polymarket.Book.EveryMs < 0 {
		return errors.New("polymarket.book.every_ms 不能为负")
	}
	if c.Polymarket.Book.Levels <= 0 {
		c.Polymarket.Book.Levels = 10
	}
	if c.Polymarket.Book.KeepDays <= 0 {
		c.Polymarket.Book.KeepDays = 14
	}
	if c.Polymarket.HistoryDays <= 0 {
		c.Polymarket.HistoryDays = 14
	}
	if c.Polymarket.EveryMin <= 0 {
		c.Polymarket.EveryMin = 10
	}
	if c.Crypto.EveryMin <= 0 {
		c.Crypto.EveryMin = 15
	}
	if c.Stocks.Concurrency <= 0 {
		c.Stocks.Concurrency = 4
	}
	if c.Sources.Routes == nil {
		c.Sources.Routes = map[string][]string{}
	}
	if c.Sources.Options == nil {
		c.Sources.Options = map[string]map[string]any{}
	}
	if c.Stocks.Options == nil {
		c.Stocks.Options = map[string]map[string]any{}
	}
	if c.Crypto.History == nil {
		c.Crypto.History = map[string]int{}
	}
	if c.Crypto.Options == nil {
		c.Crypto.Options = map[string]map[string]any{}
	}
	return nil
}

// Clone 深拷贝配置。
func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	out := &Config{}
	_ = json.Unmarshal(b, out)
	out.AdminPasswordHash = c.AdminPasswordHash
	return out
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// BackfillStartTime 返回解析后的回填起始日期。
func (c *Config) BackfillStartTime() time.Time {
	t, _ := time.Parse("2006-01-02", c.BackfillStart)
	return t
}
