// Package tencent 是腾讯行情的个股 / 场内基金日线插件（web.ifzq.gtimg.cn fqkline），作为备用源。
//
// 实测（2026-11）：
//   - 连续 200 次请求未被限流，单次约 0.4 秒；单次最多返回区间内最新 640 根，需向前分页；
//   - 腾讯的“后复权”序列在非除权日也与不复权涨跌不一致（误差可达 2%），不能用来推算除权乘数；
//   - 不复权日线在股票除权日带分红送转标注（如 {"cqr":"2025-07-16","FHcontent":"10派4.1元"}），
//     据此按交易所公式计算除权参考价与乘数；ETF 分红没有标注，乘数按 1 处理（误差约为年分红率）；
//   - 不提供证券清单（ListStocks 返回 ErrNotSupported），由本地清单或其他数据源提供。
package tencent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	ds "market-thermometer/internal/datasource"
)

const Name = "tencent"

func init() {
	ds.RegisterStock(Name, func(env ds.Env) (ds.StockSource, error) {
		return &Source{http: env.HTTP, now: env.Now}, nil
	})
}

type Source struct {
	http *http.Client
	now  func() time.Time
}

func (s *Source) Name() string { return Name }

func (s *Source) ListStocks(context.Context) ([]ds.Stock, error) { return nil, ds.ErrNotSupported }

const (
	klineURL = "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s,day,%s,%s,%d,"
	pageSize = 640
)

func (s *Source) Bars(ctx context.Context, code, from, to string) ([]ds.StockBar, error) {
	prefix := ds.MarketPrefix(code)
	if prefix == "" || ds.KindOf(code) == "" {
		return nil, fmt.Errorf("tencent: bad code %q", code)
	}
	if from == "" {
		from = "1990-01-01"
	}
	if to == "" {
		to = s.now().In(ds.Shanghai).Format("2006-01-02")
	}
	var all []rawBar
	for end := to; ; {
		body, err := ds.GetBody(ctx, s.http, fmt.Sprintf(klineURL, prefix+code, from, end, pageSize), nil)
		if err != nil {
			return nil, fmt.Errorf("tencent %s: %w", code, err)
		}
		page, err := parseKline(body, prefix+code)
		if err != nil {
			return nil, fmt.Errorf("tencent %s: %w", code, err)
		}
		all = append(page, all...)
		if len(page) < pageSize || page[0].date <= from {
			break
		}
		t, _ := time.Parse("2006-01-02", page[0].date)
		end = t.AddDate(0, 0, -1).Format("2006-01-02")
	}
	return clipFinal(toBars(all), s.now()), nil
}

type rawBar struct {
	date   string
	close  float64
	volume float64
	fh     string // 分红送转说明，如 "10派4.1元"
}

// parseKline 解析 fqkline 响应。每根 K 线是 [日期, 开, 收, 高, 低, 量, {分红标注}?]，数值为字符串。
func parseKline(body []byte, key string) ([]rawBar, error) {
	var r struct {
		Code int                                   `json:"code"`
		Msg  string                                `json:"msg"`
		Data map[string]map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("code %d: %s", r.Code, r.Msg)
	}
	var rows [][]json.RawMessage
	if raw, ok := r.Data[key]["day"]; ok {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("decode day: %w", err)
		}
	}
	out := make([]rawBar, 0, len(rows))
	for _, row := range rows {
		if len(row) < 6 {
			return nil, fmt.Errorf("bad bar %s", row)
		}
		var date, cls, vol string
		if json.Unmarshal(row[0], &date) != nil || json.Unmarshal(row[2], &cls) != nil || json.Unmarshal(row[5], &vol) != nil {
			return nil, fmt.Errorf("bad bar %s", row)
		}
		b := rawBar{date: date}
		var e1, e2 error
		b.close, e1 = strconv.ParseFloat(cls, 64)
		b.volume, e2 = strconv.ParseFloat(vol, 64)
		if e1 != nil || e2 != nil {
			return nil, fmt.Errorf("bad bar %s", row)
		}
		if len(row) > 6 {
			var ann struct {
				FH string `json:"FHcontent"`
			}
			_ = json.Unmarshal(row[6], &ann)
			b.fh = ann.FH
		}
		out = append(out, b)
	}
	return out, nil
}

// toBars 去重、丢弃停牌（无成交）K 线，并按分红送转标注计算除权乘数。
func toBars(raw []rawBar) []ds.StockBar {
	out := make([]ds.StockBar, 0, len(raw))
	prev := 0.0
	for _, r := range raw {
		if r.close <= 0 || r.volume <= 0 || (len(out) > 0 && r.date <= out[len(out)-1].Date) {
			continue
		}
		mult := 1.0
		if r.fh != "" && prev > 0 {
			mult = ExRightsMult(prev, r.fh)
		}
		out = append(out, ds.StockBar{Date: r.date, Close: r.close, Mult: mult})
		prev = r.close
	}
	return out
}

var (
	reBase  = regexp.MustCompile(`^(\d+(?:\.\d+)?)`)
	reGive  = regexp.MustCompile(`送(\d+(?:\.\d+)?)`)
	reTrans = regexp.MustCompile(`转增?(\d+(?:\.\d+)?)`)
	reDiv   = regexp.MustCompile(`派(\d+(?:\.\d+)?)`)
)

// ExRightsMult 按交易所除权公式计算乘数：参考价 = (前收 − 每股派现) ÷ (1 + 每股送转)，乘数 = 前收 ÷ 参考价。
// fh 形如 "10送3转2派1元"（每 10 股）。无法解析时返回 1。配股不处理（近年极少）。
func ExRightsMult(prevClose float64, fh string) float64 {
	m := reBase.FindStringSubmatch(fh)
	if m == nil {
		return 1
	}
	base, _ := strconv.ParseFloat(m[1], 64)
	if base <= 0 {
		return 1
	}
	num := func(re *regexp.Regexp) float64 {
		if x := re.FindStringSubmatch(fh); x != nil {
			v, _ := strconv.ParseFloat(x[1], 64)
			return v / base
		}
		return 0
	}
	ref := (prevClose - num(reDiv)) / (1 + num(reGive) + num(reTrans))
	if ref <= 0 {
		return 1
	}
	return prevClose / ref
}

func clipFinal(bars []ds.StockBar, now time.Time) []ds.StockBar {
	last := ds.LatestFinalDate(now)
	for len(bars) > 0 && bars[len(bars)-1].Date > last {
		bars = bars[:len(bars)-1]
	}
	return bars
}
