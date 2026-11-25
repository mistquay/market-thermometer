// Package job 是后台任务层（回填、每日同步），Web 与 CLI 共用。
package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/market"
	"market-thermometer/internal/store"
)

// ErrBusy 表示已有任务在运行。
var ErrBusy = errors.New("已有任务在运行")

// incrementalOverlap：增量同步时向前多取的天数，用于覆盖数据源对近期数据的修订。
const incrementalOverlap = 10

// Status 是任务的实时状态，供 Web 轮询。
type Status struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	State      string    `json:"state"` // running | done | failed | partial
	Total      int       `json:"total"`
	Done       int       `json:"done"`
	Current    string    `json:"current"`
	Log        []string  `json:"log"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

// Runner 串行执行后台任务：同一时刻只允许一个。
type Runner struct {
	store *store.Store
	hub   *ds.Hub
	mkt   market.Market
	start time.Time
	now   func() time.Time

	stocks    []ds.StockSource // 个股 / 场内基金日线数据源，按顺序回退；为空则不同步
	stockConc int
	funds     []ds.StockSource // 场内基金日线数据源顺序；为空时同 stocks
	skipFunds bool             // 不同步场内基金日线
	fallbacks atomic.Int64     // 本次同步回退到次级数据源的次数
	fundNAV   []ds.FundSource  // 场外基金净值数据源

	mu  sync.Mutex
	cur *Status
}

func NewRunner(st *store.Store, hub *ds.Hub, mkt market.Market, backfillStart time.Time) *Runner {
	return &Runner{store: st, hub: hub, mkt: mkt, start: backfillStart, now: time.Now}
}

// SourceNames 返回已配置的个股日线源、场内基金日线源与场外基金净值源名称。
func (r *Runner) SourceNames() (stocks, funds, fundNAV []string) {
	for _, s := range r.stocks {
		stocks = append(stocks, s.Name())
	}
	for _, s := range r.chain(ds.KindFund) {
		if !r.skipFunds {
			funds = append(funds, s.Name())
		}
	}
	for _, s := range r.fundNAV {
		fundNAV = append(fundNAV, s.Name())
	}
	return
}

// Current 返回最近一个任务的状态快照（可能已结束）；从未运行过时返回 nil。
func (r *Runner) Current() *Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil {
		return nil
	}
	c := *r.cur
	c.Log = append([]string(nil), r.cur.Log...)
	return &c
}

func (r *Runner) update(f func(s *Status)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur != nil {
		f(r.cur)
	}
}

func (r *Runner) logf(format string, args ...any) {
	line := r.now().In(ds.Shanghai).Format("15:04:05 ") + fmt.Sprintf(format, args...)
	r.update(func(s *Status) { s.Log = append(s.Log, line) })
}

// StartSync 在后台启动同步任务。full=true 时从 backfill_start 全量拉取，否则从库中最新日期往前 10 天增量拉取。
func (r *Runner) StartSync(full bool) (*Status, error) {
	id, err := r.begin(syncKind(full))
	if err != nil {
		return nil, err
	}
	go r.runSync(context.Background(), id, full)
	return r.Current(), nil
}

// RunSync 同步执行（供 CLI 使用），返回最终状态。
func (r *Runner) RunSync(ctx context.Context, full bool) (*Status, error) {
	id, err := r.begin(syncKind(full))
	if err != nil {
		return nil, err
	}
	r.runSync(ctx, id, full)
	return r.Current(), nil
}

func syncKind(full bool) string {
	if full {
		return "backfill"
	}
	return "sync"
}

func (r *Runner) begin(kind string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur != nil && r.cur.State == "running" {
		return 0, ErrBusy
	}
	now := r.now()
	id, err := r.store.JobStart(context.Background(), kind, now)
	if err != nil {
		return 0, err
	}
	r.cur = &Status{ID: id, Kind: kind, State: "running", StartedAt: now}
	return id, nil
}

func (r *Runner) runSync(ctx context.Context, id int64, full bool) {
	var metrics []ds.Metric
	for _, m := range ds.Metrics() {
		if len(r.hub.Route(m.ID)) > 0 {
			metrics = append(metrics, m)
		}
	}
	r.update(func(s *Status) { s.Total = len(metrics) })

	to := r.now().In(ds.Shanghai)
	var failed []string
	for _, m := range metrics {
		r.update(func(s *Status) { s.Current = string(m.ID) })
		if err := r.syncOne(ctx, m, full, to); err != nil {
			failed = append(failed, string(m.ID))
			r.logf("✗ %s：%v", m.Name, err)
		}
		r.update(func(s *Status) { s.Done++ })
		if ctx.Err() != nil {
			break
		}
	}

	state, detail := "done", fmt.Sprintf("%d 个指标同步完成", len(metrics))
	switch {
	case len(failed) == len(metrics) && len(metrics) > 0:
		state, detail = "failed", "全部失败："+strings.Join(failed, ", ")
	case len(failed) > 0:
		state, detail = "partial", "部分失败："+strings.Join(failed, ", ")
	}
	r.logf("%s", detail)

	// 个股日线与市场广度增量：首次回填耗时较长，需在数据页手动触发；之后每日同步自动进行
	if ctx.Err() == nil && r.hasLocalStocks(ctx) {
		var err error
		if len(r.stocks) > 0 {
			err = r.syncStocks(ctx)
		} else {
			r.logf("未配置个股数据源，跳过个股日线同步，广度只用本地已有数据")
		}
		if err == nil {
			err = r.computeDerived(ctx, true) // 近期日线可能被修正，从本地全量重算
		}
		if err != nil {
			r.logf("✗ 市场广度：%v", err)
			if state == "done" {
				state = "partial"
			}
			detail += "；市场广度失败"
		}
	} else if len(r.stocks) > 0 {
		r.logf("个股日线尚未回填，跳过市场广度（在数据页点击“回填市场广度”）")
	}
	// 自选场外基金净值
	if len(r.fundNAV) > 0 && ctx.Err() == nil {
		if err := r.syncFunds(ctx); err != nil {
			r.logf("✗ 基金净值：%v", err)
			if state == "done" {
				state = "partial"
			}
			detail += "；基金净值失败"
		}
	}
	r.score(ctx, &state, &detail)
	r.finish(id, state, detail)
}

// score：按生效参数全量重算得分（几百毫秒，幂等）。
func (r *Runner) score(ctx context.Context, state, detail *string) {
	if ctx.Err() != nil {
		return
	}
	r.update(func(s *Status) { s.Current = "score" })
	res, err := Score(ctx, r.store, r.hub, r.mkt, r.now())
	if err != nil {
		r.logf("✗ 打分失败：%v", err)
		if *state == "done" {
			*state = "partial"
		}
		*detail += "；打分失败"
		return
	}
	r.logf("✓ 打分完成：参数 %s，%d 个交易日，最新 %s", res.Version, res.Days, res.Last.Date)
}

func (r *Runner) finish(id int64, state, detail string) {
	finished := r.now()
	_ = r.store.JobFinish(context.Background(), id, state, detail, finished)
	r.update(func(s *Status) { s.State, s.Current, s.FinishedAt = state, "", finished })
}

func (r *Runner) syncOne(ctx context.Context, m ds.Metric, full bool, to time.Time) error {
	from := r.start
	if !full {
		last, err := r.store.LastDate(ctx, m.ID)
		if err != nil {
			return err
		}
		if last != "" {
			t, err := time.Parse("2006-01-02", last)
			if err != nil {
				return err
			}
			from = t.AddDate(0, 0, -incrementalOverlap)
		}
	}
	src, pts, err := r.hub.Fetch(ctx, m.ID, from, to)
	if err != nil {
		return err
	}
	n, err := r.store.UpsertPoints(ctx, m.ID, src, pts, r.now())
	if err != nil {
		return err
	}
	rangeText := "无新数据"
	if n > 0 {
		rangeText = pts[0].Date + " ~ " + pts[n-1].Date
	}
	r.logf("✓ %s：来自 %s，%d 条（%s）", m.Name, src, n, rangeText)
	return nil
}

// TestStockSource 直接从指定个股数据源取一只证券的日线（插件管理页的测试调用）。
func (r *Runner) TestStockSource(ctx context.Context, name, code, from string) ([]ds.StockBar, error) {
	for _, s := range append(append([]ds.StockSource{}, r.stocks...), r.funds...) {
		if s.Name() == name {
			return s.Bars(ctx, code, from, "")
		}
	}
	return nil, fmt.Errorf("个股数据源 %s 未启用", name)
}
