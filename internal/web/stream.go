package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// stream 提供认证后的 SSE 实时事件流（每 2.5s 推送任务状态、PM 盘口/影子信号与 HITL/套利计数），替代前端轮询。
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func() bool {
		payload := map[string]any{
			"at":  s.now().UTC().Format(time.RFC3339),
			"job": s.runner.Current(),
		}
		if s.opt.PMShadow != nil {
			payload["pmShadow"] = s.opt.PMShadow.Status()
		}
		if s.opt.PMBook != nil {
			payload["pmBook"] = s.opt.PMBook.Status()
		}
		if s.opt.PMArb != nil {
			if snap, hist := s.opt.PMArb.Last(); snap != nil {
				payload["arbPositive"] = snap.PositiveCount
				payload["arbHistory"] = len(hist)
			}
		}
		if props, err := s.store.PMProposals(r.Context(), 50, s.now()); err == nil {
			pending := 0
			for _, pr := range props {
				if pr.Status == "pending" {
					pending++
				}
			}
			payload["pendingProposals"] = pending
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: tick\ndata: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send() {
		return
	}
	t := time.NewTicker(2500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			if !send() {
				return
			}
		}
	}
}

// pmEventMarkers 返回某个 Polymarket 事件的策略影子信号与模拟盘成交标记，用于叠加在价格走势图上。
func (s *Server) pmEventMarkers(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !rePMSlug.MatchString(slug) {
		writeErr(w, http.StatusBadRequest, "事件 slug 无效")
		return
	}
	sigs, err := s.store.PMSignalsBySlug(r.Context(), slug)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	orders, err := s.store.PMOrdersBySlug(r.Context(), slug)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signals": sigs, "orders": orders})
}

// securityFills 返回某只 A 股 / 场内基金跨模拟账户的已成交记录，用于行情页买卖点复盘叠加。
func (s *Server) securityFills(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if len(code) != 6 {
		writeErr(w, http.StatusBadRequest, "证券代码无效")
		return
	}
	fills, err := s.store.SimFillsByCode(r.Context(), code)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fills)
}
