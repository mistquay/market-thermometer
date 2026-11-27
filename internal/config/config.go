// Package config 读取启动级配置（config.yaml）。Web 上可修改的配置存数据库，不在这里。
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen            string `yaml:"listen"`
	DBPath            string `yaml:"db_path"`
	TickDBPath        string `yaml:"tick_db_path"` // 高频盘口快照独立 SQLite 路径；为空则由 db_path 自动推导（*-tick.db）
	AdminPasswordHash string `yaml:"admin_password_hash"`
	// HTTPProxy 仅用于需要代理的出站请求（如 Telegram），A股数据源默认直连。
	HTTPProxy string `yaml:"http_proxy"`
	// BackfillStart 是全量回填的起始日期。
	BackfillStart string  `yaml:"backfill_start"`
	Sources       Sources `yaml:"sources"`
	Stocks        Stocks  `yaml:"stocks"`
	Funds         Funds   `yaml:"funds"`
	// Plugins 是外部进程插件。可执行文件与参数只能在这里配置（Web 不能新增或修改）。
	Plugins    []Plugin   `yaml:"plugins"`
	Crypto     Crypto     `yaml:"crypto"`
	Polymarket Polymarket `yaml:"polymarket"`
}

// Polymarket 是预测市场数据配置。Source 为空时不同步。
type Polymarket struct {
	Source      string   `yaml:"source"`       // 数据插件名，目前只有 polymarket
	Series      []string `yaml:"series"`       // 同步的周期性系列，如 btc-up-or-down-15m
	HistoryDays int      `yaml:"history_days"` // 系列已结算事件首次回溯天数，默认 30
	EveryMin    int      `yaml:"every_min"`    // 同步间隔（分钟），默认 10
	Proxy       bool     `yaml:"proxy"`        // 经 http_proxy 访问
	Book        PMBook   `yaml:"book"`
	Strategy    PMStrat  `yaml:"strategy"`
}

// PMStrat 是策略配置。Shadow 为 true 时在盘口采集的每一轮计算短期涨跌盘的公允概率，只记录信号、不下单（需要启用 book）。
type PMStrat struct {
	Shadow  bool    `yaml:"shadow"`
	MinEdge float64 `yaml:"min_edge"` // 记录信号的最小预期收益（每股美元，已扣手续费），默认 0.03
}

// PMBook 是盘口采集配置：EveryMs 为 0 时不采集。
type PMBook struct {
	EveryMs  int      `yaml:"every_ms"`  // 采集间隔（毫秒），如 2500
	Levels   int      `yaml:"levels"`    // 每边保存的档数，默认 10
	KeepDays int      `yaml:"keep_days"` // 保留天数，默认 14
	Series   []string `yaml:"series"`    // 采集的系列，默认与 series 相同
	WS       bool     `yaml:"ws"`        // 启用 WebSocket 实时盘口与币安现货推送（断线自动回退 REST）
}

// Crypto 是加密货币行情配置。Sources 按顺序回退；为空时不同步。
type Crypto struct {
	Sources  []string                  `yaml:"sources"`
	Symbols  []string                  `yaml:"symbols"`   // 统一写法，如 BTCUSDT
	History  map[string]int            `yaml:"history"`   // 周期 → 首次回填的天数，如 {1d: 3000, 1h: 730}
	EveryMin int                       `yaml:"every_min"` // 同步间隔（分钟），默认 15
	Proxy    bool                      `yaml:"proxy"`     // 经 http_proxy 访问（大陆网络连不上币安等）
	Options  map[string]map[string]any `yaml:"options"`
}

// Plugin 是一个外部进程插件：dir 下须有 plugin.json 清单，进程以 dir 为工作目录启动。
type Plugin struct {
	Dir      string            `yaml:"dir"`
	Command  []string          `yaml:"command"`   // 可执行文件与参数，如 [python, -I, main.py]
	TimeoutS int               `yaml:"timeout_s"` // 单次调用超时（秒），默认 60
	Config   map[string]string `yaml:"config"`    // 下发给插件的配置（Web 上修改后以数据库为准）
	Disabled bool              `yaml:"disabled"`
}

// Funds 是场外基金净值数据源配置（只同步自选基金）。
type Funds struct {
	Sources []string `yaml:"sources"`
}

// Stocks 是个股 / 场内基金日线数据源配置。Sources 按顺序回退；为空时不同步（本地已有数据仍可用）。
type Stocks struct {
	Sources     []string                  `yaml:"sources"`
	FundSources []string                  `yaml:"fund_sources"` // 场内基金单独的数据源顺序，为空同 sources
	SkipFunds   bool                      `yaml:"skip_funds"`   // 不同步场内基金日线（由外部脚本导入等）
	Source      string                    `yaml:"source"`       // 兼容旧配置：单个数据源
	Concurrency int                       `yaml:"concurrency"`
	Options     map[string]map[string]any `yaml:"options"` // 插件名 → 插件专属配置
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
	Enabled []string `yaml:"enabled"`
	// Routes：指标 ID → 数据源优先级列表；未配置的指标按"所有声明提供它的插件"路由。
	Routes map[string][]string `yaml:"routes"`
	// Options：插件名 → 插件专属配置。
	Options map[string]map[string]any `yaml:"options"`
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
		return nil, fmt.Errorf("backfill_start: %w", err)
	}
	return c, nil
}

// BackfillStartTime 返回解析后的回填起始日期。
func (c *Config) BackfillStartTime() time.Time {
	t, _ := time.Parse("2006-01-02", c.BackfillStart)
	return t
}
