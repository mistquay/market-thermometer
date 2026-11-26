// Package web 提供 HTTP API 与内嵌前端。
package web

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
	"market-thermometer/internal/notify"
	"market-thermometer/internal/pmtrade"
	"market-thermometer/internal/store"
)

type Options struct {
	StockSource  string // 个股数据源名称（展示用）
	PasswordHash string
	TrustProxy   bool        // 是否信任反向代理传入的 X-Real-IP（用于登录限速）
	Static       fs.FS       // 前端构建产物；为 nil 时不托管静态文件
	Pusher       *job.Pusher // 推送；为 nil 时用直连、无代理的默认配置
	// PluginOptions 是内置插件在 config.yaml 中的选项（插件 ID → 选项），管理页只读展示。
	PluginOptions map[string]map[string]any
	Crypto        *job.CryptoSyncer    // 加密行情同步器；为 nil 时加密页显示未配置
	PM            *job.PMSyncer        // Polymarket 同步器；为 nil 时预测市场页显示未配置
	PMBook        *job.PMBookCollector // 盘口采集器；为 nil 时不采集
}

type Server struct {
	opt     Options
	store   *store.Store
	hub     *ds.Hub
	runner  *job.Runner
	pusher  *job.Pusher
	limiter *loginLimiter

	pmSim       *pmtrade.Sim // 预测市场模拟盘；未配置预测市场时为 nil
	stockSource string       // 个股数据源名称，仅用于展示
	now         func() time.Time
}

func New(opt Options, st *store.Store, hub *ds.Hub, runner *job.Runner) *Server {
	pusher := opt.Pusher
	if pusher == nil {
		c := &http.Client{Timeout: 30 * time.Second}
		pusher = job.NewPusher(st, notify.Env{HTTP: c, Proxy: c}, market.CN)
	}
	srv := &Server{opt: opt, stockSource: opt.StockSource, store: st, hub: hub, runner: runner, pusher: pusher, limiter: newLoginLimiter(), now: time.Now}
	if opt.PM.Configured() {
		srv.pmSim = pmtrade.NewSim(st, opt.PM.Source())
	}
	return srv
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/auth/login", s.login)
	api.HandleFunc("POST /api/auth/logout", s.auth(s.logout))
	api.HandleFunc("GET /api/auth/me", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	api.HandleFunc("GET /api/metrics", s.auth(s.metrics))
	api.HandleFunc("GET /api/series", s.auth(s.series))
	api.HandleFunc("GET /api/overview", s.auth(s.overview))
	api.HandleFunc("GET /api/scores", s.auth(s.scores))
	api.HandleFunc("GET /api/signals", s.auth(s.signals))
	api.HandleFunc("GET /api/params", s.auth(s.params))
	api.HandleFunc("POST /api/params", s.auth(s.saveParams))
	api.HandleFunc("POST /api/backtest", s.auth(s.runBacktest))
	api.HandleFunc("POST /api/backtest/robust", s.auth(s.runRobust))
	api.HandleFunc("POST /api/jobs/sync", s.auth(s.startSync))
	api.HandleFunc("POST /api/jobs/breadth", s.auth(s.startBreadth))
	api.HandleFunc("GET /api/stocks/stats", s.auth(s.stockStats))
	api.HandleFunc("GET /api/funds", s.auth(s.funds))
	api.HandleFunc("GET /api/funds/search", s.auth(s.searchFunds))
	api.HandleFunc("POST /api/funds/watch", s.auth(s.watchFund))
	api.HandleFunc("DELETE /api/funds/watch/{code}", s.auth(s.unwatchFund))
	api.HandleFunc("GET /api/funds/{code}/nav", s.auth(s.fundNAV))
	api.HandleFunc("POST /api/jobs/funds", s.auth(s.startFunds))
	api.HandleFunc("GET /api/quality", s.auth(s.quality))
	api.HandleFunc("POST /api/jobs/crosscheck", s.auth(s.startCrossCheck))
	api.HandleFunc("POST /api/jobs/gapfill", s.auth(s.startGapFill))
	api.HandleFunc("POST /api/jobs/ext-backfill", s.auth(s.startExtBackfill))
	api.HandleFunc("GET /api/securities/search", s.auth(s.searchSecurities))
	api.HandleFunc("GET /api/securities/{kind}/{code}/series", s.auth(s.securitySeries))
	api.HandleFunc("POST /api/jobs/names", s.auth(s.startNames))
	api.HandleFunc("GET /api/portfolios", s.auth(s.portfolios))
	api.HandleFunc("POST /api/portfolios", s.auth(s.savePortfolio))
	api.HandleFunc("PUT /api/portfolios/{id}", s.auth(s.savePortfolio))
	api.HandleFunc("DELETE /api/portfolios/{id}", s.auth(s.deletePortfolio))
	api.HandleFunc("GET /api/portfolios/{id}/analysis", s.auth(s.analyzePortfolio))
	api.HandleFunc("GET /api/sim/accounts", s.auth(s.simAccounts))
	api.HandleFunc("POST /api/sim/accounts", s.auth(s.createSimAccount))
	api.HandleFunc("GET /api/sim/accounts/{id}", s.auth(s.simAccount))
	api.HandleFunc("DELETE /api/sim/accounts/{id}", s.auth(s.deleteSimAccount))
	api.HandleFunc("POST /api/sim/accounts/{id}/orders", s.auth(s.placeSimOrders))
	api.HandleFunc("POST /api/sim/accounts/{id}/orders/{oid}/cancel", s.auth(s.cancelSimOrder))
	api.HandleFunc("POST /api/sim/accounts/{id}/rebalance", s.auth(s.planSimRebalance))
	api.HandleFunc("GET /api/plugins", s.auth(s.plugins))
	api.HandleFunc("PUT /api/plugins/{kind}/{name}", s.auth(s.savePlugin))
	api.HandleFunc("POST /api/plugins/{kind}/{name}/test", s.auth(s.testPlugin))
	api.HandleFunc("GET /api/notify", s.auth(s.notifyState))
	api.HandleFunc("POST /api/notify/settings", s.auth(s.saveNotifySettings))
	api.HandleFunc("POST /api/notify/channels", s.auth(s.saveChannel))
	api.HandleFunc("PUT /api/notify/channels/{id}", s.auth(s.saveChannel))
	api.HandleFunc("DELETE /api/notify/channels/{id}", s.auth(s.deleteChannel))
	api.HandleFunc("POST /api/notify/channels/{id}/test", s.auth(s.testChannel))
	api.HandleFunc("GET /api/crypto", s.auth(s.cryptoState))
	api.HandleFunc("GET /api/crypto/klines", s.auth(s.cryptoKlines))
	api.HandleFunc("GET /api/crypto/price", s.auth(s.cryptoPrice))
	api.HandleFunc("POST /api/crypto/sync", s.auth(s.startCryptoSync))
	api.HandleFunc("GET /api/pm", s.auth(s.pmState))
	api.HandleFunc("GET /api/pm/events", s.auth(s.pmEvents))
	api.HandleFunc("GET /api/pm/events/{slug}", s.auth(s.pmEvent))
	api.HandleFunc("GET /api/pm/search", s.auth(s.pmSearch))
	api.HandleFunc("GET /api/pm/prices", s.auth(s.pmPrices))
	api.HandleFunc("GET /api/pm/book", s.auth(s.pmBook))
	api.HandleFunc("GET /api/pm/books", s.auth(s.pmBooks))
	api.HandleFunc("POST /api/pm/watch", s.auth(s.pmWatch))
	api.HandleFunc("DELETE /api/pm/watch/{slug}", s.auth(s.pmUnwatch))
	api.HandleFunc("POST /api/pm/sync", s.auth(s.startPMSync))
	api.HandleFunc("GET /api/pm/sim", s.auth(s.pmSimState))
	api.HandleFunc("POST /api/pm/sim/accounts", s.auth(s.createPMSimAccount))
	api.HandleFunc("GET /api/pm/sim/accounts/{id}", s.auth(s.pmSimAccount))
	api.HandleFunc("PUT /api/pm/sim/accounts/{id}/params", s.auth(s.setPMSimParams))
	api.HandleFunc("DELETE /api/pm/sim/accounts/{id}", s.auth(s.deletePMSimAccount))
	api.HandleFunc("POST /api/pm/sim/orders", s.auth(s.placePMSimOrder))
	api.HandleFunc("POST /api/pm/halt", s.auth(s.setPMHalt))
	api.HandleFunc("GET /api/jobs/current", s.auth(s.currentJob))
	api.HandleFunc("GET /api/jobs", s.auth(s.recentJobs))
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})

	root := http.NewServeMux()
	root.Handle("/api/", s.guard(api))
	if s.opt.Static != nil {
		root.Handle("/", spaHandler(s.opt.Static))
	}
	return root
}

// guard：写请求校验 Origin，限制请求体大小，统一安全头。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeErr(w, http.StatusForbidden, "origin mismatch")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err == nil {
			ok, err := s.store.ValidSession(r.Context(), c.Value, s.now())
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if ok {
				h(w, r)
				return
			}
		}
		writeErr(w, http.StatusUnauthorized, "未登录")
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.opt.TrustProxy)
	now := s.now()
	if blocked, left := s.limiter.Blocked(ip, now); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(left.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "登录失败次数过多，请 "+strconv.Itoa(int(left.Minutes())+1)+" 分钟后再试")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(s.opt.PasswordHash), []byte(req.Password)) != nil {
		s.limiter.Fail(ip, now)
		writeErr(w, http.StatusUnauthorized, "口令错误")
		return
	}
	s.limiter.Reset(ip)
	token := newToken()
	if err := s.store.CreateSession(r.Context(), token, now, sessionTTL); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type metricView struct {
	ds.Metric
	Route    []string         `json:"route"`
	Coverage []store.Coverage `json:"coverage"`
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	cov, err := s.store.Coverage(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	byMetric := map[ds.MetricID][]store.Coverage{}
	for _, c := range cov {
		byMetric[c.Metric] = append(byMetric[c.Metric], c)
	}
	out := []metricView{}
	for _, m := range ds.Metrics() {
		route := s.hub.Route(m.ID)
		if route == nil {
			route = []string{} // 派生指标（如市场广度）没有数据源插件；JSON 须为 [] 而非 null
		}
		out = append(out, metricView{Metric: m, Route: route, Coverage: byMetric[m.ID]})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := ds.MetricID(q.Get("metric"))
	if _, ok := ds.Lookup(id); !ok {
		writeErr(w, http.StatusBadRequest, "unknown metric")
		return
	}
	source := q.Get("source")
	if source == "" {
		// 未指定来源时：按路由优先级取第一个库里有数据的来源
		cov, err := s.store.Coverage(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		have := map[string]bool{}
		for _, c := range cov {
			if c.Metric == id {
				have[c.Source] = true
			}
		}
		for _, name := range s.hub.Route(id) {
			if have[name] {
				source = name
				break
			}
		}
	}
	pts := []ds.Point{}
	if source != "" {
		var err error
		if pts, err = s.store.Series(r.Context(), id, source, q.Get("from"), q.Get("to")); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"metric": id, "source": source, "points": pts})
}

func (s *Server) startSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Full bool `json:"full"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	st, err := s.runner.StartSync(req.Full)
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

func (s *Server) startBreadth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Full bool `json:"full"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	st, err := s.runner.StartBreadth(req.Full)
	switch {
	case errors.Is(err, job.ErrBusy):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, job.ErrNoStockSource):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, st)
	}
}

func (s *Server) stockStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.StockStats(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": st, "source": s.stockSource})
}

func (s *Server) currentJob(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.runner.Current())
}

func (s *Server) recentJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.RecentJobs(r.Context(), 20)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []store.JobRun{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

// spaHandler 托管前端；不存在的路径回落到 index.html（前端路由）。
func spaHandler(static fs.FS) http.Handler {
	files := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if fi, err := fs.Stat(static, p); err == nil && !fi.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(static, "index.html")
		if err != nil {
			http.Error(w, "前端尚未构建：请在 web/ 目录执行 pnpm build 后重新编译", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("web: encode: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
