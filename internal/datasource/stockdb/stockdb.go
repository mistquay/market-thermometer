// Package stockdb 是本地 free-stockdb 的个股数据插件（HTTP 接口，默认 127.0.0.1:7899）。
//
// 接口要点（2026-11 实测，v0.3.5）：
//   - 必须带 json=1，否则返回 MessagePack；
//   - 键里的 ':' ',' 不能百分号编码（编码后查不到），只编码中文表名；
//   - 日k 的 close 为不复权价，pre_close 口径不一致（不使用）；除权信息在「复权」表的 mult 字段；
//   - 不要发全市场（k1=*）查询：极慢，且客户端断开后服务端仍继续计算。
package stockdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/plugin"
)

const Name = "stockdb"

func init() {
	plugin.MustRegister(plugin.Manifest{Name: Name, Kind: plugin.KindStock, Label: "free-stockdb（本机）",
		Description:  "本机离线数据库，提供证券清单（含退市）、不复权日线与除权乘数；需先启动 stockdb.exe",
		Capabilities: []string{"list", "bars"},
		Config:       []plugin.Field{{Key: "url", Label: "接口地址", Help: "默认 http://127.0.0.1:7899"}}})
	ds.RegisterStock(Name, func(env ds.Env) (ds.StockSource, error) {
		base := "http://127.0.0.1:7899"
		if v, ok := env.Options["url"].(string); ok && v != "" {
			base = strings.TrimRight(v, "/")
		}
		if _, err := url.Parse(base); err != nil {
			return nil, fmt.Errorf("stockdb url: %w", err)
		}
		return &Source{base: base, http: env.HTTP, now: env.Now}, nil
	})
}

type Source struct {
	base string
	http *http.Client
	now  func() time.Time
}

func (s *Source) Name() string { return Name }

var (
	tblDaily  = url.QueryEscape("日k")
	tblAdj    = url.QueryEscape("复权")
	tblCodes  = url.QueryEscape("股票代码")
	tblDelist = url.QueryEscape("退市") + "*"
)

func (s *Source) get(ctx context.Context, query string, v any) error {
	body, err := ds.GetBody(ctx, s.http, s.base+"/?json=1&"+query, nil)
	if err != nil {
		return fmt.Errorf("stockdb: %w（stockdb.exe 是否已启动？）", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("stockdb: decode: %w", err)
	}
	return nil
}

func (s *Source) ListStocks(ctx context.Context) ([]ds.Stock, error) {
	var groups map[string][]string
	if err := s.get(ctx, "cmd=get&t="+tblCodes, &groups); err != nil {
		return nil, err
	}
	var delisted []json.RawMessage
	if err := s.get(ctx, "cmd=vals&t="+tblDelist, &delisted); err != nil {
		return nil, err
	}
	return mergeStocks(groups, delisted), nil
}

// mergeStocks 合并在市与退市清单，只保留 A 股与场内基金，按代码排序。退市清单里非字符串的项忽略。
func mergeStocks(groups map[string][]string, delisted []json.RawMessage) []ds.Stock {
	seen := map[string]bool{}
	var out []ds.Stock
	for _, codes := range groups {
		for _, c := range codes {
			if k := ds.KindOf(c); k != "" && !seen[c] {
				seen[c] = true
				out = append(out, ds.Stock{Code: c, Kind: k})
			}
		}
	}
	for _, raw := range delisted {
		var c string
		if json.Unmarshal(raw, &c) != nil || ds.KindOf(c) == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, ds.Stock{Code: c, Kind: ds.KindOf(c), Delisted: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func compact(date string) string { return strings.ReplaceAll(date, "-", "") }

func (s *Source) Bars(ctx context.Context, code, from, to string) ([]ds.StockBar, error) {
	if ds.KindOf(code) == "" {
		return nil, fmt.Errorf("stockdb: bad code %q", code)
	}
	k2 := "all:"
	if from != "" || to != "" {
		if from == "" {
			from = "1990-01-01"
		}
		if to == "" {
			to = "2099-12-31"
		}
		k2 = "fwd:" + compact(from) + "," + compact(to)
	}
	var rows []json.RawMessage
	if err := s.get(ctx, "cmd=vals&t="+tblDaily+"&k1=key:"+code+"&k2="+k2, &rows); err != nil {
		return nil, err
	}
	var adj [][2]json.RawMessage
	if err := s.get(ctx, "cmd=get&t="+tblAdj+"&k1=key:"+code+"&k2=all:", &adj); err != nil {
		return nil, err
	}
	bars, err := parseBars(rows, adj)
	if err != nil {
		return nil, fmt.Errorf("stockdb %s: %w", code, err)
	}
	return clipFinal(bars, s.now()), nil
}

// parseBars 把日 K 与「复权」表合并。复权表为 [key, {mult,...}] 对，key 形如 "复权:600000:20250716"。
func parseBars(rows []json.RawMessage, adj [][2]json.RawMessage) ([]ds.StockBar, error) {
	mult := map[string]float64{}
	for _, pair := range adj {
		var key string
		var v struct {
			Mult float64 `json:"mult"`
		}
		if json.Unmarshal(pair[0], &key) != nil || json.Unmarshal(pair[1], &v) != nil {
			return nil, fmt.Errorf("bad adj pair %s", pair[0])
		}
		i := strings.LastIndexByte(key, ':')
		if d, ok := fmtDate(key[i+1:]); ok && v.Mult > 0 {
			mult[d] = v.Mult
		}
	}
	out := make([]ds.StockBar, 0, len(rows))
	for _, raw := range rows {
		var r struct {
			Date   json.Number `json:"date"`
			Close  float64     `json:"close"`
			Volume float64     `json:"volume"`
		}
		if string(raw) == "null" {
			continue
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("bad bar %.100s: %w", raw, err)
		}
		d, ok := fmtDate(r.Date.String())
		if !ok {
			return nil, fmt.Errorf("bad date %q", r.Date)
		}
		if r.Close <= 0 || r.Volume <= 0 { // 停牌或无效
			continue
		}
		m := mult[d]
		if m == 0 {
			m = 1
		}
		out = append(out, ds.StockBar{Date: d, Close: r.Close, Mult: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

func fmtDate(s string) (string, bool) {
	if len(s) != 8 {
		return "", false
	}
	if _, err := strconv.Atoi(s); err != nil {
		return "", false
	}
	return s[:4] + "-" + s[4:6] + "-" + s[6:], true
}

func clipFinal(bars []ds.StockBar, now time.Time) []ds.StockBar {
	last := ds.LatestFinalDate(now)
	for len(bars) > 0 && bars[len(bars)-1].Date > last {
		bars = bars[:len(bars)-1]
	}
	return bars
}
