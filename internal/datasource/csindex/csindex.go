// Package csindex 是中证指数官网数据插件：沪深300 PE(TTM)、收盘价，中证全指成交额。
// perf 接口实测不受东财那样的限流影响，可作为指数收盘价的备用源。
package csindex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	ds "market-thermometer/internal/datasource"
)

const Name = "csindex"

func init() {
	ds.Register(Name, func(env ds.Env) (ds.Source, error) {
		return &Source{http: env.HTTP, now: env.Now}, nil
	})
}

type Source struct {
	http *http.Client
	now  func() time.Time
}

func (s *Source) Name() string { return Name }
func (s *Source) Provides() []ds.MetricID {
	return []ds.MetricID{ds.CNHS300PE, ds.CNHS300Close, ds.CNCSIAllAmount}
}

const perfURL = "https://www.csindex.com.cn/csindex-home/perf/index-perf?indexCode=%s&startDate=%s&endDate=%s"

func (s *Source) Fetch(ctx context.Context, m ds.MetricID, from, to time.Time) ([]ds.Point, error) {
	var (
		code string
		pick func(Row) *float64
	)
	switch m {
	case ds.CNHS300PE:
		code, pick = "000300", func(r Row) *float64 { return r.PE }
	case ds.CNHS300Close:
		code, pick = "000300", func(r Row) *float64 { return r.Close }
	case ds.CNCSIAllAmount:
		code, pick = "000985", func(r Row) *float64 {
			if r.TradingValue == nil {
				return nil
			}
			v := *r.TradingValue * 1e8 // 接口单位为亿元
			return &v
		}
	default:
		return nil, fmt.Errorf("csindex: unsupported metric %s", m)
	}
	u := fmt.Sprintf(perfURL, code, from.Format("20060102"), to.Format("20060102"))
	body, err := ds.GetBody(ctx, s.http, u, nil)
	if err != nil {
		return nil, fmt.Errorf("csindex perf %s: %w", code, err)
	}
	rows, err := ParsePerf(body)
	if err != nil {
		return nil, err
	}
	return ds.ClipFinal(Points(rows, pick), s.now()), nil
}

// Row 是 index-perf 的一行。字段可能为 null。
type Row struct {
	Date         string   // YYYY-MM-DD
	Close        *float64 `json:"close"`
	PE           *float64 `json:"peg"`          // 实测与 PE(TTM) 接近，口径待核实（见设计文档 §2）
	TradingValue *float64 `json:"tradingValue"` // 亿元
}

// ParsePerf 解析 index-perf 响应，丢弃周末行（接口会返回 20000101 这类非交易日行）。
func ParsePerf(body []byte) ([]Row, error) {
	var r struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			TradeDate string `json:"tradeDate"`
			Row
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("csindex: decode: %w", err)
	}
	if r.Code != "200" {
		return nil, fmt.Errorf("csindex: code %s: %s", r.Code, r.Msg)
	}
	out := make([]Row, 0, len(r.Data))
	for _, x := range r.Data {
		t, err := time.Parse("20060102", x.TradeDate)
		if err != nil {
			return nil, fmt.Errorf("csindex: bad tradeDate %q", x.TradeDate)
		}
		if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		row := x.Row
		row.Date = t.Format("2006-01-02")
		out = append(out, row)
	}
	return out, nil
}

// Points 取出某一字段，跳过空值和非正值。
func Points(rows []Row, pick func(Row) *float64) []ds.Point {
	out := make([]ds.Point, 0, len(rows))
	for _, r := range rows {
		if v := pick(r); v != nil && *v > 0 {
			out = append(out, ds.Point{Date: r.Date, Value: *v})
		}
	}
	return out
}
