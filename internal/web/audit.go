package web

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// audit 追加一条审计记录（写库失败不影响主流程）。
func (s *Server) audit(ctx context.Context, action, target string, detail map[string]any) {
	_ = s.store.AddAudit(context.WithoutCancel(ctx), "web", action, target, detail, s.now())
}

// healthz 是无鉴权的探活接口，供负载均衡 / 监控脚本检查服务与数据库、盘口采集器状态。
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	dbErr := s.store.Ping(ctx)
	ok := dbErr == nil
	out := map[string]any{
		"ok":        ok,
		"uptimeSec": int64(s.now().Sub(s.startedAt).Seconds()),
		"dbOk":      ok,
	}
	if dbErr != nil {
		out["dbError"] = dbErr.Error()
	}
	if s.opt.PMBook.Configured() {
		st := s.opt.PMBook.Status()
		stale := st.Running && !st.LastRun.IsZero() && s.now().Sub(st.LastRun) > 2*time.Minute
		out["pmBook"] = map[string]any{
			"running": st.Running,
			"ticks":   st.Ticks,
			"errors":  st.Errors,
			"lastRun": st.LastRun,
			"stale":   stale,
		}
	}
	code := http.StatusOK
	if !ok {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, out)
}

// audits 返回最近的审计日志。
func (s *Server) audits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	list, err := s.store.RecentAudits(r.Context(), q.Get("action"), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}
