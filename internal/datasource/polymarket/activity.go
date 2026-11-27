package polymarket

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	ds "market-thermometer/internal/datasource"
)

const dataAPIBase = "https://data-api.polymarket.com"

// TopActiveEvents 拉取全站 24h 成交额最高的一批进行中事件（用于全站 NegRisk 套利发现）。
func (s *Source) TopActiveEvents(ctx context.Context, limit int) ([]ds.PMEvent, error) {
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	u := fmt.Sprintf("%s/events?active=true&closed=false&order=volume24hr&ascending=false&limit=%d", s.gamma, limit)
	var raw []gammaEvent
	if err := s.get(ctx, u, &raw); err != nil {
		return nil, err
	}
	out := make([]ds.PMEvent, 0, len(raw))
	for _, e := range raw {
		out = append(out, e.convert())
	}
	return out, nil
}

// RecentTrades 调用 Polymarket Data API v2 拉取最新公开成交记录（可按钱包地址或市场过滤）。
func (s *Source) RecentTrades(ctx context.Context, user, condID string, limit int) ([]ds.PMTrade, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	v := url.Values{}
	v.Set("limit", strconv.Itoa(limit))
	if user != "" {
		v.Set("user", user)
	}
	if condID != "" {
		v.Set("market", condID)
	}
	var resp struct {
		Data []struct {
			ProxyWallet string  `json:"proxy_wallet"`
			Name        string  `json:"name"`
			Pseudonym   string  `json:"pseudonym"`
			Side        string  `json:"side"`
			TokenID     string  `json:"token_id"`
			CondID      string  `json:"condition_id"`
			Size        float64 `json:"size"`
			Price       float64 `json:"price"`
			Timestamp   int64   `json:"timestamp"`
			Title       string  `json:"title"`
			Slug        string  `json:"slug"`
			EventSlug   string  `json:"event_slug"`
			Outcome     string  `json:"outcome"`
			TxHash      string  `json:"transaction_hash"`
		} `json:"data"`
	}
	if err := s.get(ctx, dataAPIBase+"/v2/trades?"+v.Encode(), &resp); err != nil {
		return nil, err
	}
	out := make([]ds.PMTrade, 0, len(resp.Data))
	for _, r := range resp.Data {
		name := r.Name
		if name == "" {
			name = r.Pseudonym
		}
		out = append(out, ds.PMTrade{
			Wallet: r.ProxyWallet, Name: name, Side: r.Side, TokenID: r.TokenID, CondID: r.CondID,
			Size: r.Size, Price: r.Price, USDC: r.Size * r.Price, Timestamp: r.Timestamp,
			Title: r.Title, Slug: r.Slug, EventSlug: r.EventSlug, Outcome: r.Outcome, TxHash: r.TxHash,
		})
	}
	return out, nil
}
