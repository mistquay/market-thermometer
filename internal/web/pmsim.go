package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"market-thermometer/internal/job"
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
	s.audit(r.Context(), "pm.account.params", strconv.FormatInt(id, 10), map[string]any{
		"maxOrderUsd": p.MaxOrderUSD, "maxDailyUsd": p.MaxDailyUSD, "maxMarketUsd": p.MaxMarketUSD, "maxAssetUsd": p.MaxAssetUSD, "halted": p.Halted,
	})
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
	s.audit(r.Context(), "pm.order", req.Slug, map[string]any{
		"account": req.Account, "side": req.Side, "outcome": req.Outcome, "status": o.Status, "shares": o.Fill.Shares, "gross": o.Fill.Gross, "reason": o.Reason,
	})
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
	s.audit(r.Context(), "pm.halt", "global", map[string]any{"on": req.On})
	if s.pusher != nil {
		state := "已解除"
		if req.On {
			state = "已开启（拒绝所有新委托）"
		}
		go s.pusher.SendAlert(r.Context(), "pm-halt-"+strconv.FormatBool(req.On), "🛑【风控告警 · Polymarket 全局熔断"+state+"】", []string{
			"操作来源：Web 控制台",
		})
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": req.On})
}

// ---- 策略执行模式（shadow / hitl / auto_sim）与人工审批队列 ----

func (s *Server) pmExecGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, job.LoadPMExecConfig(r.Context(), s.store))
}

func (s *Server) pmExecSave(w http.ResponseWriter, r *http.Request) {
	cfg := job.DefaultPMExecConfig()
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	switch cfg.Mode {
	case "shadow", "hitl", "auto_sim":
	default:
		writeErr(w, http.StatusBadRequest, "模式须为 shadow / hitl / auto_sim")
		return
	}
	if cfg.Mode == "auto_sim" && cfg.AccountID <= 0 {
		writeErr(w, http.StatusBadRequest, "自动模拟盘模式必须绑定模拟账户")
		return
	}
	if cfg.OrderUSD <= 0 || cfg.OrderUSD > 10000 {
		writeErr(w, http.StatusBadRequest, "单笔金额须在 0–10000 之间")
		return
	}
	if err := s.store.SaveJSON(r.Context(), job.SettingPMExec, cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), "pm.exec", "config", map[string]any{
		"mode": cfg.Mode, "accountId": cfg.AccountID, "sizing": cfg.Sizing, "orderUsd": cfg.OrderUSD, "kSrc": cfg.KSrc, "minEdge": cfg.MinEdge,
	})
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) pmProposals(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.PMProposals(r.Context(), 100, s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) pmProposalApprove(w http.ResponseWriter, r *http.Request) {
	if !s.pmSimReady(w) {
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "提案 ID 无效")
		return
	}
	var req struct {
		AccountID int64   `json:"accountId"`
		Amount    float64 `json:"amount"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ctx := r.Context()
	prop, err := s.store.PMProposalByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "提案不存在")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if prop.Status != "pending" {
		writeErr(w, http.StatusConflict, "提案已处理："+prop.Status)
		return
	}
	if s.now().UnixMilli() > prop.ExpiresAt {
		_ = s.store.ResolvePMProposal(ctx, id, "expired", 0, "已过确认时限")
		writeErr(w, http.StatusConflict, "提案已过期，请勿追单")
		return
	}
	acctID := req.AccountID
	if acctID <= 0 {
		acctID = prop.AccountID
	}
	if acctID <= 0 {
		writeErr(w, http.StatusBadRequest, "请指定要执行的模拟账户")
		return
	}
	amt := req.Amount
	if amt <= 0 {
		amt = prop.SuggestedUSD
	}
	o, err := s.pmSim.Place(ctx, pmtrade.OrderReq{
		Account: acctID, Slug: prop.Slug, Cond: prop.Cond, Outcome: prop.Outcome,
		Side: pmsim.Buy, Amount: amt, Limit: prop.P, Source: "hitl:" + prop.Strategy,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.store.ResolvePMProposal(ctx, id, "approved", o.ID, o.Reason)
	s.audit(ctx, "pm.proposal.approve", strconv.FormatInt(id, 10), map[string]any{
		"slug": prop.Slug, "account": acctID, "orderId": o.ID, "status": o.Status, "gross": o.Fill.Gross, "reason": o.Reason,
	})
	writeJSON(w, http.StatusOK, map[string]any{"order": o})
}

func (s *Server) pmProposalReject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "提案 ID 无效")
		return
	}
	if err := s.store.ResolvePMProposal(r.Context(), id, "rejected", 0, "人工拒绝"); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusConflict, "提案不存在或已处理")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), "pm.proposal.reject", strconv.FormatInt(id, 10), map[string]any{})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
