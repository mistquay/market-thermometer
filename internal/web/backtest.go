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
	s.audit(r.Context(), "params.save", req.Params.Version(), map[string]any{"name": req.Name, "activate": req.Activate})
	writeJSON(w, http.StatusOK, out)
}

// backtestRequest 解析回测请求：参数以生效参数为底合并（只提交改动的字段即可），区间设置可选。
func (s *Server) backtestRequest(w http.ResponseWriter, r *http.Request) (p, active engine.Params, cfg backtest.Config, ok bool) {
	active, err := s.store.ActiveParams(r.Context(), s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p = active
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
	cfg = backtest.DefaultConfig()
	if req.Config != nil {
		cfg.From, cfg.To, cfg.Split = req.Config.From, req.Config.To, req.Config.Split
		if _, err := time.Parse("2006-01-02", cfg.From); err != nil {
			writeErr(w, http.StatusBadRequest, "回测起点日期格式应为 YYYY-MM-DD")
			return
		}
	}
	cfg.Position = p.Position
	return p, active, cfg, true
}

// runBacktest：用请求中的参数（缺省为生效参数）在内存中重算并回测，不写库。
func (s *Server) runBacktest(w http.ResponseWriter, r *http.Request) {
	p, active, cfg, ok := s.backtestRequest(w, r)
	if !ok {
		return
	}
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

// robustRow 是一个扰动变体的结果。
type robustRow struct {
	backtest.Variant
	Outcome *backtest.Outcome `json:"outcome,omitempty"`
}

// runRobust：稳健性检验。以请求参数为基准，逐组做 ±20% 单参数扰动，并按自然年分段统计；只展示，不挑选参数、不写库。
func (s *Server) runRobust(w http.ResponseWriter, r *http.Request) {
	p, _, cfg, ok := s.backtestRequest(w, r)
	if !ok {
		return
	}
	start := time.Now()
	ctx := r.Context()
	cache := map[string][]engine.Day{} // 只改信号 / 仓位参数的变体复用同一份打分结果
	load := func(q engine.Params) ([]engine.Day, error) {
		k := q
		k.Signal, k.Position = engine.SignalParams{}, engine.PositionParams{}
		if d, ok := cache[k.Version()]; ok {
			return d, nil
		}
		d, _, err := job.LoadDays(ctx, s.store, s.hub, market.CN, q)
		if err == nil {
			cache[k.Version()] = d
		}
		return d, err
	}
	run := func(q engine.Params) backtest.Outcome {
		days, err := load(q)
		if err != nil {
			return backtest.Outcome{Error: err.Error()}
		}
		_, events := engine.RunSignals(days, q.Signal)
		c := cfg
		c.Position = q.Position
		return backtest.Summarize(backtest.Run(days, events, c))
	}
	base := run(p)
	if base.Error != "" {
		writeErr(w, http.StatusInternalServerError, base.Error)
		return
	}
	var rows []robustRow
	for _, v := range backtest.Perturb(p) {
		row := robustRow{Variant: v}
		if v.Skip == "" {
			o := run(v.Params)
			row.Outcome = &o
		}
		rows = append(rows, row)
	}
	days, _ := load(p)
	_, events := engine.RunSignals(days, p.Signal)
	writeJSON(w, http.StatusOK, map[string]any{
		"paramsVersion": p.Version(), "ratio": backtest.PerturbRatio, "from": cfg.From,
		"base": base, "rows": rows, "years": backtest.Years(days, events, cfg),
		"elapsedMs": time.Since(start).Milliseconds(),
	})
}
