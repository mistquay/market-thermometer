package web

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	ds "market-thermometer/internal/datasource"
)

// ---- 加密货币：本地 K 线、实时价、手动同步 ----

func (s *Server) cryptoState(w http.ResponseWriter, r *http.Request) {
	c := s.opt.Crypto
	if !c.Configured() {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	cov, err := s.store.KlineCoverages(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "symbols": c.Symbols(), "intervals": c.Intervals(),
		"status": c.Status(), "coverage": cov})
}

// cryptoSymbol 校验品种必须是配置里的。
func (s *Server) cryptoSymbol(w http.ResponseWriter, sym string) bool {
	if !s.opt.Crypto.Configured() {
		writeErr(w, http.StatusBadRequest, "未配置加密行情")
		return false
	}
	if !slices.Contains(s.opt.Crypto.Symbols(), sym) {
		writeErr(w, http.StatusBadRequest, "未配置的品种")
		return false
	}
	return true
}

// cryptoKlines 返回 to（UTC 毫秒，不含；0 为最新）之前最多 limit 根，more 表示更早还有。
func (s *Server) cryptoKlines(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym, iv := q.Get("symbol"), q.Get("interval")
	if !s.cryptoSymbol(w, sym) {
		return
	}
	if _, ok := ds.KlineIntervals[iv]; !ok {
		writeErr(w, http.StatusBadRequest, "不支持的周期")
		return
	}
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 1500
	}
	ks, err := s.store.KlinesBefore(r.Context(), sym, iv, to, limit+1)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	more := len(ks) > limit
	if more {
		ks = ks[1:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"klines": ks, "more": more})
}

func (s *Server) cryptoPrice(w http.ResponseWriter, r *http.Request) {
	sym := r.URL.Query().Get("symbol")
	if !s.cryptoSymbol(w, sym) {
		return
	}
	p, src, err := s.opt.Crypto.Price(r.Context(), sym)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbol": sym, "price": p, "source": src, "time": s.now()})
}

func (s *Server) startCryptoSync(w http.ResponseWriter, r *http.Request) {
	c := s.opt.Crypto
	if !c.Configured() {
		writeErr(w, http.StatusBadRequest, "未配置加密行情")
		return
	}
	if c.Status().Running {
		writeErr(w, http.StatusConflict, "加密行情同步正在进行")
		return
	}
	go c.SyncOnce(context.Background()) // 与请求无关，不能用 r.Context()
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}
