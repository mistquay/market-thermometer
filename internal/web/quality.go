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

// startExtBackfill 从 stockdb 回填 A 股扩展字段（OHLC、成交、估值），默认 2015 年起。
func (s *Server) startExtBackfill(w http.ResponseWriter, r *http.Request) {
	st, err := s.runner.StartExtBackfill(job.ExtBackfillFrom)
	switch {
	case errors.Is(err, job.ErrBusy):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, st)
	}
}

// startGapFill 用指定数据源补齐这些股票的缺口（通常来自交叉核对中“本地缺、来源有”的差异）。
func (s *Server) startGapFill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string   `json:"source"`
		Codes  []string `json:"codes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Codes) == 0 || len(req.Codes) > 50 {
		writeErr(w, http.StatusBadRequest, "须提供 1–50 只股票")
		return
	}
	for _, c := range req.Codes {
		if !reFundCode.MatchString(c) {
			writeErr(w, http.StatusBadRequest, "证券代码应为 6 位数字")
			return
		}
	}
	st, err := s.runner.StartGapFill(req.Source, req.Codes)
	switch {
	case errors.Is(err, job.ErrBusy):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, st)
	}
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
