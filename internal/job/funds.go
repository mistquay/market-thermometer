package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	ds "market-thermometer/internal/datasource"
)

// ErrNoFundSource 表示未配置场外基金数据源。
var ErrNoFundSource = errors.New("未配置场外基金数据源（config.yaml 的 funds.sources）")

// fundRevisitDays：每次同步往回重拉的自然日数，覆盖净值修正。
const fundRevisitDays = 15

// SetFundNAVSources 配置场外基金净值数据源（按顺序回退）。
func (r *Runner) SetFundNAVSources(srcs []ds.FundSource) { r.fundNAV = srcs }

// HasFundNAV 表示是否配置了场外基金数据源。
func (r *Runner) HasFundNAV() bool { return len(r.fundNAV) > 0 }

// StartFunds 在后台同步基金清单与自选基金净值。
func (r *Runner) StartFunds() (*Status, error) {
	if len(r.fundNAV) == 0 {
		return nil, ErrNoFundSource
	}
	id, err := r.begin("funds")
	if err != nil {
		return nil, err
	}
	go func() {
		state, detail := "done", "基金净值同步完成"
		if err := r.syncFunds(context.Background()); err != nil {
			state, detail = "failed", err.Error()
			r.logf("✗ %v", err)
		}
		r.finish(id, state, detail)
	}()
	return r.Current(), nil
}

// syncFunds 刷新基金清单（本地为空或每次同步都刷新，单次请求），再增量同步自选基金净值。
func (r *Runner) syncFunds(ctx context.Context) error {
	var listErr error
	for _, src := range r.fundNAV {
		list, err := src.ListFunds(ctx)
		if err == nil {
			if err := r.store.ReplaceFundInfo(ctx, list, r.now()); err != nil {
				return err
			}
			r.logf("✓ 基金清单：来自 %s，%d 只", src.Name(), len(list))
			listErr = nil
			break
		}
		listErr = errors.Join(listErr, fmt.Errorf("%s: %w", src.Name(), err))
	}
	if listErr != nil {
		r.logf("✗ 基金清单：%v（沿用本地清单）", listErr)
	}

	watched, err := r.store.WatchedFunds(ctx)
	if err != nil {
		return err
	}
	if len(watched) == 0 {
		r.logf("没有自选基金，跳过净值同步（在“基金”页添加）")
		return nil
	}
	r.update(func(s *Status) { s.Total, s.Done, s.Current = len(watched), 0, "funds" })
	var failed int
	for _, f := range watched {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		from := ""
		if f.Last != "" {
			t, _ := time.Parse("2006-01-02", f.Last)
			from = t.AddDate(0, 0, -fundRevisitDays).Format("2006-01-02")
		}
		var errs []error
		ok := false
		for _, src := range r.fundNAV {
			navs, err := src.NAV(ctx, f.Code, from)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", src.Name(), err))
				continue
			}
			if err := r.store.SaveFundNAV(ctx, f.Code, src.Name(), navs); err != nil {
				return err
			}
			ok = true
			break
		}
		if !ok {
			failed++
			r.logf("✗ %s %s：%v", f.Code, f.Name, errors.Join(errs...))
		}
		r.update(func(s *Status) { s.Done++ })
	}
	r.logf("✓ 自选基金净值：%d 只，失败 %d 只", len(watched), failed)
	if failed == len(watched) {
		return fmt.Errorf("自选基金净值全部获取失败")
	}
	return nil
}
