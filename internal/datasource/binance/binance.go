// Package binance 是币安现货公共行情插件（加密货币 K 线与最新价，无需 API Key）。
//
// 接口：/api/v3/klines（每次最多 1000 根）、/api/v3/ticker/price。按 IP 计权重限流（每分钟 6000），
// 本插件每次请求间隔至少 100ms。大陆网络无法直连，需在海外服务器或经代理运行。
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/plugin"
)

const Name = "binance"

func init() {
	plugin.MustRegister(plugin.Manifest{Name: Name, Kind: plugin.KindCrypto, Label: "币安现货",
		Description:  "币安公共行情：加密货币 K 线（1m–1d）与最新价，无需 API Key；大陆网络需代理或海外服务器",
		Capabilities: []string{"klines", "price"},
		Config:       []plugin.Field{{Key: "base", Label: "接口地址", Help: "默认 https://api.binance.com"}}})
	ds.RegisterKline(Name, func(env ds.Env) (ds.KlineSource, error) {
		base := "https://api.binance.com"
		if v, ok := env.Options["base"].(string); ok && v != "" {
			base = strings.TrimRight(v, "/")
		}
		return &Source{base: base, http: env.HTTP, now: env.Now}, nil
	})
}

type Source struct {
	base string
	http *http.Client
	now  func() time.Time

	mu   sync.Mutex
	next time.Time
}

func (s *Source) Name() string { return Name }

func (s *Source) get(ctx context.Context, path string, v any) error {
	s.mu.Lock()
	wait := time.Until(s.next)
	s.next = time.Now().Add(max(wait, 0) + 100*time.Millisecond)
	s.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	body, err := ds.GetBody(ctx, s.http, s.base+path, nil)
	if err != nil {
		return fmt.Errorf("binance: %w", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("binance: decode %.200s: %w", body, err)
	}
	return nil
}

func (s *Source) Klines(ctx context.Context, symbol, interval string, from, to int64) ([]ds.Kline, error) {
	d, ok := ds.KlineIntervals[interval]
	if !ok {
		return nil, fmt.Errorf("binance: unsupported interval %q", interval)
	}
	var out []ds.Kline
	for start := from; start < to; {
		var rows [][]json.RawMessage
		q := fmt.Sprintf("/api/v3/klines?symbol=%s&interval=%s&startTime=%d&endTime=%d&limit=1000", symbol, interval, start, to-1)
		if err := s.get(ctx, q, &rows); err != nil {
			return nil, err
		}
		page, err := parse(rows)
		if err != nil {
			return nil, fmt.Errorf("binance %s: %w", symbol, err)
		}
		out = append(out, page...)
		if len(page) < 1000 {
			break
		}
		start = page[len(page)-1].T + d.Milliseconds()
	}
	return ds.ClosedKlines(out, interval, s.now()), nil
}

// parse 解析 [开盘时间, 开, 高, 低, 收, 量, 收盘时间, 成交额, ...]，价格为字符串。
func parse(rows [][]json.RawMessage) ([]ds.Kline, error) {
	out := make([]ds.Kline, 0, len(rows))
	for _, r := range rows {
		if len(r) < 8 {
			return nil, fmt.Errorf("bad kline %s", r)
		}
		var k ds.Kline
		if err := json.Unmarshal(r[0], &k.T); err != nil {
			return nil, err
		}
		for i, dst := range []*float64{&k.Open, &k.High, &k.Low, &k.Close, &k.Volume} {
			if err := num(r[i+1], dst); err != nil {
				return nil, err
			}
		}
		if err := num(r[7], &k.Quote); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

func num(raw json.RawMessage, dst *float64) error {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	v, err := strconv.ParseFloat(s, 64)
	*dst = v
	return err
}

func (s *Source) Price(ctx context.Context, symbol string) (float64, error) {
	var r struct {
		Price string `json:"price"`
	}
	if err := s.get(ctx, "/api/v3/ticker/price?symbol="+symbol, &r); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(r.Price, 64)
}
