// Package eastmoney 是东方财富数据插件。
//
// 注意：push2his（K 线）有严格限流，连续约 50 次请求就会被断开。本插件每个指标只发一次 K 线请求
// （一次拉取整个区间），不要用它批量拉个股。datacenter 接口不受此限制。
package eastmoney

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/plugin"
)

const Name = "eastmoney"

func init() {
	plugin.MustRegister(plugin.Manifest{Name: Name, Kind: plugin.KindMetric, Label: "东方财富",
		Description: "国债收益率、两融、流通市值（datacenter）；K 线（push2his）会封 IP，失败后短路 10 分钟"})
	ds.Register(Name, func(env ds.Env) (ds.Source, error) {
		return &Source{http: env.HTTP, now: env.Now}, nil
	})
}

type Source struct {
	http *http.Client
	now  func() time.Time

	mu                sync.Mutex
	klineBlockedUntil time.Time
}

func (s *Source) Name() string { return Name }

func (s *Source) Provides() []ds.MetricID {
	return []ds.MetricID{ds.CNHS300Close, ds.CNCSIAllTurnover, ds.CNMarketAmount, ds.CNBondY10, ds.CNMarginBuy, ds.CNMarketFloatMV}
}

func (s *Source) Fetch(ctx context.Context, m ds.MetricID, from, to time.Time) ([]ds.Point, error) {
	var (
		pts []ds.Point
		err error
	)
	switch m {
	case ds.CNHS300Close:
		pts, err = s.klineField(ctx, "1.000300", from, to, func(b Bar) float64 { return b.Close })
	case ds.CNCSIAllTurnover:
		pts, err = s.klineField(ctx, "1.000985", from, to, func(b Bar) float64 { return b.Turnover })
	case ds.CNMarketAmount:
		pts, err = s.marketAmount(ctx, from, to)
	case ds.CNBondY10:
		pts, err = s.datacenter(ctx, "RPTA_WEB_TREASURYYIELD", "SOLAR_DATE", "EMM00166466", from, to)
	case ds.CNMarginBuy:
		pts, err = s.datacenter(ctx, "RPTA_RZRQ_LSHJ", "DIM_DATE", "RZMRE", from, to)
	case ds.CNMarketFloatMV:
		pts, err = s.datacenter(ctx, "RPTA_RZRQ_LSHJ", "DIM_DATE", "LTSZ", from, to)
	default:
		return nil, fmt.Errorf("eastmoney: unsupported metric %s", m)
	}
	if err != nil {
		return nil, err
	}
	return ds.ClipFinal(pts, s.now()), nil
}

// ---- K 线 ----

// Bar 是东财日 K 线的一根。
type Bar struct {
	Date     string
	Close    float64
	Amount   float64 // 成交额，元
	Turnover float64 // 换手率，%
}

const klineURL = "https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=%s" +
	"&fields1=f1,f2,f3&fields2=f51,f52,f53,f54,f55,f56,f57,f61&klt=101&fqt=0&beg=%s&end=%s"

// klineCooldown：K 线请求失败后，在此时间内不再请求 push2his，直接失败（交给路由回退）。
const klineCooldown = 10 * time.Minute

func (s *Source) klines(ctx context.Context, secid string, from, to time.Time) ([]Bar, error) {
	s.mu.Lock()
	until := s.klineBlockedUntil
	s.mu.Unlock()
	if now := s.now(); now.Before(until) {
		return nil, fmt.Errorf("kline %s: push2his 疑似限流，%s 前暂停请求", secid, until.In(ds.Shanghai).Format("15:04"))
	}
	u := fmt.Sprintf(klineURL, secid, from.Format("20060102"), to.Format("20060102"))
	body, err := ds.GetBodyOnce(ctx, s.http, u, nil)
	if err != nil {
		if ctx.Err() == nil {
			s.mu.Lock()
			s.klineBlockedUntil = s.now().Add(klineCooldown)
			s.mu.Unlock()
		}
		return nil, fmt.Errorf("kline %s: %w", secid, err)
	}
	return ParseKlines(body)
}

// ParseKlines 解析 K 线响应。字段顺序：日期,开,收,高,低,量,额,换手率。
func ParseKlines(body []byte) ([]Bar, error) {
	var r struct {
		RC   int `json:"rc"`
		Data *struct {
			Klines []string `json:"klines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("kline: decode: %w", err)
	}
	if r.Data == nil {
		return nil, fmt.Errorf("kline: empty data (rc=%d)", r.RC)
	}
	out := make([]Bar, 0, len(r.Data.Klines))
	for _, line := range r.Data.Klines {
		f := strings.Split(line, ",")
		if len(f) < 8 {
			return nil, fmt.Errorf("kline: bad line %q", line)
		}
		b := Bar{Date: f[0]}
		var e1, e2, e3 error
		b.Close, e1 = strconv.ParseFloat(f[2], 64)
		b.Amount, e2 = strconv.ParseFloat(f[6], 64)
		b.Turnover, e3 = strconv.ParseFloat(f[7], 64)
		if err := errors.Join(e1, e2, e3); err != nil {
			return nil, fmt.Errorf("kline: bad line %q: %w", line, err)
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *Source) klineField(ctx context.Context, secid string, from, to time.Time, pick func(Bar) float64) ([]ds.Point, error) {
	bars, err := s.klines(ctx, secid, from, to)
	if err != nil {
		return nil, err
	}
	pts := make([]ds.Point, len(bars))
	for i, b := range bars {
		pts[i] = ds.Point{Date: b.Date, Value: pick(b)}
	}
	return pts, nil
}

// marketAmount = 上证指数成交额 + 深证成指成交额，只保留两边都有的日期。
func (s *Source) marketAmount(ctx context.Context, from, to time.Time) ([]ds.Point, error) {
	sh, err := s.klines(ctx, "1.000001", from, to)
	if err != nil {
		return nil, err
	}
	sz, err := s.klines(ctx, "0.399001", from, to)
	if err != nil {
		return nil, err
	}
	return SumAmount(sh, sz), nil
}

// SumAmount 按日期合并两个市场的成交额。
func SumAmount(a, b []Bar) []ds.Point {
	m := make(map[string]float64, len(b))
	for _, x := range b {
		m[x.Date] = x.Amount
	}
	var out []ds.Point
	for _, x := range a {
		if v, ok := m[x.Date]; ok {
			out = append(out, ds.Point{Date: x.Date, Value: x.Amount + v})
		}
	}
	return out
}

// ---- datacenter ----

const datacenterURL = "https://datacenter-web.eastmoney.com/api/data/v1/get"

func (s *Source) datacenter(ctx context.Context, report, dateCol, valCol string, from, to time.Time) ([]ds.Point, error) {
	var out []ds.Point
	for page := 1; ; page++ {
		q := url.Values{
			"reportName":  {report},
			"columns":     {dateCol + "," + valCol},
			"sortColumns": {dateCol},
			"sortTypes":   {"1"},
			"pageSize":    {"500"},
			"pageNumber":  {strconv.Itoa(page)},
			"filter": {fmt.Sprintf("(%s>='%s')(%s<='%s')",
				dateCol, from.Format("2006-01-02"), dateCol, to.Format("2006-01-02"))},
		}
		body, err := ds.GetBody(ctx, s.http, datacenterURL+"?"+q.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("datacenter %s: %w", report, err)
		}
		pts, pages, err := ParseDatacenter(body, dateCol, valCol)
		if err != nil {
			return nil, fmt.Errorf("datacenter %s: %w", report, err)
		}
		out = append(out, pts...)
		if page >= pages {
			return out, nil
		}
	}
}

// ParseDatacenter 解析 datacenter 响应，返回数据点和总页数。值为 null 的行跳过。
func ParseDatacenter(body []byte, dateCol, valCol string) ([]ds.Point, int, error) {
	var r struct {
		Success bool   `json:"success"`
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  *struct {
			Pages int                          `json:"pages"`
			Data  []map[string]json.RawMessage `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, fmt.Errorf("decode: %w", err)
	}
	if r.Result == nil {
		if r.Code == 9201 { // 返回数据为空
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("code %d: %s", r.Code, r.Message)
	}
	out := make([]ds.Point, 0, len(r.Result.Data))
	for _, row := range r.Result.Data {
		var date string
		var val *float64
		if err := json.Unmarshal(row[dateCol], &date); err != nil || len(date) < 10 {
			return nil, 0, fmt.Errorf("bad %s: %s", dateCol, row[dateCol])
		}
		if raw, ok := row[valCol]; ok {
			if err := json.Unmarshal(raw, &val); err != nil {
				return nil, 0, fmt.Errorf("bad %s: %s", valCol, raw)
			}
		}
		if val == nil {
			continue
		}
		out = append(out, ds.Point{Date: date[:10], Value: *val})
	}
	return out, r.Result.Pages, nil
}
