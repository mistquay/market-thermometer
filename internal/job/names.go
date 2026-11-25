package job

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SettingNamesAt：最近一次更新证券名称的时间（RFC3339）。
const SettingNamesAt = "names.updated_at"

// namesMaxAge：证券名称超过这么久就在每日同步时刷新一次（名称变化很少）。
const namesMaxAge = 7 * 24 * time.Hour

// refreshNames 从第一个能提供名称的数据源更新证券名称；force=false 时只在过期后更新。
func (r *Runner) refreshNames(ctx context.Context, force bool) error {
	if !force {
		at, err := r.store.Setting(ctx, SettingNamesAt)
		if err != nil {
			return err
		}
		if t, err := time.Parse(time.RFC3339, at); err == nil && r.now().Sub(t) < namesMaxAge {
			return nil
		}
	}
	var errs []error
	for _, ns := range r.hub.NameSources() {
		names, err := ns.SecurityNames(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := r.store.SaveNames(ctx, names, r.now()); err != nil {
			return err
		}
		r.logf("✓ 证券名称：%d 只", len(names))
		return r.store.SetSetting(ctx, SettingNamesAt, r.now().UTC().Format(time.RFC3339))
	}
	if len(errs) == 0 {
		return errors.New("没有能提供证券名称的数据源（需启用 eastmoney）")
	}
	return errors.Join(errs...)
}

// StartNames 在后台立即更新证券名称。
func (r *Runner) StartNames() (*Status, error) {
	id, err := r.begin("names")
	if err != nil {
		return nil, err
	}
	go func() {
		state, detail := "done", "证券名称已更新"
		if err := r.refreshNames(context.Background(), true); err != nil {
			state, detail = "failed", fmt.Sprintf("证券名称：%v", err)
			r.logf("✗ %v", err)
		}
		r.finish(id, state, detail)
	}()
	return r.Current(), nil
}
