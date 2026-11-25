package job

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/derived"
)

const (
	stockFetchFrom = "2008-06-01" // 个股日线最晚起点：保证 2010 年初已有 200 根（backfill_start 更早则用它）
	stockBatch     = 100          // 同步时每多少只股票提交一次
	// stockRevisitDays：每次同步往回重拉的自然日数（约 10 个交易日），覆盖本地已有数据。
	// 数据源当日先发布临时数据、事后校对（stockdb 盘后 15:00 更新、24:00 校对入库），不重拉会把临时值永久留在本地。
	stockRevisitDays = 15
)

// ErrNoStockSource 表示未配置个股数据源。
var ErrNoStockSource = errors.New("未配置个股数据源（config.yaml 的 stocks.source），无法同步个股日线")

// SetStocks 配置个股数据源（按顺序回退）与并发数；为空时不同步个股日线（本地已有日线仍可用于计算广度）。
func (r *Runner) SetStocks(srcs []ds.StockSource, concurrency int) {
	r.stocks, r.stockConc = srcs, max(1, concurrency)
}

// SetFundSources 单独指定场内基金日线的数据源顺序；不设置时与个股相同。
func (r *Runner) SetFundSources(srcs []ds.StockSource) { r.funds = srcs }

// SetSkipFunds 为 true 时同步个股日线时跳过场内基金。
func (r *Runner) SetSkipFunds(skip bool) { r.skipFunds = skip }

func (r *Runner) chain(kind string) []ds.StockSource {
	if kind == ds.KindFund && len(r.funds) > 0 {
		return r.funds
	}
	return r.stocks
}

func (r *Runner) stockNames() string {
	var names []string
	for _, s := range r.stocks {
		names = append(names, s.Name())
	}
	return strings.Join(names, " → ")
}

// listStocks 从第一个支持且成功的数据源取证券清单。
func (r *Runner) listStocks(ctx context.Context) ([]ds.Stock, string, error) {
	var errs []error
	for _, s := range r.stocks {
		list, err := s.ListStocks(ctx)
		if err == nil {
			return list, s.Name(), nil
		}
		if !errors.Is(err, ds.ErrNotSupported) {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
		}
	}
	return nil, "", errors.Join(errs...)
}

// fetchBars 依次尝试各数据源，返回第一个成功的结果及来源名。
func (r *Runner) fetchBars(ctx context.Context, code, kind, from string) ([]ds.StockBar, string, error) {
	var errs []error
	for _, s := range r.chain(kind) {
		bars, err := s.Bars(ctx, code, from, "")
		if err == nil {
			return bars, s.Name(), nil
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
	}
	return nil, "", errors.Join(errs...)
}

func breadthKind(full bool) string {
	if full {
		return "breadth-backfill"
	}
	return "breadth"
}

// StartBreadth 在后台同步个股日线并计算市场广度。
// 个股日线总是增量同步（可中断续传）；full=true 时从本地日线全量重算广度。
func (r *Runner) StartBreadth(full bool) (*Status, error) {
	if len(r.stocks) == 0 {
		return nil, ErrNoStockSource
	}
	id, err := r.begin(breadthKind(full))
	if err != nil {
		return nil, err
	}
	go r.runBreadthJob(context.Background(), id, full)
	return r.Current(), nil
}

// RunBreadth 同步执行（供 CLI 使用）。
func (r *Runner) RunBreadth(ctx context.Context, full bool) (*Status, error) {
	if len(r.stocks) == 0 {
		return nil, ErrNoStockSource
	}
	id, err := r.begin(breadthKind(full))
	if err != nil {
		return nil, err
	}
	r.runBreadthJob(ctx, id, full)
	return r.Current(), nil
}

func (r *Runner) runBreadthJob(ctx context.Context, id int64, full bool) {
	state, detail := "done", "个股日线同步、市场广度计算完成"
	err := r.syncStocks(ctx)
	if err == nil {
		err = r.computeDerived(ctx, true) // 近期日线可能被修正，总是从本地全量重算（约 20 秒）
	}
	if err != nil {
		state, detail = "failed", err.Error()
		r.logf("✗ %v", err)
	} else {
		r.score(ctx, &state, &detail)
	}
	r.finish(id, state, detail)
}

// hasLocalStocks 表示本地是否已有个股日线（完成过首次回填）。
func (r *Runner) hasLocalStocks(ctx context.Context) bool {
	st, err := r.store.StockStats(ctx)
	return err == nil && st.WithBars > 0
}

// syncStocks 从数据源增量同步个股与场内基金日线到本地库。证券清单取自第一个支持的数据源，
// 都不可用时沿用本地清单；每只证券从本地最后日期（含）开始拉取，数据源按顺序回退；
// 已退市且本地已有数据的跳过。可随时中断，已提交的批次保留。
func (r *Runner) syncStocks(ctx context.Context) error {
	if len(r.stocks) == 0 {
		return ErrNoStockSource
	}
	srcList, listFrom, err := r.listStocks(ctx)
	if err != nil || srcList == nil {
		if err != nil {
			r.logf("✗ 证券清单：%v", err)
		}
		r.logf("没有数据源能提供证券清单，沿用本地清单（新上市证券要等清单源恢复后才会加入）")
	} else if err := r.store.UpsertStockList(ctx, srcList, r.now()); err != nil {
		return err
	}
	local, err := r.store.StockList(ctx)
	if err != nil {
		return err
	}
	if len(local) == 0 {
		return errors.New("本地没有证券清单，且数据源无法提供")
	}
	inSource := make(map[string]bool, len(local))
	for _, s := range local {
		inSource[s.Code] = srcList == nil // 沿用本地清单时全部同步
	}
	for _, s := range srcList {
		inSource[s.Code] = true
	}
	start := min(r.start.Format("2006-01-02"), stockFetchFrom)
	type task struct{ code, kind, from string }
	var tasks []task
	for _, s := range local {
		if !inSource[s.Code] || (s.Delisted && s.LastDate != "") || (r.skipFunds && s.Kind == ds.KindFund) {
			continue
		}
		from := start
		if s.LastDate != "" {
			t, err := time.Parse("2006-01-02", s.LastDate)
			if err != nil {
				return fmt.Errorf("%s 最后日期异常：%w", s.Code, err)
			}
			from = max(start, t.AddDate(0, 0, -stockRevisitDays).Format("2006-01-02"))
		}
		tasks = append(tasks, task{s.Code, s.Kind, from})
	}
	// 本地还没有日线的排在最前：数据源限流（如腾讯 WAF）时，配额先用于首次回填，而不是重拉已有数据，回填才能逐次推进
	empty := map[string]bool{}
	for _, s := range local {
		empty[s.Code] = s.LastDate == ""
	}
	sort.SliceStable(tasks, func(i, j int) bool { return empty[tasks[i].code] && !empty[tasks[j].code] })
	if listFrom == "" {
		listFrom = "本地"
	}
	r.logf("个股 / 场内基金日线：清单来自 %s，共 %d 只（含退市），本次需同步 %d 只；数据源 %s", listFrom, len(local), len(tasks), r.stockNames())
	r.update(func(s *Status) { s.Total, s.Done, s.Current = len(tasks), 0, "stocks" })

	type result struct {
		code, kind, src string
		bars            []ds.StockBar
		err             error
	}
	jobs, results := make(chan task), make(chan result)
	var wg sync.WaitGroup
	for range r.stockConc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				bars, src, err := r.fetchBars(ctx, t.code, t.kind, t.from)
				select {
				case results <- result{t.code, t.kind, src, bars, err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, t := range tasks {
			select {
			case jobs <- t:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	batch, rows, pending := map[string]map[string][]ds.StockBar{}, 0, 0 // 来源 → 代码 → 日线
	bySrc := map[string]int{}
	flush := func() error {
		for src, b := range batch {
			if err := r.store.SaveStockBars(context.WithoutCancel(ctx), src, b); err != nil {
				return err
			}
		}
		batch, pending = map[string]map[string][]ds.StockBar{}, 0
		return nil
	}
	var failed []string
	failedStocks, stockTasks := 0, 0
	for _, t := range tasks {
		if t.kind == ds.KindStock {
			stockTasks++
		}
	}
	began := r.now()
	for res := range results {
		switch {
		case res.err != nil:
			failed = append(failed, res.code)
			if res.kind == ds.KindStock {
				failedStocks++
			}
			if len(failed) <= 5 {
				r.logf("✗ %s：%v", res.code, res.err)
			}
		case len(res.bars) > 0:
			if batch[res.src] == nil {
				batch[res.src] = map[string][]ds.StockBar{}
			}
			batch[res.src][res.code] = res.bars
			rows += len(res.bars)
			bySrc[res.src]++
			pending++
		}
		r.update(func(s *Status) { s.Done++ })
		if pending >= stockBatch {
			if err := flush(); err != nil {
				return err
			}
			if done := r.Current().Done; done%(stockBatch*5) < stockBatch {
				r.logf("… 已同步 %d / %d 只（%v）", done, len(tasks), r.now().Sub(began).Round(time.Second))
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("已中断（已同步的部分保留，下次继续）：%w", err)
	}
	msg := fmt.Sprintf("✓ 个股 / 场内基金日线：写入 %d 行，耗时 %v，来源 %v", rows, r.now().Sub(began).Round(time.Second), bySrc)
	if len(failed) > 0 {
		msg += fmt.Sprintf("；%d 只获取失败（股票 %d、基金 %d），下次重试", len(failed), failedStocks, len(failed)-failedStocks)
	}
	r.logf("%s", msg)
	// 只按股票判断：股票缺数会让市场广度失真；基金失败只影响基金本身，下次同步重试
	if stockTasks > 0 && failedStocks*20 > stockTasks {
		return fmt.Errorf("%d 只股票获取失败（超过 5%%），请检查数据源后重试", failedStocks)
	}
	return nil
}

// RunDerivedLocal 只从本地数据全量重算派生指标并打分（不请求任何数据源，供 CLI 使用）。
func (r *Runner) RunDerivedLocal(ctx context.Context) (*Status, error) {
	id, err := r.begin("derived")
	if err != nil {
		return nil, err
	}
	state, detail := "done", "派生指标重算完成"
	if err := r.computeDerived(ctx, true); err != nil {
		state, detail = "failed", err.Error()
		r.logf("✗ %v", err)
	} else {
		r.score(ctx, &state, &detail)
	}
	r.finish(id, state, detail)
	return r.Current(), nil
}

// computeDerived 依次运行全部派生指标（如市场广度），只读本地数据。full=true 时全量重算。
func (r *Runner) computeDerived(ctx context.Context, full bool) error {
	for _, d := range derived.All() {
		env := derived.Env{Store: r.store, Now: r.now, Logf: r.logf, Progress: func(done, total int) {
			r.update(func(s *Status) { s.Total, s.Done, s.Current = total, done, d.Name() })
		}}
		if err := d.Compute(ctx, env, full); err != nil {
			return fmt.Errorf("%s：%w", d.Name(), err)
		}
	}
	return nil
}
