package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
	"market-thermometer/internal/sim"
	"market-thermometer/internal/store"
)

// 模拟交易：账户、委托、调仓预览。与券商无关，只用本地日线撮合。

func (s *Server) simAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.SimAccounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": list, "defaults": sim.DefaultParams()})
}

func (s *Server) createSimAccount(w http.ResponseWriter, r *http.Request) {
	req := store.SimAccount{Params: sim.DefaultParams()}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	req.Name, req.Note = strings.TrimSpace(req.Name), strings.TrimSpace(req.Note)
	switch {
	case req.Name == "" || len([]rune(req.Name)) > 40:
		writeErr(w, http.StatusBadRequest, "账户名称须为 1–40 个字符")
		return
	case !(req.Cash >= 1000 && req.Cash <= 1e10):
		writeErr(w, http.StatusBadRequest, "初始资金须在 1,000 – 100 亿元之间")
		return
	}
	if err := req.Params.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Start == "" {
		req.Start = s.now().In(ds.Shanghai).Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", req.Start); err != nil {
		writeErr(w, http.StatusBadRequest, "起始日格式应为 YYYY-MM-DD")
		return
	}
	id, err := s.store.CreateSimAccount(r.Context(), req, s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func pathID(r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) deleteSimAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusNotFound, "账户不存在")
		return
	}
	if err := s.store.DeleteSimAccount(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// simAccount 回放账户（顺带保存新的撮合结果）并返回持仓、资金、净值、委托。
func (s *Server) simAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusNotFound, "账户不存在")
		return
	}
	v, _, err := job.LoadSim(r.Context(), s.store, s.hub, market.CN, id, nil, s.now())
	if s.simErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) simErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, "账户不存在")
	default:
		writeErr(w, http.StatusBadRequest, err.Error())
	}
	return true
}

// placeSimOrders 提交一批委托（手工或调仓预览确认后），整批通过风控检查才提交。
func (s *Server) placeSimOrders(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusNotFound, "账户不存在")
		return
	}
	var req struct {
		Source string      `json:"source"`
		Orders []sim.Order `json:"orders"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	switch req.Source {
	case "manual", "rebalance", "strategy":
	default:
		writeErr(w, http.StatusBadRequest, "来源须为 manual / rebalance / strategy")
		return
	}
	for i := range req.Orders {
		req.Orders[i].Note = truncateRunes(strings.TrimSpace(req.Orders[i].Note), 200)
	}
	ids, err := job.PlaceSimOrders(r.Context(), s.store, s.hub, market.CN, id, req.Orders, req.Source, s.now())
	if s.simErr(w, err) {
		return
	}
	s.audit(r.Context(), "sim.order", strconv.FormatInt(id, 10), map[string]any{"source": req.Source, "count": len(ids)})
	writeJSON(w, http.StatusOK, map[string]any{"ids": ids})
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (s *Server) cancelSimOrder(w http.ResponseWriter, r *http.Request) {
	id, ok1 := pathID(r, "id")
	oid, ok2 := pathID(r, "oid")
	if !ok1 || !ok2 {
		writeErr(w, http.StatusNotFound, "委托不存在")
		return
	}
	if err := s.store.CancelSimOrder(r.Context(), id, oid, s.now()); errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusConflict, "只能撤销待成交的委托")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// planSimRebalance 生成调仓预览（不提交）：按组合权重（portfolioId），或按温度计分层仓位持有一只代理证券（strategyCode）。
func (s *Server) planSimRebalance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusNotFound, "账户不存在")
		return
	}
	var req struct {
		PortfolioID  int64  `json:"portfolioId"`
		StrategyCode string `json:"strategyCode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	var targets []sim.Target
	var note string
	var pos *job.StrategyPosition
	switch {
	case req.PortfolioID > 0:
		p, err := s.store.Portfolio(r.Context(), req.PortfolioID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "组合不存在")
			return
		}
		if targets, err = job.PortfolioTargets(p); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		note = "按组合“" + p.Name + "”的目标权重调仓"
	case req.StrategyCode != "":
		if !reFundCode.MatchString(req.StrategyCode) {
			writeErr(w, http.StatusBadRequest, "证券代码应为 6 位数字")
			return
		}
		sp, err := job.ThermometerPosition(r.Context(), s.store, s.hub, market.CN, s.now())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		pos = &sp
		targets = []sim.Target{{Code: req.StrategyCode, Weight: sp.Weight}}
		note = "按温度计分层仓位调仓（" + sp.Date + "）"
	default:
		writeErr(w, http.StatusBadRequest, "须指定组合或策略代理证券")
		return
	}
	plan, err := job.PlanRebalance(r.Context(), s.store, s.hub, market.CN, id, targets, note, s.now())
	if s.simErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "strategy": pos})
}
