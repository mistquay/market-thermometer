package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
	"market-thermometer/internal/portfolio"
	"market-thermometer/internal/store"
)

func (s *Server) searchSecurities(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.SearchSecurities(r.Context(), r.URL.Query().Get("q"), 30)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := s.store.NameCount(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "names": n})
}

func (s *Server) startNames(w http.ResponseWriter, r *http.Request) {
	st, err := s.runner.StartNames()
	if errors.Is(err, job.ErrBusy) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (s *Server) portfolios(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Portfolios(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// savePortfolio 新增（POST）或修改（PUT /{id}）组合。场外基金会自动加入自选，以便每日同步净值。
func (s *Server) savePortfolio(w http.ResponseWriter, r *http.Request) {
	var p store.Portfolio
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p.ID = 0
	if idText := r.PathValue("id"); idText != "" {
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusNotFound, "组合不存在")
			return
		}
		p.ID = id
	}
	p.Name, p.Note = strings.TrimSpace(p.Name), strings.TrimSpace(p.Note)
	if err := validatePortfolio(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.store.SavePortfolio(r.Context(), p, s.now())
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "组合不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, it := range p.Items {
		if it.Kind == store.SecOFund {
			if err := s.store.WatchFund(r.Context(), it.Code, s.now()); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	saved, err := s.store.Portfolio(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func validatePortfolio(p store.Portfolio) error {
	if p.Name == "" || len([]rune(p.Name)) > 40 {
		return errors.New("组合名称须为 1–40 个字符")
	}
	if len([]rune(p.Note)) > 500 {
		return errors.New("备注最多 500 个字符")
	}
	if p.Start != "" {
		if _, err := time.Parse("2006-01-02", p.Start); err != nil {
			return errors.New("起始日格式应为 YYYY-MM-DD")
		}
	}
	if len(p.Items) == 0 || len(p.Items) > 100 {
		return errors.New("组合须有 1–100 个持仓")
	}
	seen := map[string]bool{}
	for _, it := range p.Items {
		switch {
		case it.Kind != store.SecStock && it.Kind != store.SecFund && it.Kind != store.SecOFund:
			return errors.New("持仓类别须为 stock / fund / ofund")
		case !reFundCode.MatchString(it.Code):
			return errors.New("证券代码应为 6 位数字")
		case !(it.Weight > 0 && it.Weight <= 1e6):
			return errors.New(it.Code + " 的权重须为正数")
		case seen[it.Kind+it.Code]:
			return errors.New(it.Code + " 重复")
		}
		seen[it.Kind+it.Code] = true
	}
	return nil
}

func (s *Server) deletePortfolio(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusNotFound, "组合不存在")
		return
	}
	if err := s.store.DeletePortfolio(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// analyzePortfolio 分析组合（只读本地数据）；?start= 覆盖组合保存的起始日。
func (s *Server) analyzePortfolio(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var p store.Portfolio
	if err == nil {
		p, err = s.store.Portfolio(r.Context(), id)
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "组合不存在")
		return
	}
	start := p.Start
	if q := r.URL.Query().Get("start"); q != "" {
		if _, err := time.Parse("2006-01-02", q); err != nil {
			writeErr(w, http.StatusBadRequest, "起始日格式应为 YYYY-MM-DD")
			return
		}
		start = q
	}
	began := time.Now()
	out, err := job.AnalyzePortfolio(r.Context(), s.store, s.hub, market.CN, p, start)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"portfolio": p, "result": out, "elapsedMs": time.Since(began).Milliseconds()})
}

// securitySeries 返回一只证券的本地价格序列：股票、场内基金给不复权收盘价与后复权价；场外基金给单位净值与累计净值。
func (s *Server) securitySeries(w http.ResponseWriter, r *http.Request) {
	kind, code := r.PathValue("kind"), r.PathValue("code")
	if !reFundCode.MatchString(code) {
		writeErr(w, http.StatusBadRequest, "证券代码应为 6 位数字")
		return
	}
	names, err := s.store.SecurityNames(r.Context(), []store.Security{{Kind: kind, Code: code}})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{"kind": kind, "code": code, "name": names[kind+":"+code]}
	switch kind {
	case store.SecStock, store.SecFund:
		bars, err := s.store.StockBars(r.Context(), code, "")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		dates, closes, mults := make([]string, len(bars)), make([]float64, len(bars)), make([]float64, len(bars))
		for i, b := range bars {
			dates[i], closes[i], mults[i] = b.Date, b.Close, b.Mult
		}
		hfq := portfolio.HFQ(dates, closes, mults)
		adj := make([]float64, len(dates))
		for i, d := range dates {
			adj[i] = hfq[d]
		}
		out["dates"], out["close"], out["adjusted"] = dates, closes, adj
	case store.SecOFund:
		navs, err := s.store.FundNAV(r.Context(), code)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		dates, nav, acc := make([]string, len(navs)), make([]float64, len(navs)), make([]float64, len(navs))
		for i, n := range navs {
			dates[i], nav[i], acc[i] = n.Date, n.NAV, n.AccNAV
		}
		out["dates"], out["close"], out["adjusted"] = dates, nav, acc
	default:
		writeErr(w, http.StatusBadRequest, "类别须为 stock / fund / ofund")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
