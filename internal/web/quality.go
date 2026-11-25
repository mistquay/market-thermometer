package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
)

func (s *Server) quality(w http.ResponseWriter, r *http.Request) {
	rep, err := job.Quality(r.Context(), s.store, s.hub, market.CN, s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// startCrossCheck 启动交叉核对；codes 为空时随机抽样。
func (s *Server) startCrossCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Codes []string `json:"codes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if len(req.Codes) > 50 {
		writeErr(w, http.StatusBadRequest, "一次最多核对 50 只")
		return
	}
	for _, c := range req.Codes {
		if !reFundCode.MatchString(c) {
			writeErr(w, http.StatusBadRequest, "证券代码应为 6 位数字")
			return
		}
	}
	st, err := s.runner.StartCrossCheck(req.Codes)
	switch {
	case errors.Is(err, job.ErrBusy):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, job.ErrNoStockSource):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, st)
	}
}
