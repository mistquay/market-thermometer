// Package okx 是 OKX 现货公共行情插件（加密货币 K 线与最新价，无需 API Key），作为币安的备用源。
//
// 接口：/api/v5/market/history-candles（每次最多 100 根，按时间倒序，用 after 向前翻页）、/api/v5/market/ticker。
// 日线用 UTC 对齐的 1Dutc（OKX 的 1D 按北京时间对齐）。公共接口限速每 2 秒 20 次，本插件每次请求间隔 120ms。
package okx

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

const Name = "okx"

var bars = map[string]string{"1m": "1m", "5m": "5m", "15m": "15m", "1h": "1H", "4h": "4Hutc", "1d": "1Dutc"}

func init() {
	plugin.MustRegister(plugin.Manifest{Name: Name, Kind: plugin.KindCrypto, Label: "OKX 现货",
		Description:  "OKX 公共行情：加密货币 K 线（1m–1d，UTC 对齐）与最新价，无需 API Key；作为币安的备用源",
		Capabilities: []string{"klines", "price"}})
	ds.RegisterKline(Name, func(env ds.Env) (ds.KlineSource, error) {
		return &Source{base: "https://www.okx.com", http: env.HTTP, now: env.Now}, nil
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

// InstID 把统一写法 BTCUSDT 转成 OKX 的 BTC-USDT。
func InstID(symbol string) string {
	for _, q := range []string{"USDT", "USDC", "USD", "BTC", "ETH"} {
		if strings.HasSuffix(symbol, q) && len(symbol) > len(q) {
			return symbol[:len(symbol)-len(q)] + "-" + q
		}
	}
	return symbol
}

func (s *Source) get(ctx context.Context, path string) ([][]string, error) {
	s.mu.Lock()
	wait := time.Until(s.next)
	s.next = time.Now().Add(max(wait, 0) + 120*time.Millisecond)
	s.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	body, err := ds.GetBody(ctx, s.http, s.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("okx: %w", err)
	}
	var r struct {
		Code string            `json:"code"`
		Msg  string            `json:"msg"`
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("okx: decode %.200s: %w", body, err)
	}
	if r.Code != "0" {
		return nil, fmt.Errorf("okx: code %s %s", r.Code, r.Msg)
	}
	out := make([][]string, 0, len(r.Data))
	for _, d := range r.Data {
		var row []string
		if err := json.Unmarshal(d, &row); err != nil {
			// ticker 返回对象而不是数组
			var obj map[string]string
			if json.Unmarshal(d, &obj) == nil {
				out = append(out, []string{obj["last"]})
				continue
			}
			return nil, fmt.Errorf("okx: bad row %s", d)
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Source) Klines(ctx context.Context, symbol, interval string, from, to int64) ([]ds.Kline, error) {
	bar, ok := bars[interval]
	if !ok {
		return nil, fmt.Errorf("okx: unsupported interval %q", interval)
	}
	var out []ds.Kline
	for after := to; after > from; {
		rows, err := s.get(ctx, fmt.Sprintf("/api/v5/market/history-candles?instId=%s&bar=%s&after=%d&limit=100", InstID(symbol), bar, after))
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		oldest := after
		for _, r := range rows {
			k, err := parse(r)
			if err != nil {
				return nil, fmt.Errorf("okx %s: %w", symbol, err)
			}
			if k.T >= from && k.T < to {
				out = append(out, k)
			}
			oldest = min(oldest, k.T)
		}
		if len(rows) < 100 || oldest >= after {
			break
		}
		after = oldest
	}
	return ds.ClosedKlines(out, interval, s.now()), nil
}

// parse 解析 [ts, o, h, l, c, vol, volCcy, volCcyQuote, confirm]。
func parse(r []string) (ds.Kline, error) {
	var k ds.Kline
	if len(r) < 8 {
		return k, fmt.Errorf("bad kline %v", r)
	}
	t, err := strconv.ParseInt(r[0], 10, 64)
	if err != nil {
		return k, err
	}
	k.T = t
	for i, dst := range []*float64{&k.Open, &k.High, &k.Low, &k.Close, &k.Volume} {
		if *dst, err = strconv.ParseFloat(r[i+1], 64); err != nil {
			return k, err
		}
	}
	k.Quote, err = strconv.ParseFloat(r[7], 64)
	return k, err
}

func (s *Source) Price(ctx context.Context, symbol string) (float64, error) {
	rows, err := s.get(ctx, "/api/v5/market/ticker?instId="+InstID(symbol))
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return 0, fmt.Errorf("okx: empty ticker for %s", symbol)
	}
	return strconv.ParseFloat(rows[0][0], 64)
}
