package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"market-thermometer/internal/backtest"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
)

// signals：生效参数下的全部状态机事件 + 当前阶段。
func (s *Server) signals(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.ActiveParams(r.Context(), s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	evs, err := s.store.Events(r.Context(), market.CN.ID, p.Version())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	date, phase, err := s.store.LatestPhase(r.Context(), market.CN.ID, p.Version())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"paramsVersion": p.Version(), "events": evs, "phase": phase, "phaseDate": date,
	})
}

// params：已保存的参数集 + 默认参数。
func (s *Server) params(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.ActiveParams(r.Context(), s.now()); err != nil { // 确保至少有默认参数集
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sets, err := s.store.ListParams(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sets": sets, "defaults": engine.DefaultParams(),
		"defaultsVersion": engine.DefaultParams().Version()})
}

// saveParams：保存参数集；activate=true 时设为生效并立即重算得分与信号。
func (s *Server) saveParams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string        `json:"name"`
		Params   engine.Params `json:"params"`
		Activate bool          `json:"activate"`
	}
	req.Params = engine.DefaultParams()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 40 {
		writeErr(w, http.StatusBadRequest, "参数集名称须为 1–40 个字符")
		return
	}
	if err := req.Params.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SaveParams(r.Context(), req.Name, req.Params, req.Activate, s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{"version": req.Params.Version()}
	if req.Activate {
		res, err := job.Score(r.Context(), s.store, s.hub, market.CN, s.now())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "已保存，但重算失败："+err.Error())
			return
		}
		out["events"], out["days"] = res.Events, res.Days
	}
	writeJSON(w, http.StatusOK, out)
}

// runBacktest：用请求中的参数（缺省为生效参数）在内存中重算并回测，不写库。
func (s *Server) runBacktest(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveParams(r.Context(), s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 请求中的参数以生效参数为底合并：只提交改动的字段即可
	p := active
	req := struct {
		Params *engine.Params   `json:"params"`
		Config *backtest.Config `json:"config"`
	}{Params: &p}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := backtest.DefaultConfig()
	if req.Config != nil {
		cfg.From, cfg.To, cfg.Split = req.Config.From, req.Config.To, req.Config.Split
		if _, err := time.Parse("2006-01-02", cfg.From); err != nil {
			writeErr(w, http.StatusBadRequest, "回测起点日期格式应为 YYYY-MM-DD")
			return
		}
	}
	cfg.Position = p.Position
	start := time.Now()
	days, _, err := job.LoadDays(r.Context(), s.store, s.hub, market.CN, p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, events := engine.RunSignals(days, p.Signal)
	res := backtest.Run(days, events, cfg)
	writeJSON(w, http.StatusOK, map[string]any{
		"paramsVersion": p.Version(), "activeVersion": active.Version(), "params": p,
		"elapsedMs": time.Since(start).Milliseconds(), "result": res,
	})
}
