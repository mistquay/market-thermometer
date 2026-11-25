package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"market-thermometer/internal/job"
)

var reFundCode = regexp.MustCompile(`^\d{6}$`)

func (s *Server) funds(w http.ResponseWriter, r *http.Request) {
	watched, err := s.store.WatchedFunds(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := s.store.FundInfoCount(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"watched": watched, "infoCount": n, "configured": s.runner.HasFundNAV()})
}

func (s *Server) searchFunds(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.SearchFunds(r.Context(), r.URL.Query().Get("q"), 20)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) watchFund(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !reFundCode.MatchString(req.Code) {
		writeErr(w, http.StatusBadRequest, "基金代码应为 6 位数字")
		return
	}
	if err := s.store.WatchFund(r.Context(), req.Code, s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) unwatchFund(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !reFundCode.MatchString(code) {
		writeErr(w, http.StatusBadRequest, "基金代码应为 6 位数字")
		return
	}
	if err := s.store.UnwatchFund(r.Context(), code); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) fundNAV(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !reFundCode.MatchString(code) {
		writeErr(w, http.StatusBadRequest, "基金代码应为 6 位数字")
		return
	}
	navs, err := s.store.FundNAV(r.Context(), code)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, navs)
}

func (s *Server) startFunds(w http.ResponseWriter, r *http.Request) {
	st, err := s.runner.StartFunds()
	switch {
	case errors.Is(err, job.ErrBusy):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, job.ErrNoFundSource):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, st)
	}
}
