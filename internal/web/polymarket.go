package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/store"
)

// ---- 预测市场（Polymarket）：本地事件与价格历史为主，搜索 / 订单簿 / 未结算事件实时取 ----

var (
	rePMSlug  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,199}$`)
	rePMToken = regexp.MustCompile(`^\d{1,100}$`)
)

func (s *Server) pmReady(w http.ResponseWriter) bool {
	if !s.opt.PM.Configured() {
		writeErr(w, http.StatusBadRequest, "未配置预测市场数据源")
		return false
	}
	return true
}

func (s *Server) pmState(w http.ResponseWriter, r *http.Request) {
	p := s.opt.PM
	if !p.Configured() {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	ctx := r.Context()
	stats, err := s.store.PMSeriesStatsAll(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	slugs, err := s.store.PMWatched(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	watched := []ds.PMEvent{}
	for _, slug := range slugs {
		e, err := s.store.PMEventBySlug(ctx, slug)
		if errors.Is(err, sql.ErrNoRows) {
			e = ds.PMEvent{Slug: slug, Title: slug} // 还没同步到
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		watched = append(watched, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "source": p.Source().Name(), "series": p.Series(),
		"status": p.Status(), "stats": stats, "watched": watched})
}

// pmEvents 读取本地某系列事件：state=upcoming 为未结束（按结束时间升序），否则为已结算（倒序）。
func (s *Server) pmEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	series := q.Get("series")
	if series == "" || !rePMSlug.MatchString(series) {
		writeErr(w, http.StatusBadRequest, "系列无效")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	now := s.now().Unix()
	sq := store.PMEventQuery{Series: series, Limit: limit}
	if q.Get("state") == "upcoming" {
		open := false
		sq.Closed, sq.From, sq.Asc = &open, now, true // 只要还没结束的，跳过早该结束却仍挂着的陈旧事件
	} else {
		closed := true
		sq.Closed = &closed
		if before, _ := strconv.ParseInt(q.Get("before"), 10, 64); before > 0 {
			sq.To = before
		}
	}
	evs, err := s.store.PMEvents(r.Context(), sq)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if evs == nil {
		evs = []ds.PMEvent{}
	}
	writeJSON(w, http.StatusOK, evs)
}

// pmEvent 返回一个事件：未结算或本地没有时实时取并存入本地。
func (s *Server) pmEvent(w http.ResponseWriter, r *http.Request) {
	if !s.pmReady(w) {
		return
	}
	slug := r.PathValue("slug")
	if !rePMSlug.MatchString(slug) {
		writeErr(w, http.StatusBadRequest, "事件 slug 无效")
		return
	}
	ctx := r.Context()
	e, err := s.store.PMEventBySlug(ctx, slug)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err != nil || !e.Closed {
		live, lerr := s.fetchPMEvent(ctx, slug)
		switch {
		case lerr == nil:
			e = live
		case err != nil: // 本地也没有
			writeErr(w, http.StatusBadGateway, lerr.Error())
			return
		}
	}
	watched, err := s.store.PMWatched(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	isWatched := false
	for _, x := range watched {
		isWatched = isWatched || x == slug
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": e, "watched": isWatched})
}

func (s *Server) fetchPMEvent(ctx context.Context, slug string) (ds.PMEvent, error) {
	evs, err := s.opt.PM.Source().Events(ctx, ds.PMEventQuery{Slug: slug, Limit: 1})
	if err != nil {
		return ds.PMEvent{}, err
	}
	if len(evs) == 0 {
		return ds.PMEvent{}, errors.New("事件不存在")
	}
	if err := s.store.SavePMEvents(context.WithoutCancel(ctx), evs[:1], s.now()); err != nil {
		return ds.PMEvent{}, err
	}
	return evs[0], nil
}

func (s *Server) pmSearch(w http.ResponseWriter, r *http.Request) {
	if !s.pmReady(w) {
		return
	}
	text := r.URL.Query().Get("q")
	if text == "" || len(text) > 100 {
		writeErr(w, http.StatusBadRequest, "请输入 1–100 个字符")
		return
	}
	evs, err := s.opt.PM.Source().Search(r.Context(), text, 20)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, evs)
}

// pmPrices 返回代币价格：本地有该区间的点就用本地，否则实时取（不入库，关注的事件由同步器入库）。
func (s *Server) pmPrices(w http.ResponseWriter, r *http.Request) {
	if !s.pmReady(w) {
		return
	}
	q := r.URL.Query()
	token := q.Get("token")
	if !rePMToken.MatchString(token) {
		writeErr(w, http.StatusBadRequest, "代币 ID 无效")
		return
	}
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	fidelity, _ := strconv.Atoi(q.Get("fidelity"))
	now := s.now().Unix()
	if to <= 0 || to > now {
		to = now
	}
	if from <= 0 || from >= to {
		from = to - 7*86400
	}
	if fidelity <= 0 {
		fidelity = max(1, int((to-from)/60/500)) // 约 500 个点
	}
	ctx := r.Context()
	pts, err := s.store.PMPrices(ctx, token, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	src := "local"
	if len(pts) < 2 {
		if pts, err = s.opt.PM.Source().PriceHistory(ctx, token, from, to, fidelity); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		src = s.opt.PM.Source().Name()
	}
	if pts == nil {
		pts = []ds.PricePoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"points": pts, "source": src})
}

func (s *Server) pmBook(w http.ResponseWriter, r *http.Request) {
	if !s.pmReady(w) {
		return
	}
	token := r.URL.Query().Get("token")
	if !rePMToken.MatchString(token) {
		writeErr(w, http.StatusBadRequest, "代币 ID 无效")
		return
	}
	b, err := s.opt.PM.Source().Book(r.Context(), token)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if b.Bids == nil {
		b.Bids = []ds.BookLevel{}
	}
	if b.Asks == nil {
		b.Asks = []ds.BookLevel{}
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) pmWatch(w http.ResponseWriter, r *http.Request) {
	if !s.pmReady(w) {
		return
	}
	var req struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !rePMSlug.MatchString(req.Slug) {
		writeErr(w, http.StatusBadRequest, "事件 slug 无效")
		return
	}
	ctx := r.Context()
	if _, err := s.store.PMEventBySlug(ctx, req.Slug); errors.Is(err, sql.ErrNoRows) {
		if _, err := s.fetchPMEvent(ctx, req.Slug); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.WatchPM(ctx, req.Slug, s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) pmUnwatch(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !rePMSlug.MatchString(slug) {
		writeErr(w, http.StatusBadRequest, "事件 slug 无效")
		return
	}
	if err := s.store.UnwatchPM(r.Context(), slug); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) startPMSync(w http.ResponseWriter, r *http.Request) {
	if !s.pmReady(w) {
		return
	}
	if s.opt.PM.Status().Running {
		writeErr(w, http.StatusConflict, "Polymarket 同步正在进行")
		return
	}
	go s.opt.PM.SyncOnce(context.Background()) // 与请求无关，不能用 r.Context()
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}
