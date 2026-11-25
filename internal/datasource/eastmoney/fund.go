package eastmoney

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	ds "market-thermometer/internal/datasource"
)

// 东财基金净值插件（场外基金）。
//   - 清单：fund.eastmoney.com/js/fundcode_search.js（约 2.6 万只，3MB）
//   - 净值：fund.eastmoney.com/pingzhongdata/{code}.js，一次返回全部历史的单位净值与累计净值
//
// 实测连续 40 次请求成功 33 次，偶发失败由 GetBody 重试；只用于自选基金，请求量小。
const FundName = "eastmoney"

func init() {
	ds.RegisterFund(FundName, func(env ds.Env) (ds.FundSource, error) {
		return &FundSource{http: env.HTTP}, nil
	})
}

type FundSource struct{ http *http.Client }

func (s *FundSource) Name() string { return FundName }

const (
	fundListURL = "https://fund.eastmoney.com/js/fundcode_search.js"
	fundNAVURL  = "https://fund.eastmoney.com/pingzhongdata/%s.js"
)

func (s *FundSource) ListFunds(ctx context.Context) ([]ds.FundInfo, error) {
	body, err := ds.GetBody(ctx, s.http, fundListURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fund list: %w", err)
	}
	return ParseFundList(body)
}

var reFundList = regexp.MustCompile(`(?s)var\s+r\s*=\s*(\[.*\])`)

// ParseFundList 解析 `var r = [["000001","HXCZHH","华夏成长混合","混合型-灵活","HUAXIA..."],...];`。
func ParseFundList(body []byte) ([]ds.FundInfo, error) {
	m := reFundList.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("fund list: unexpected format")
	}
	var rows [][]string
	if err := json.Unmarshal(m[1], &rows); err != nil {
		return nil, fmt.Errorf("fund list: %w", err)
	}
	out := make([]ds.FundInfo, 0, len(rows))
	for _, r := range rows {
		if len(r) >= 4 && len(r[0]) == 6 {
			out = append(out, ds.FundInfo{Code: r[0], Name: r[2], Type: r[3]})
		}
	}
	return out, nil
}

func (s *FundSource) NAV(ctx context.Context, code, from string) ([]ds.FundNAV, error) {
	if len(code) != 6 {
		return nil, fmt.Errorf("bad fund code %q", code)
	}
	body, err := ds.GetBody(ctx, s.http, fmt.Sprintf(fundNAVURL, code), map[string]string{"Referer": "https://fund.eastmoney.com/"})
	if err != nil {
		return nil, fmt.Errorf("fund %s: %w", code, err)
	}
	navs, err := ParseFundNAV(body)
	if err != nil {
		return nil, fmt.Errorf("fund %s: %w", code, err)
	}
	i := 0
	for i < len(navs) && navs[i].Date < from {
		i++
	}
	return navs[i:], nil
}

var (
	reNetWorth = regexp.MustCompile(`(?s)var\s+Data_netWorthTrend\s*=\s*(\[.*?\]);`)
	reACWorth  = regexp.MustCompile(`(?s)var\s+Data_ACWorthTrend\s*=\s*(\[.*?\]);`)
)

// ParseFundNAV 解析 pingzhongdata：Data_netWorthTrend = [{"x":毫秒,"y":单位净值,...}]，
// Data_ACWorthTrend = [[毫秒,累计净值],...]。时间戳按北京时间取日期。
func ParseFundNAV(body []byte) ([]ds.FundNAV, error) {
	m := reNetWorth.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("净值数据缺失（基金代码不存在或接口改版）")
	}
	var unit []struct {
		X int64   `json:"x"`
		Y float64 `json:"y"`
	}
	if err := json.Unmarshal(m[1], &unit); err != nil {
		return nil, fmt.Errorf("decode netWorth: %w", err)
	}
	acc := map[int64]float64{}
	if m := reACWorth.FindSubmatch(body); m != nil {
		var rows [][]*float64
		if json.Unmarshal(m[1], &rows) == nil {
			for _, r := range rows {
				if len(r) == 2 && r[0] != nil && r[1] != nil {
					acc[int64(*r[0])] = *r[1]
				}
			}
		}
	}
	out := make([]ds.FundNAV, 0, len(unit))
	for _, u := range unit {
		if u.Y <= 0 {
			continue
		}
		d := time.UnixMilli(u.X).In(ds.Shanghai).Format("2006-01-02")
		if n := len(out); n > 0 && out[n-1].Date >= d {
			continue
		}
		a, ok := acc[u.X]
		if !ok {
			a = u.Y
		}
		out = append(out, ds.FundNAV{Date: d, NAV: u.Y, AccNAV: a})
	}
	return out, nil
}
