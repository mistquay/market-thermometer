package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"market-thermometer/internal/breadth"
	ds "market-thermometer/internal/datasource"
)

const (
	breadthMA        = 200          // 均线周期
	stockFetchFrom   = "2008-06-01" // 个股日线最晚起点：保证 2010 年初已有 200 根（backfill_start 更早则用它）
	breadthCountFrom = "2010-01-01" // 广度序列起点
	stockBatch       = 100          // 同步时每多少只股票提交一次
	breadthSource    = "local"      // 广度由本地个股日线派生
)

// ErrNoStockSource 表示未配置个股数据源。
var ErrNoStockSource = errors.New("未配置个股数据源（config.yaml 的 stocks.source），无法同步个股日线")

// SetStocks 配置个股数据源与并发数；src 为 nil 时不同步个股日线（已存在本地的日线仍可用于计算广度）。
func (r *Runner) SetStocks(src ds.StockSource, concurrency int) {
	r.stocks, r.stockConc = src, max(1, concurrency)
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
	if r.stocks == nil {
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
	if r.stocks == nil {
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
		err = r.computeBreadth(ctx, full)
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

// syncStocks 从个股数据源增量同步日线到本地库。每只股票从本地最后日期（含）开始拉取，
// 已退市且本地已有数据的股票跳过。可随时中断，已提交的批次保留。
func (r *Runner) syncStocks(ctx context.Context) error {
	if r.stocks == nil {
		return ErrNoStockSource
	}
	srcList, err := r.stocks.ListStocks(ctx)
	if err != nil {
		return err
	}
	if err := r.store.UpsertStockList(ctx, srcList, r.now()); err != nil {
		return err
	}
	local, err := r.store.StockList(ctx)
	if err != nil {
		return err
	}
	inSource := make(map[string]bool, len(srcList))
	for _, s := range srcList {
		inSource[s.Code] = true
	}
	start := min(r.start.Format("2006-01-02"), stockFetchFrom)
	type task struct{ code, from string }
	var tasks []task
	for _, s := range local {
		if !inSource[s.Code] || (s.Delisted && s.LastDate != "") {
			continue
		}
		from := start
		if s.LastDate != "" {
			from = s.LastDate
		}
		tasks = append(tasks, task{s.Code, from})
	}
	r.logf("个股日线：数据源 %s 共 %d 只（含退市），本次需同步 %d 只", r.stocks.Name(), len(srcList), len(tasks))
	r.update(func(s *Status) { s.Total, s.Done, s.Current = len(tasks), 0, "stocks" })

	type result struct {
		code string
		bars []ds.StockBar
		err  error
	}
	jobs, results := make(chan task), make(chan result)
	var wg sync.WaitGroup
	for range r.stockConc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				bars, err := r.stocks.Bars(ctx, t.code, t.from, "")
				select {
				case results <- result{t.code, bars, err}:
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

	batch, rows := map[string][]ds.StockBar{}, 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := r.store.SaveStockBars(context.WithoutCancel(ctx), r.stocks.Name(), batch)
		batch = map[string][]ds.StockBar{}
		return err
	}
	var failed []string
	began := r.now()
	for res := range results {
		switch {
		case res.err != nil:
			failed = append(failed, res.code)
			if len(failed) <= 5 {
				r.logf("✗ %s：%v", res.code, res.err)
			}
		case len(res.bars) > 0:
			batch[res.code] = res.bars
			rows += len(res.bars)
		}
		r.update(func(s *Status) { s.Done++ })
		if len(batch) >= stockBatch {
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
	msg := fmt.Sprintf("✓ 个股日线：写入 %d 行，耗时 %v", rows, r.now().Sub(began).Round(time.Second))
	if len(failed) > 0 {
		msg += fmt.Sprintf("；%d 只获取失败，下次重试", len(failed))
	}
	r.logf("%s", msg)
	if len(tasks) > 0 && len(failed)*20 > len(tasks) {
		return fmt.Errorf("%d 只股票获取失败（超过 5%%），请检查数据源后重试", len(failed))
	}
	return nil
}

// computeBreadth 只读本地个股日线，增量（或全量）更新广度状态与日计数，并重写 cn.breadth.ma200 序列。
func (r *Runner) computeBreadth(ctx context.Context, full bool) error {
	if full {
		if err := r.store.ResetBreadth(ctx); err != nil {
			return err
		}
	}
	local, err := r.store.StockList(ctx)
	if err != nil {
		return err
	}
	raw, err := r.store.BreadthStates(ctx)
	if err != nil {
		return err
	}
	type task struct {
		code string
		st   breadth.State
	}
	var tasks []task
	for _, s := range local {
		var st breadth.State
		if js, ok := raw[s.Code]; ok {
			if err := json.Unmarshal([]byte(js), &st); err != nil {
				return fmt.Errorf("%s 广度状态损坏，请全量重算：%w", s.Code, err)
			}
		}
		if s.LastDate != "" && st.LastDate < s.LastDate {
			tasks = append(tasks, task{s.Code, st})
		}
	}
	r.update(func(s *Status) { s.Total, s.Done, s.Current = len(tasks), 0, "breadth" })
	began := r.now()
	states, counts := map[string]string{}, breadth.Counts{}
	flush := func() error {
		if len(states) == 0 {
			return nil
		}
		err := r.store.SaveBreadth(context.WithoutCancel(ctx), states, counts)
		states, counts = map[string]string{}, breadth.Counts{}
		return err
	}
	for i, t := range tasks {
		if err := ctx.Err(); err != nil {
			_ = flush()
			return fmt.Errorf("已中断：%w", err)
		}
		bars, err := r.store.StockBars(ctx, t.code, t.st.LastDate)
		if err != nil {
			return err
		}
		st := breadth.Feed(t.st, bars, breadthMA, breadthCountFrom, counts)
		b, _ := json.Marshal(st)
		states[t.code] = string(b)
		if (i+1)%500 == 0 {
			if err := flush(); err != nil {
				return err
			}
		}
		r.update(func(s *Status) { s.Done++ })
	}
	if err := flush(); err != nil {
		return err
	}

	dates, all, err := r.store.BreadthCounts(ctx)
	if err != nil {
		return err
	}
	pts := breadth.Percent(dates, all)
	if _, err := r.store.UpsertPoints(ctx, ds.CNBreadthMA200, breadthSource, pts, r.now()); err != nil {
		return err
	}
	rangeText := "无数据"
	if n := len(pts); n > 0 {
		last := all[pts[n-1].Date]
		rangeText = fmt.Sprintf("%s ~ %s，最新 %.1f%%（%d / %d）", pts[0].Date, pts[n-1].Date, pts[n-1].Value, last.Above, last.Total)
	}
	r.logf("✓ 市场广度：更新 %d 只股票，共 %d 个交易日（%s），耗时 %v", len(tasks), len(pts), rangeText, r.now().Sub(began).Round(time.Second))
	return nil
}
