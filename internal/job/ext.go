package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
)

// collectExt 保存个股数据源在拉取日线时顺带记下的扩展字段（OHLC、成交、估值），不额外请求。
func (r *Runner) collectExt(ctx context.Context) error {
	for _, s := range r.stocks {
		ec, ok := s.(ds.ExtCollector)
		if !ok {
			continue
		}
		if ext := ec.TakeExt(); len(ext) > 0 {
			if err := r.store.SaveStockExt(ctx, ext); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExtBackfillFrom 是扩展字段回填的默认起点：约 10 年，够 K 线与估值分位使用。
// 实测（2026-11-26）：5,474 只股票、1,161 万行，stockdb 回填 3 分钟，库体积增加约 1.2 GB。
const ExtBackfillFrom = "2015-01-01"

// StartExtBackfill 在后台从第一个提供扩展字段的个股数据源（stockdb）回填 A 股的扩展字段。
// 逐只按代码查询（不发全市场查询），已覆盖到 from 的股票跳过，可中断后续跑。
func (r *Runner) StartExtBackfill(from string) (*Status, error) {
	src, err := r.extSource()
	if err != nil {
		return nil, err
	}
	id, err := r.begin("ext-backfill")
	if err != nil {
		return nil, err
	}
	go func() {
		state, detail := "done", "扩展字段回填完成"
		if err := r.extBackfill(context.Background(), src, from); err != nil {
			state, detail = "failed", err.Error()
			r.logf("✗ %v", err)
		}
		r.finish(id, state, detail)
	}()
	return r.Current(), nil
}

// RunExtBackfill 同步执行（供 CLI 使用）。
func (r *Runner) RunExtBackfill(ctx context.Context, from string) (*Status, error) {
	src, err := r.extSource()
	if err != nil {
		return nil, err
	}
	id, err := r.begin("ext-backfill")
	if err != nil {
		return nil, err
	}
	state, detail := "done", "扩展字段回填完成"
	if err := r.extBackfill(ctx, src, from); err != nil {
		state, detail = "failed", err.Error()
		r.logf("✗ %v", err)
	}
	r.finish(id, state, detail)
	return r.Current(), nil
}

func (r *Runner) extSource() (ds.StockSource, error) {
	for _, s := range r.stocks {
		if _, ok := s.(ds.ExtCollector); ok {
			return s, nil
		}
	}
	return nil, errors.New("没有提供扩展字段（OHLC、估值）的个股数据源，需在 stocks.sources 中启用 stockdb")
}

func (r *Runner) extBackfill(ctx context.Context, src ds.StockSource, from string) error {
	ec := src.(ds.ExtCollector)
	_ = ec.TakeExt() // 丢弃之前残留的
	list, err := r.store.StockList(ctx)
	if err != nil {
		return err
	}
	done, err := r.store.ExtCoveredFrom(ctx, from)
	if err != nil {
		return err
	}
	var codes []string
	for _, s := range list {
		if s.Kind == ds.KindStock && s.LastDate != "" && s.LastDate >= from && !done[s.Code] {
			codes = append(codes, s.Code)
		}
	}
	r.logf("扩展字段回填：来源 %s，%s 起，需处理 %d 只股票（已覆盖的 %d 只跳过）", src.Name(), from, len(codes), len(done))
	r.update(func(s *Status) { s.Total, s.Done, s.Current = len(codes), 0, "ext" })
	began := r.now()

	jobs := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed []string
	for range r.stockConc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range jobs {
				if _, err := src.Bars(ctx, code, from, ""); err != nil {
					mu.Lock()
					failed = append(failed, code)
					if len(failed) <= 5 {
						r.logf("✗ %s：%v", code, err)
					}
					mu.Unlock()
				}
				r.update(func(s *Status) { s.Done++ })
			}
		}()
	}
	saveErr := error(nil)
	for i, code := range codes {
		if ctx.Err() != nil {
			break
		}
		jobs <- code
		if (i+1)%200 == 0 {
			if saveErr = r.store.SaveStockExt(context.WithoutCancel(ctx), ec.TakeExt()); saveErr != nil {
				break
			}
			r.logf("… 已处理 %d / %d 只（%v）", i+1, len(codes), r.now().Sub(began).Round(time.Second))
		}
	}
	close(jobs)
	wg.Wait()
	if err := r.store.SaveStockExt(context.WithoutCancel(ctx), ec.TakeExt()); saveErr == nil {
		saveErr = err
	}
	r.collectNames(context.WithoutCancel(ctx))
	if saveErr != nil {
		return saveErr
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("已中断（已保存的部分保留，下次跳过）：%w", err)
	}
	r.logf("✓ 扩展字段回填：%d 只，耗时 %v；失败 %d 只（下次重试）", len(codes)-len(failed), r.now().Sub(began).Round(time.Second), len(failed))
	return nil
}
