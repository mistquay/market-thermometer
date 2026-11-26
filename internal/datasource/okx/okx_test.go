package okx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInstID(t *testing.T) {
	for in, want := range map[string]string{"BTCUSDT": "BTC-USDT", "ETHBTC": "ETH-BTC", "SOLUSDC": "SOL-USDC"} {
		if got := InstID(in); got != want {
			t.Errorf("%s → %s", in, got)
		}
	}
}

func TestKlinesAndPrice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v5/market/ticker" {
			w.Write([]byte(`{"code":"0","msg":"","data":[{"instId":"BTC-USDT","last":"91234.5"}]}`))
			return
		}
		if r.URL.Query().Get("bar") != "1Dutc" || r.URL.Query().Get("instId") != "BTC-USDT" {
			t.Errorf("bad request %s", r.URL)
		}
		w.Write([]byte(`{"code":"0","msg":"","data":[["1795622400000","105","120","100","118","8","900","900","1"],["1795536000000","100","110","90","105","12.5","1300.5","1300.5","1"]]}`))
	}))
	defer srv.Close()
	now := time.UnixMilli(1795708800000)
	s := &Source{base: srv.URL, http: srv.Client(), now: func() time.Time { return now }}
	ks, err := s.Klines(context.Background(), "BTCUSDT", "1d", 1795536000000, now.UnixMilli())
	if err != nil || len(ks) != 2 || ks[0].T != 1795536000000 || ks[1].High != 120 {
		t.Fatalf("klines %+v %v", ks, err)
	}
	if p, err := s.Price(context.Background(), "BTCUSDT"); err != nil || p != 91234.5 {
		t.Fatalf("price %v %v", p, err)
	}
}
