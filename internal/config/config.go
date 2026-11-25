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

// Stocks 是个股数据源配置（用于市场广度）。Source 为空时不计算市场广度。
type Stocks struct {
	Source      string         `yaml:"source"`
	Concurrency int            `yaml:"concurrency"`
	Options     map[string]any `yaml:"options"`
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
