// Package polymarket 是 Polymarket 公共行情插件（只读，无需钱包或 API Key）。
//
// 接口：
//   - Gamma（gamma-api.polymarket.com）：事件与市场元数据，/events 每页最多 100 条（超过 50 条只返回 gzip，Go 自动处理），
//     支持 series_slug、closed、order、offset；/public-search 全文搜索。
//   - CLOB（clob.polymarket.com）：/book 订单簿、/prices-history 价格历史（fidelity 为分钟）。
//
// 实测（2026-11-26）：BTC 等短期涨跌盘的事件 slug 形如 btc-updown-15m-<开始 Unix 秒>，系列为 btc-up-or-down-{5m,15m,hourly,4h,daily}，
// 按 Chainlink 60 秒 TWAP 结算（不是币安价）；已结算市场 outcomePrices 为 ["1","0"] 或 ["0","1"]。
// 下单受地区限制（见 docs/polymarket.md），本插件只读不受影响。
package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/plugin"
)

const Name = "polymarket"

func init() {
	plugin.MustRegister(plugin.Manifest{Name: Name, Kind: plugin.KindPrediction, Label: "Polymarket",
		Description:  "Polymarket 公共行情（只读）：事件与市场、搜索、订单簿、价格历史；无需钱包",
		Capabilities: []string{"events", "search", "book", "history"}})
	ds.RegisterPrediction(Name, func(env ds.Env) (ds.PredictionSource, error) {
		return &Source{gamma: "https://gamma-api.polymarket.com", clob: "https://clob.polymarket.com", http: env.HTTP}, nil
	})
}

type Source struct {
	gamma, clob string
	http        *http.Client

	mu   sync.Mutex
	next time.Time
}

func (s *Source) Name() string { return Name }

func (s *Source) get(ctx context.Context, u string, v any) error {
	return s.do(ctx, http.MethodGet, u, nil, v)
}

func (s *Source) do(ctx context.Context, method, u string, payload any, v any) error {
	s.mu.Lock()
	wait := time.Until(s.next)
	s.next = time.Now().Add(max(wait, 0) + 50*time.Millisecond)
	s.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	var body []byte
	var err error
	if method == http.MethodGet {
		body, err = ds.GetBody(ctx, s.http, u, nil)
	} else {
		body, err = s.post(ctx, u, payload)
	}
	if err != nil {
		return fmt.Errorf("polymarket: %w", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("polymarket: decode %.200s: %w", body, err)
	}
	return nil
}

// flt 兼容数字与数字字符串（Gamma 两种都有）。
type flt float64

func (f *flt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	*f = flt(v)
	return err
}

// strList 解析 Gamma 中以 JSON 字符串形式存放的数组，如 "[\"Yes\", \"No\"]"。
type strList []string

func (l *strList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return json.Unmarshal(b, (*[]string)(l)) // 也可能直接是数组
	}
	if s == "" {
		*l = nil
		return nil
	}
	return json.Unmarshal([]byte(s), (*[]string)(l))
}

func ts(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

type gammaMarket struct {
	ConditionID    string  `json:"conditionId"`
	Slug           string  `json:"slug"`
	Question       string  `json:"question"`
	GroupItemTitle string  `json:"groupItemTitle"`
	Outcomes       strList `json:"outcomes"`
	Tokens         strList `json:"clobTokenIds"`
	Prices         strList `json:"outcomePrices"`
	BestBid        flt     `json:"bestBid"`
	BestAsk        flt     `json:"bestAsk"`
	LastTradePrice flt     `json:"lastTradePrice"`
	VolumeNum      flt     `json:"volumeNum"`
	Volume24hr     flt     `json:"volume24hr"`
	LiquidityNum   flt     `json:"liquidityNum"`
	EventStartTime string  `json:"eventStartTime"`
	StartDate      string  `json:"startDate"`
	EndDate        string  `json:"endDate"`
	Closed         bool    `json:"closed"`
	Accepting      bool    `json:"acceptingOrders"`
	TickSize       flt     `json:"orderPriceMinTickSize"`
	MinSize        flt     `json:"orderMinSize"`
	FeeType        string  `json:"feeType"`
	FeesEnabled    bool    `json:"feesEnabled"`
	FeeSchedule    *struct {
		Rate     flt `json:"rate"`
		Exponent flt `json:"exponent"`
	} `json:"feeSchedule"`
	UMAStatus string `json:"umaResolutionStatus"`
}

type gammaEvent struct {
	ID         string                  `json:"id"`
	Slug       string                  `json:"slug"`
	Title      string                  `json:"title"`
	SeriesSlug string                  `json:"seriesSlug"`
	Tags       []struct{ Slug string } `json:"tags"`
	StartDate  string                  `json:"startDate"`
	EndDate    string                  `json:"endDate"`
	Closed     bool                    `json:"closed"`
	NegRisk    bool                    `json:"negRisk"`
	Volume     flt                     `json:"volume"`
	Volume24hr flt                     `json:"volume24hr"`
	Liquidity  flt                     `json:"liquidity"`
	Metadata   *struct {
		PriceToBeat flt `json:"priceToBeat"`
	} `json:"eventMetadata"`
	Markets []gammaMarket `json:"markets"`
}

func (m gammaMarket) convert() ds.PMMarket {
	out := ds.PMMarket{ConditionID: m.ConditionID, Slug: m.Slug, Question: m.Question, GroupTitle: m.GroupItemTitle,
		Outcomes: m.Outcomes, Tokens: m.Tokens, BestBid: float64(m.BestBid), BestAsk: float64(m.BestAsk), LastPrice: float64(m.LastTradePrice),
		Volume: float64(m.VolumeNum), Volume24h: float64(m.Volume24hr), Liquidity: float64(m.LiquidityNum),
		Start: ts(m.EventStartTime), End: ts(m.EndDate), Closed: m.Closed, Accepting: m.Accepting,
		TickSize: float64(m.TickSize), MinSize: float64(m.MinSize), FeeType: m.FeeType, Winner: -1, Resolution: m.UMAStatus}
	if out.Start.IsZero() {
		out.Start = ts(m.StartDate)
	}
	if m.FeesEnabled && m.FeeSchedule != nil {
		out.FeeRate, out.FeeExp = float64(m.FeeSchedule.Rate), float64(m.FeeSchedule.Exponent)
	}
	for _, p := range m.Prices {
		v, _ := strconv.ParseFloat(p, 64)
		out.Prices = append(out.Prices, v)
	}
	// 已结算：关闭、UMA 状态为空或 resolved（短期涨跌盘不走 UMA，状态为空），且价格恰为 1 / 0
	if m.Closed && (m.UMAStatus == "" || m.UMAStatus == "resolved") {
		win, settled := -1, true
		for i, p := range out.Prices {
			switch {
			case p >= 0.999:
				if win >= 0 {
					settled = false
				}
				win = i
			case p > 0.001:
				settled = false
			}
		}
		if settled {
			out.Winner = win
		}
	}
	return out
}

func (e gammaEvent) convert() ds.PMEvent {
	out := ds.PMEvent{ID: e.ID, Slug: e.Slug, Title: e.Title, Series: e.SeriesSlug, Start: ts(e.StartDate), End: ts(e.EndDate),
		Closed: e.Closed, NegRisk: e.NegRisk, Volume: float64(e.Volume), Volume24h: float64(e.Volume24hr), Liquidity: float64(e.Liquidity), Tags: []string{}}
	for _, t := range e.Tags {
		out.Tags = append(out.Tags, t.Slug)
	}
	if e.Metadata != nil {
		out.PriceToBeat = float64(e.Metadata.PriceToBeat)
	}
	for _, m := range e.Markets {
		out.Markets = append(out.Markets, m.convert())
	}
	return out
}

func (s *Source) Events(ctx context.Context, q ds.PMEventQuery) ([]ds.PMEvent, error) {
	v := query(q)
	v.Set("offset", strconv.Itoa(q.Offset))
	var raw []gammaEvent
	if err := s.get(ctx, s.gamma+"/events?"+v.Encode(), &raw); err != nil {
		return nil, err
	}
	return convertAll(raw), nil
}

// EventsAfter 用 /events/keyset 按游标分页（/events 的 offset 超过约 2,000 会返回 422）。
func (s *Source) EventsAfter(ctx context.Context, q ds.PMEventQuery, cursor string) ([]ds.PMEvent, string, error) {
	v := query(q)
	if cursor != "" {
		v.Set("after_cursor", cursor)
	}
	var r struct {
		Events     []gammaEvent `json:"events"`
		NextCursor string       `json:"next_cursor"`
	}
	if err := s.get(ctx, s.gamma+"/events/keyset?"+v.Encode(), &r); err != nil {
		return nil, "", err
	}
	if len(r.Events) == 0 {
		r.NextCursor = ""
	}
	return convertAll(r.Events), r.NextCursor, nil
}

func convertAll(raw []gammaEvent) []ds.PMEvent {
	out := make([]ds.PMEvent, 0, len(raw))
	for _, e := range raw {
		out = append(out, e.convert())
	}
	return out
}

func query(q ds.PMEventQuery) url.Values {
	v := url.Values{}
	if q.Slug != "" {
		v.Add("slug", q.Slug)
	}
	for _, s := range q.Slugs {
		v.Add("slug", s)
	}
	if !q.EndMin.IsZero() {
		v.Set("end_date_min", q.EndMin.UTC().Format(time.RFC3339))
	}
	if q.Series != "" {
		v.Set("series_slug", q.Series)
	}
	if q.Tag != "" {
		v.Set("tag_slug", q.Tag)
	}
	if q.Closed != nil {
		v.Set("closed", strconv.FormatBool(*q.Closed))
		if !*q.Closed {
			v.Set("active", "true")
		}
	}
	if q.Order != "" {
		v.Set("order", q.Order)
		v.Set("ascending", strconv.FormatBool(q.Asc))
	}
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	v.Set("limit", strconv.Itoa(limit))
	return v
}

func (s *Source) Search(ctx context.Context, text string, limit int) ([]ds.PMEvent, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var r struct {
		Events []gammaEvent `json:"events"`
	}
	u := fmt.Sprintf("%s/public-search?q=%s&limit_per_type=%d&events_status=active", s.gamma, url.QueryEscape(text), limit)
	if err := s.get(ctx, u, &r); err != nil {
		return nil, err
	}
	out := make([]ds.PMEvent, 0, len(r.Events))
	for _, e := range r.Events {
		out = append(out, e.convert())
	}
	return out, nil
}

func (s *Source) PriceHistory(ctx context.Context, token string, from, to int64, fidelity int) ([]ds.PricePoint, error) {
	if fidelity <= 0 {
		fidelity = 1
	}
	var r struct {
		History []ds.PricePoint `json:"history"`
	}
	u := fmt.Sprintf("%s/prices-history?market=%s&startTs=%d&endTs=%d&fidelity=%d", s.clob, url.QueryEscape(token), from, to, fidelity)
	if err := s.get(ctx, u, &r); err != nil {
		return nil, err
	}
	sort.Slice(r.History, func(i, j int) bool { return r.History[i].T < r.History[j].T })
	return r.History, nil
}

func (s *Source) post(ctx context.Context, u string, payload any) ([]byte, error) {
	js, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(js))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %.200s", resp.StatusCode, body)
	}
	return body, nil
}

type clobBook struct {
	AssetID   string `json:"asset_id"`
	Timestamp flt    `json:"timestamp"`
	Hash      string `json:"hash"`
	Bids      []struct {
		Price flt `json:"price"`
		Size  flt `json:"size"`
	} `json:"bids"`
	Asks []struct {
		Price flt `json:"price"`
		Size  flt `json:"size"`
	} `json:"asks"`
}

func (s *Source) Book(ctx context.Context, token string) (ds.Book, error) {
	var r clobBook
	if err := s.get(ctx, s.clob+"/book?token_id="+url.QueryEscape(token), &r); err != nil {
		return ds.Book{}, err
	}
	return r.convert(token), nil
}

// Books 用 POST /books 一次取多个代币的订单簿。
func (s *Source) Books(ctx context.Context, tokens []string) ([]ds.Book, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	req := make([]map[string]string, len(tokens))
	for i, t := range tokens {
		req[i] = map[string]string{"token_id": t}
	}
	var raw []clobBook
	if err := s.do(ctx, http.MethodPost, s.clob+"/books", req, &raw); err != nil {
		return nil, err
	}
	out := make([]ds.Book, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.convert(r.AssetID))
	}
	return out, nil
}

func (r clobBook) convert(token string) ds.Book {
	b := ds.Book{Token: token, Time: time.UnixMilli(int64(r.Timestamp)), Hash: r.Hash}
	for _, l := range r.Bids {
		b.Bids = append(b.Bids, ds.BookLevel{Price: float64(l.Price), Size: float64(l.Size)})
	}
	for _, l := range r.Asks {
		b.Asks = append(b.Asks, ds.BookLevel{Price: float64(l.Price), Size: float64(l.Size)})
	}
	sort.Slice(b.Bids, func(i, j int) bool { return b.Bids[i].Price > b.Bids[j].Price })
	sort.Slice(b.Asks, func(i, j int) bool { return b.Asks[i].Price < b.Asks[j].Price })
	return b
}
