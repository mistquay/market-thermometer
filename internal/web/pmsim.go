package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"market-thermometer/internal/pmsim"
	"market-thermometer/internal/pmtrade"
	"market-thermometer/internal/store"
)

// ---- 预测市场模拟盘 ----

func (s *Server) pmSimReady(w http.ResponseWriter) bool {
	if s.pmSim == nil {
		writeErr(w, http.StatusBadRequest, "未配置预测市场数据源")
		return false
	}
	return true
}

func (s *Server) pmSimState(w http.ResponseWriter, r *http.Request) {
	if !s.pmSimReady(w) {
		return
	}
	ctx := r.Context()
	accts, err := s.store.PMSimAccounts(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := []pmtrade.AccountView{}
	for _, a := range accts {
		v, err := s.pmSim.Account(ctx, a.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		v.Orders = nil // 列表只给汇总
		views = append(views, v)
	}
	halt, err := pmtrade.GlobalHalt(ctx, s.store)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": views, "globalHalt": halt, "defaults": pmsim.DefaultParams()})
}

func (s *Server) createPMSimAccount(w http.ResponseWriter, r *http.Request) {
	if !s.pmSimReady(w) {
		return
	}
	var req struct {
		Name   string        `json:"name"`
		Cash   float64       `json:"cash"`
		Params *pmsim.Params `json:"params"`
		Note   string        `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 60 || req.Cash <= 0 || req.Cash > 1e9 {
		writeErr(w, http.StatusBadRequest, "名称为 1–60 个字符，初始资金为正数")
		return
	}
	p := pmsim.DefaultParams()
	if req.Params != nil {
		p = *req.Params
	}
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.store.CreatePMSimAccount(r.Context(), store.PMSimAccount{Name: req.Name, Cash: req.Cash, Params: p, Note: req.Note}, s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *Server) pmSimAccount(w http.ResponseWriter, r *http.Request) {
	if !s.pmSimReady(w) {
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "账户 ID 无效")
		return
	}
	v, err := s.pmSim.Account(r.Context(), id)
	if errors.Is(err, pmtrade.ErrInvalid) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(v.Orders) > 300 {
		v.Orders = v.Orders[:300]
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) setPMSimParams(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "账户 ID 无效")
		return
	}
	var p pmsim.Params
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetPMSimParams(r.Context(), id, p); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "账户不存在")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deletePMSimAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "账户 ID 无效")
		return
	}
	if err := s.store.DeletePMSimAccount(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// placePMSimOrder 下单（dryRun 为 true 时只试算）。风控拒单返回 200 与 status=rejected；请求无效返回 400。
func (s *Server) placePMSimOrder(w http.ResponseWriter, r *http.Request) {
	if !s.pmSimReady(w) {
		return
	}
	var req pmtrade.OrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !rePMSlug.MatchString(req.Slug) {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Source = "manual" // 网页下单一律记为人工
	o, err := s.pmSim.Place(r.Context(), req)
	if errors.Is(err, pmtrade.ErrInvalid) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	} else if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) setPMHalt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := pmtrade.SetGlobalHalt(r.Context(), s.store, req.On); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": req.On})
}
