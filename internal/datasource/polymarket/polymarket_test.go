package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
)

const eventJSON = `[{"id":"1","slug":"btc-updown-15m-1795671000","title":"Bitcoin Up or Down","seriesSlug":"btc-up-or-down-15m",
"tags":[{"slug":"crypto"}],"startDate":"2026-11-25T05:00:00Z","endDate":"2026-11-26T05:45:00Z","closed":true,"negRisk":false,
"volume":12784.2,"volume24hr":"100","liquidity":"0","eventMetadata":{"priceToBeat":60496.41636499259},
"markets":[{"conditionId":"0xabc","slug":"btc-updown-15m-1795671000","question":"Bitcoin Up or Down","outcomes":"[\"Up\", \"Down\"]",
"clobTokenIds":"[\"111\", \"222\"]","outcomePrices":"[\"0\", \"1\"]","bestBid":0.001,"bestAsk":"0.999","volumeNum":12784.2,
"eventStartTime":"2026-11-26T05:30:00Z","endDate":"2026-11-26T05:45:00Z","closed":true,"acceptingOrders":false,
"orderPriceMinTickSize":0.01,"orderMinSize":5,"feeType":"crypto_btc_5m_15m_fees","umaResolutionStatus":"resolved",
"feesEnabled":true,"feeSchedule":{"exponent":1,"rate":0.05,"takerOnly":true,"rebateRate":0.2},
"resolutionSource":"https://data.chain.link/streams/btc-usd-twap-60s-streams","cryptoMarketConfig":{"id":"btc-15m-twap-60","twapEnabled":true,"twapLookbackSeconds":60}}]}]`

func TestEventsBookHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/events":
			if r.URL.Query().Get("series_slug") != "btc-up-or-down-15m" || r.URL.Query().Get("closed") != "true" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			w.Write([]byte(eventJSON))
		case r.URL.Path == "/book":
			w.Write([]byte(`{"asset_id":"111","timestamp":"1795673391930","bids":[{"price":"0.40","size":"10"},{"price":"0.45","size":"5"}],"asks":[{"price":"0.60","size":"3"},{"price":"0.55","size":"7"}]}`))
		case r.URL.Path == "/books" && r.Method == http.MethodPost:
			var req []map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req) != 2 || req[1]["token_id"] != "333" {
				t.Errorf("books body %v %v", req, err)
			}
			w.Write([]byte(`[{"asset_id":"333","timestamp":"1795673391930","hash":"h3","bids":[],"asks":[{"price":"0.2","size":"1"}]},
				{"asset_id":"111","timestamp":"1795673391930","hash":"h1","bids":[{"price":"0.4","size":"2"}],"asks":[]}]`))
		case r.URL.Path == "/prices-history":
			w.Write([]byte(`{"history":[{"t":20,"p":0.6},{"t":10,"p":0.5}]}`))
		}
	}))
	defer srv.Close()
	s := &Source{gamma: srv.URL, clob: srv.URL, http: srv.Client()}
	closed := true
	evs, err := s.Events(context.Background(), ds.PMEventQuery{Series: "btc-up-or-down-15m", Closed: &closed})
	if err != nil || len(evs) != 1 {
		t.Fatalf("events %v %v", evs, err)
	}
	m := evs[0].Markets[0]
	if evs[0].Series != "btc-up-or-down-15m" || m.Outcomes[0] != "Up" || m.Tokens[1] != "222" || m.Winner != 1 || m.BestAsk != 0.999 ||
		!m.Start.Equal(time.Date(2026, 11, 26, 5, 30, 0, 0, time.UTC)) || evs[0].Volume24h != 100 {
		t.Fatalf("market %+v", m)
	}
	b, err := s.Book(context.Background(), "111")
	if err != nil || b.Bids[0].Price != 0.45 || b.Asks[0].Price != 0.55 {
		t.Fatalf("book %+v %v", b, err)
	}
	if m.TwapSeconds != 60 || m.ResolutionSource == "" {
		t.Fatalf("resolution %q %d", m.ResolutionSource, m.TwapSeconds)
	}
	if evs[0].PriceToBeat != 60496.41636499259 || m.FeeRate != 0.05 || m.FeeExp != 1 || math.Abs(m.TakerFee(0.5, 100)-1.25) > 1e-9 {
		t.Fatalf("ptb / fee: %v %v %v %v", evs[0].PriceToBeat, m.FeeRate, m.FeeExp, m.TakerFee(0.5, 100))
	}
	bs, err := s.Books(context.Background(), []string{"111", "333"})
	if err != nil || len(bs) != 2 || bs[0].Token != "333" || bs[0].Hash != "h3" || bs[1].Bids[0].Size != 2 {
		t.Fatalf("books %+v %v", bs, err)
	}
	h, err := s.PriceHistory(context.Background(), "111", 0, 30, 1)
	if err != nil || len(h) != 2 || h[0].T != 10 {
		t.Fatalf("history %v %v", h, err)
	}
}

// TestLive 访问真实接口（POLYMARKET_LIVE=1 时运行）。
func TestLive(t *testing.T) {
	if os.Getenv("POLYMARKET_LIVE") != "1" {
		t.Skip("set POLYMARKET_LIVE=1")
	}
	s := &Source{gamma: "https://gamma-api.polymarket.com", clob: "https://clob.polymarket.com", http: &http.Client{Timeout: 20 * time.Second}}
	ctx := context.Background()
	closed := true
	evs, err := s.Events(ctx, ds.PMEventQuery{Series: "btc-up-or-down-15m", Closed: &closed, Order: "endDate", Limit: 100})
	if err != nil || len(evs) != 100 {
		t.Fatalf("events %d %v", len(evs), err)
	}
	m := evs[0].Markets[0]
	t.Logf("%s winner=%d start=%s tokens=%d", evs[0].Slug, m.Winner, m.Start, len(m.Tokens))
	if m.Winner < 0 || len(m.Tokens) != 2 {
		t.Fatalf("closed market not resolved: %+v", m)
	}
	h, err := s.PriceHistory(ctx, m.Tokens[0], m.Start.Unix()-600, m.End.Unix(), 1)
	if err != nil || len(h) == 0 {
		t.Fatalf("history %d %v", len(h), err)
	}
	found, err := s.Search(ctx, "bitcoin", 5)
	if err != nil || len(found) == 0 {
		t.Fatalf("search %d %v", len(found), err)
	}
	open := false
	act, err := s.Events(ctx, ds.PMEventQuery{Closed: &open, Order: "volume24hr", Limit: 5})
	if err != nil || len(act) == 0 || len(act[0].Markets) == 0 {
		t.Fatalf("active %v", err)
	}
	b, err := s.Book(ctx, act[0].Markets[0].Tokens[0])
	if err != nil || (len(b.Bids) == 0 && len(b.Asks) == 0) {
		t.Fatalf("book %+v %v", b, err)
	}
	t.Logf("%s: bid %v ask %v; search[0]=%s", act[0].Slug, b.Bids[:min(1, len(b.Bids))], b.Asks[:min(1, len(b.Asks))], found[0].Slug)
}

func TestWinner(t *testing.T) {
	for _, c := range []struct {
		closed bool
		uma    string
		prices string
		want   int
	}{
		{true, "resolved", `["0","1"]`, 1},
		{true, "", `["1","0"]`, 0},              // 短期涨跌盘不走 UMA
		{true, "proposed", `["1","0"]`, -1},     // UMA 未最终确认
		{true, "", `["0.995","0.005"]`, -1},     // 已关闭但价格还没归 1 / 0
		{false, "", `["1","0"]`, -1},            // 未关闭
		{true, "resolved", `["0.5","0.5"]`, -1}, // 平局 / 作废
		{true, "resolved", `["0","0","1"]`, 2},  // 多结果
	} {
		var m gammaMarket
		js := fmt.Sprintf(`{"closed":%v,"umaResolutionStatus":%q,"outcomePrices":%q}`, c.closed, c.uma, c.prices)
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			t.Fatal(err)
		}
		if got := m.convert().Winner; got != c.want {
			t.Errorf("%s: winner %d, want %d", js, got, c.want)
		}
	}
}
