package web

import (
	"encoding/json"
	"math"
	"net/http"

	"market-thermometer/internal/datasource"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/market"
)

// jf 把 NaN 转成 JSON null（encoding/json 无法编码 NaN）。
func jf(x float64) any {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return math.Round(x*1e4) / 1e4
}

func dayJSON(d engine.Day) map[string]any {
	t := make([]any, 4)
	for k := range 4 {
		t[k] = jf(d.T[k])
	}
	return map[string]any{
		"date": d.Date, "close": jf(d.Close),
		"erp": jf(d.ERP), "v": jf(d.V), "tier": d.Tier,
		"heat": jf(d.Heat), "leverage": jf(d.Leverage), "breadth": jf(d.Breadth), "rsiD": jf(d.RSID), "rsiW": jf(d.RSIW), "rMix": jf(d.RMix),
		"t": t, "tBase": jf(d.TBase), "tPct": jf(d.TPct), "tS": jf(d.TS),
		"nLow": d.NLow, "nHigh": d.NHigh, "degraded": d.Degraded,
	}
}

// overview：最新一天的完整打分 + 前一天（算变化）+ 最近一年的 T_s / 收盘价。
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.ActiveParams(r.Context(), s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	days, err := s.store.Scores(r.Context(), market.CN.ID, p.Version(), "", "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{
		"market": market.CN.Name, "index": market.CN.Index, "paramsVersion": p.Version(), "params": p,
	}
	if sets, err := s.store.ListParams(r.Context()); err == nil {
		for _, ps := range sets {
			if ps.Active {
				out["paramsName"] = ps.Name
				break
			}
		}
	}
	raw := map[string]any{}
	if pt, ok, err := s.store.LatestPoint(r.Context(), datasource.CNHS300PE); err == nil && ok {
		raw["pe"], raw["peDate"] = jf(pt.Value), pt.Date
	}
	if pt, ok, err := s.store.LatestPoint(r.Context(), datasource.CNBondY10); err == nil && ok {
		raw["bondY10"], raw["bondDate"] = jf(pt.Value), pt.Date
	}
	out["raw"] = raw
	if _, phase, err := s.store.LatestPhase(r.Context(), market.CN.ID, p.Version()); err == nil {
		out["phase"] = phase
	}
	if evs, err := s.store.Events(r.Context(), market.CN.ID, p.Version()); err == nil {
		// 最近的择时事件（不含估值档切换和降级记录），同时计算分层持仓的当前信号叠加项
		var recent []json.RawMessage
		overlay := 0.0
		lastSig := ""
		for _, rawEv := range evs {
			var e struct {
				Date    string
				Type    string
				Observe bool
			}
			if json.Unmarshal(rawEv, &e) == nil {
				switch {
				case e.Type == engine.EvBuyDip && !e.Observe:
					overlay, lastSig = p.Position.Step, e.Date+" 底部信号"
				case e.Type == engine.EvSellPeak:
					overlay, lastSig = -p.Position.Step, e.Date+" 顶部信号"
				}
			}
		}
		for i := len(evs) - 1; i >= 0 && len(recent) < 5; i-- {
			var e struct{ Type string }
			if json.Unmarshal(evs[i], &e) == nil && e.Type != engine.EvTierChange && e.Type != engine.EvDegraded {
				recent = append(recent, evs[i])
			}
		}
		out["recentEvents"] = recent
		if n := len(days); n > 0 {
			base := p.Position.Base(days[n-1].Tier)
			weight := math.Max(0, math.Min(1, base+overlay))
			out["position"] = map[string]any{
				"tier": days[n-1].Tier, "base": jf(base), "overlay": jf(overlay), "weight": jf(weight), "lastSignal": lastSig,
			}
		}
	}
	if n := len(days); n > 0 {
		out["last"] = dayJSON(days[n-1])
		if n > 1 {
			out["prev"] = dayJSON(days[n-2])
		}
		from := max(0, n-250)
		recent := make([]map[string]any, 0, n-from)
		for _, d := range days[from:] {
			recent = append(recent, map[string]any{"date": d.Date, "close": jf(d.Close), "tS": jf(d.TS), "v": jf(d.V)})
		}
		out["recent"] = recent
	}
	writeJSON(w, http.StatusOK, out)
}

// scores：按列返回历史得分，供历史页画图。
func (s *Server) scores(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.ActiveParams(r.Context(), s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := r.URL.Query()
	days, err := s.store.Scores(r.Context(), market.CN.ID, p.Version(), q.Get("from"), q.Get("to"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n := len(days)
	cols := map[string][]any{}
	for _, k := range []string{"close", "v", "tS", "t1", "t2", "t3", "t4"} {
		cols[k] = make([]any, n)
	}
	dates := make([]string, n)
	tiers := make([]string, n)
	for i, d := range days {
		dates[i], tiers[i] = d.Date, d.Tier
		cols["close"][i], cols["v"][i], cols["tS"][i] = jf(d.Close), jf(d.V), jf(d.TS)
		cols["t1"][i], cols["t2"][i], cols["t3"][i], cols["t4"][i] = jf(d.T[0]), jf(d.T[1]), jf(d.T[2]), jf(d.T[3])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"paramsVersion": p.Version(), "dates": dates, "tiers": tiers, "series": cols,
		"thresholds": map[string]float64{"cheap": p.CheapBelow, "expensive": p.ExpensiveAbove, "extreme": p.Extreme},
	})
}
