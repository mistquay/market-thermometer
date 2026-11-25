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
	AdminPasswordHash string `yaml:"admin_password_hash"`
	// HTTPProxy 仅用于需要代理的出站请求（如 Telegram），A股数据源默认直连。
	HTTPProxy string `yaml:"http_proxy"`
	// BackfillStart 是全量回填的起始日期。
	BackfillStart string  `yaml:"backfill_start"`
	Sources       Sources `yaml:"sources"`
	Stocks        Stocks  `yaml:"stocks"`
}

// Stocks 是个股 / 场内基金日线数据源配置。Sources 按顺序回退；为空时不同步（本地已有数据仍可用）。
type Stocks struct {
	Sources     []string                  `yaml:"sources"`
	FundSources []string                  `yaml:"fund_sources"` // 场内基金单独的数据源顺序，为空同 sources
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
