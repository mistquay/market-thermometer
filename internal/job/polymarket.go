package job

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/store"
)

// PMSyncer 定时把 Polymarket 数据同步进本地库（7×24）：
//   - 系列（如 btc-up-or-down-15m）：已结算事件按结束时间倒序增量拉取（首次回溯 historyDays 天），连同未开始 / 进行中的事件；
//     已结算市场的第一个结果代币（Up / Yes）按 1 分钟拉取价格历史，供短期涨跌盘策略研究与回测；
//   - 关注的事件：刷新元数据，按小时拉取各市场第一个结果代币的价格历史。
type PMSyncer struct {
	store       *store.Store
	src         ds.PredictionSource
	series      []string
	historyDays int
	every       time.Duration
	maxHistory  int // 每轮最多拉取多少个市场的价格历史（首轮回填分多轮完成，避免长时间占用）
	now         func() time.Time
	logf        func(string, ...any)

	mu       sync.Mutex
	running  bool
	lastRun  time.Time
	lastLog  []string
	lastErrs int
}

func NewPMSyncer(st *store.Store, src ds.PredictionSource, series []string, historyDays int, every time.Duration) *PMSyncer {
	if historyDays <= 0 {
		historyDays = 30
	}
	if every <= 0 {
		every = 10 * time.Minute
	}
	return &PMSyncer{store: st, src: src, series: series, historyDays: historyDays, every: every, maxHistory: 600, now: time.Now, logf: log.Printf}
}

// Configured 表示是否配置了数据源。
func (p *PMSyncer) Configured() bool { return p != nil && p.src != nil }

// Source 返回数据源（供 Web 实时查询：搜索、订单簿）。
func (p *PMSyncer) Source() ds.PredictionSource { return p.src }

// Series 返回同步的系列。
func (p *PMSyncer) Series() []string { return append([]string(nil), p.series...) }

func (p *PMSyncer) Run(ctx context.Context) {
	t := time.NewTicker(p.every)
	defer t.Stop()
	for {
		if err := p.SyncOnce(ctx); err != nil && !errors.Is(err, errBusySync) {
			p.logf("Polymarket 同步：%v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *PMSyncer) SyncOnce(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return errBusySync
	}
	p.running = true
	p.mu.Unlock()
	var lines []string
	errs := 0
	logf := func(f string, a ...any) { lines = append(lines, p.now().Format("15:04:05 ")+fmt.Sprintf(f, a...)) }
	defer func() {
		p.mu.Lock()
		p.running, p.lastRun, p.lastLog, p.lastErrs = false, p.now(), lines, errs
		p.mu.Unlock()
	}()
	budget := p.maxHistory
	for _, s := range p.series {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, h, err := p.syncSeries(ctx, s, &budget)
		if err != nil {
			errs++
			logf("✗ %s：%v", s, err)
			continue
		}
		logf("✓ %s：事件 %d 个，价格历史 %d 个市场", s, n, h)
	}
	watched, err := p.store.PMWatched(ctx)
	if err != nil {
		return err
	}
	for _, slug := range watched {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := p.syncWatched(ctx, slug); err != nil {
			errs++
			logf("✗ 关注 %s：%v", slug, err)
		}
	}
	if len(watched) > 0 {
		logf("✓ 关注的事件：%d 个", len(watched))
	}
	if errs > 0 {
		return fmt.Errorf("%d 项失败", errs)
	}
	return nil
}

// syncSeries 拉取系列中新结算的事件与未结算事件，并为缺价格历史的已结算市场补历史。
func (p *PMSyncer) syncSeries(ctx context.Context, series string, budget *int) (events, hist int, err error) {
	since, through, err := p.store.PMSeriesSync(ctx, series)
	if err != nil {
		return 0, 0, err
	}
	horizon := p.now().Add(-time.Duration(p.historyDays) * 24 * time.Hour).Unix()
	// 已连续覆盖到回溯起点（留 1 天余量，日级系列一天才一个事件）就只拉 through 之后的；否则一直翻到回溯起点
	stop := horizon
	if through > 0 && since <= horizon+86400 {
		stop = max(through, horizon)
	} else {
		since = horizon
	}
	newest, complete := through, false
	closed := true
	for cursor, pages := "", 0; pages < 1000; pages++ {
		evs, next, err := p.src.EventsAfter(ctx, ds.PMEventQuery{Series: series, Closed: &closed, Order: "endDate", Asc: false, Limit: 100}, cursor)
		if err != nil {
			return events, hist, err
		}
		cursor = next
		var keep []ds.PMEvent
		for _, e := range evs {
			if e.End.Unix() >= stop {
				keep = append(keep, e)
				newest = max(newest, e.End.Unix())
			}
		}
		if err := p.store.SavePMEvents(context.WithoutCancel(ctx), keep, p.now()); err != nil {
			return events, hist, err
		}
		events += len(keep)
		if cursor == "" || len(keep) < len(evs) {
			complete = true
			break
		}
	}
	if !complete {
		return events, hist, fmt.Errorf("已结算事件超过 1000 页，未完成")
	}
	if err := p.store.SetPMSeriesSync(context.WithoutCancel(ctx), series, since, newest); err != nil {
		return events, hist, err
	}
	open := false
	evs, err := p.src.Events(ctx, ds.PMEventQuery{Series: series, Closed: &open, Order: "endDate", Asc: true, Limit: 100})
	if err != nil {
		return events, hist, err
	}
	if err := p.store.SavePMEvents(context.WithoutCancel(ctx), evs, p.now()); err != nil {
		return events, hist, err
	}
	events += len(evs)

	// 已结算市场的价格历史（1 分钟），覆盖开盘前同样长的一段：短期盘通常在计价区间开始前就可交易
	done, err := p.store.PMEvents(ctx, store.PMEventQuery{Series: series, Closed: &closed, From: horizon, Limit: 100_000})
	if err != nil {
		return events, hist, err
	}
	var todo []ds.PMMarket
	for _, e := range done {
		for _, m := range e.Markets {
			if len(todo) >= *budget {
				break
			}
			if len(m.Tokens) == 0 || m.Start.IsZero() || len(m.Prices) == 0 {
				continue
			}
			has, err := p.store.HasPMPrices(ctx, m.Tokens[0])
			if err != nil {
				return events, hist, err
			}
			if !has {
				todo = append(todo, m)
			}
		}
	}
	*budget -= len(todo)
	hist, err = p.fetchHistories(ctx, todo)
	return events, hist, err
}

// fetchHistories 并发（4 路）拉取已结算市场第一个结果代币的 1 分钟价格历史并保存。
func (p *PMSyncer) fetchHistories(ctx context.Context, todo []ds.PMMarket) (int, error) {
	jobs := make(chan ds.PMMarket)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	done := 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				span := m.End.Sub(m.Start)
				pts, err := p.src.PriceHistory(ctx, m.Tokens[0], m.Start.Add(-span).Unix(), m.End.Unix(), 1)
				if err == nil && len(pts) == 0 { // 没有成交：记一个占位点，避免每轮重复请求
					pts = []ds.PricePoint{{T: m.End.Unix(), P: m.Prices[0]}}
				}
				if err == nil {
					err = p.store.SavePMPrices(context.WithoutCancel(ctx), m.Tokens[0], pts)
				}
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("%s 价格历史：%w", m.Slug, err)
				}
				if err == nil {
					done++
				}
				mu.Unlock()
			}
		}()
	}
	for _, m := range todo {
		mu.Lock()
		stop := firstErr != nil
		mu.Unlock()
		if stop || ctx.Err() != nil {
			break
		}
		jobs <- m
	}
	close(jobs)
	wg.Wait()
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return done, firstErr
}

// syncWatched 刷新关注事件的元数据与各市场第一个结果代币的小时价格。
func (p *PMSyncer) syncWatched(ctx context.Context, slug string) error {
	evs, err := p.src.Events(ctx, ds.PMEventQuery{Slug: slug, Limit: 1})
	if err != nil {
		return err
	}
	if len(evs) == 0 {
		return fmt.Errorf("事件不存在")
	}
	if err := p.store.SavePMEvents(context.WithoutCancel(ctx), evs, p.now()); err != nil {
		return err
	}
	now := p.now().Unix()
	for _, m := range evs[0].Markets {
		if len(m.Tokens) == 0 {
			continue
		}
		old, err := p.store.PMPrices(ctx, m.Tokens[0], 0, 0)
		if err != nil {
			return err
		}
		from := now - int64(p.historyDays)*86400
		if n := len(old); n > 0 {
			from = old[n-1].T
		}
		to := now
		if m.Closed && !m.End.IsZero() {
			to = min(to, m.End.Unix()+3600)
		}
		if from >= to {
			continue
		}
		pts, err := p.src.PriceHistory(ctx, m.Tokens[0], from, to, 60)
		if err != nil {
			return err
		}
		if err := p.store.SavePMPrices(context.WithoutCancel(ctx), m.Tokens[0], pts); err != nil {
			return err
		}
	}
	return nil
}

// PMStatus 是同步器状态。
type PMStatus = CryptoStatus

func (p *PMSyncer) Status() PMStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PMStatus{Running: p.running, LastRun: p.lastRun, Errors: p.lastErrs, Log: append([]string{}, p.lastLog...), Every: p.every.String()}
}
