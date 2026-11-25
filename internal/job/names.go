package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	ds "market-thermometer/internal/datasource"
)

const (
	// SettingNamesAt：最近一次完整取到证券名称的时间（RFC3339）。
	SettingNamesAt = "names.updated_at"
	// SettingNamesCursor：名称列表分批拉取的游标（未取完时下次从这里继续）。
	SettingNamesCursor = "names.cursor"
	// namesMaxAge：证券名称完整取到后，过这么久才再刷新（名称变化很少）。
	namesMaxAge = 7 * 24 * time.Hour
)

// refreshNames 分批更新证券名称：每次调用取一批（受数据源限流约束），保存已取到的部分与游标，
// 全部取完后记录时间。force=true 时忽略“最近已更新”的判断。
func (r *Runner) refreshNames(ctx context.Context, force bool) error {
	cursor, err := r.store.Setting(ctx, SettingNamesCursor)
	if err != nil {
		return err
	}
	if !force && cursor == "" {
		at, err := r.store.Setting(ctx, SettingNamesAt)
		if err != nil {
			return err
		}
		if t, err := time.Parse(time.RFC3339, at); err == nil && r.now().Sub(t) < namesMaxAge {
			return nil
		}
	}
	sources := r.hub.NameSources()
	if len(sources) == 0 {
		return errors.New("没有能提供证券名称的数据源（需启用 eastmoney）")
	}
	names, next, ferr := sources[0].SecurityNames(ctx, cursor)
	if len(names) > 0 {
		if err := r.store.SaveNames(ctx, names, r.now()); err != nil {
			return err
		}
	}
	if err := r.store.SetSetting(ctx, SettingNamesCursor, next); err != nil {
		return err
	}
	switch {
	case ferr != nil:
		return fmt.Errorf("本次取到 %d 条，下次从 %s 继续：%w", len(names), next, ferr)
	case next != "":
		r.logf("证券名称：本次取到 %d 条，未取完，下次同步从 %s 继续", len(names), next)
		return nil
	}
	r.logf("✓ 证券名称已全部更新（本次 %d 条）", len(names))
	return r.store.SetSetting(ctx, SettingNamesAt, r.now().UTC().Format(time.RFC3339))
}

// StartNames 在后台立即取一批证券名称。
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

// collectNames 保存个股数据源在拉取日线时顺带记下的证券名称（如 stockdb 日 K 每行带名称，离线、无需额外请求）。
func (r *Runner) collectNames(ctx context.Context) {
	for _, s := range append(append([]ds.StockSource{}, r.stocks...), r.funds...) {
		nc, ok := s.(ds.NameCollector)
		if !ok {
			continue
		}
		if names := nc.TakeNames(); len(names) > 0 {
			if err := r.store.SaveNames(ctx, names, r.now()); err != nil {
				r.logf("✗ 保存证券名称：%v", err)
				continue
			}
			r.logf("✓ 证券名称：从 %s 更新 %d 只", s.Name(), len(names))
		}
	}
}
