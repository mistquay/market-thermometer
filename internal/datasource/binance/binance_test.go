package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKlines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v3/klines") || r.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Errorf("bad request %s", r.URL)
		}
		// 两根已收盘的日线 + 一根未收盘
		w.Write([]byte(`[[1795536000000,"100","110","90","105","12.5",1795622399999,"1300.5",10,"1","1","0"],
			[1795622400000,"105","120","100","118","8",1795708799999,"900",5,"1","1","0"],
			[1795708800000,"118","119","117","118.5","1",1795795199999,"118",1,"1","1","0"]]`))
	}))
	defer srv.Close()
	now := time.UnixMilli(1795708800000 + 3600_000)
	s := &Source{base: srv.URL, http: srv.Client(), now: func() time.Time { return now }}
	ks, err := s.Klines(context.Background(), "BTCUSDT", "1d", 1795536000000, now.UnixMilli())
	if err != nil || len(ks) != 2 || ks[1].Close != 118 || ks[0].Quote != 1300.5 || ks[0].Volume != 12.5 {
		t.Fatalf("klines %+v %v", ks, err)
	}
}
