// Package ma200 是内置派生指标插件“市场广度”：全 A 股（含退市）后复权收盘价站上自身 200 日均线的占比。
// 只读本地个股日线（增量状态 + 可加计数），写回 cn.breadth.ma200。
package ma200

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"market-thermometer/internal/breadth"
	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/derived"
	"market-thermometer/internal/plugin"
)

const (
	Name      = "breadth_ma200"
	MA        = 200          // 均线周期
	CountFrom = "2010-01-01" // 广度序列起点
	Source    = "local"      // 写入 raw_metric 的来源名：由本地个股日线派生
)

func init() {
	derived.Register(Plugin{}, plugin.Manifest{Label: "市场广度（站上 200 日均线占比）",
		Description: "全 A 股（含退市股）后复权收盘价站上自身 200 日均线的比例；只读本地个股日线，个股同步后全量重算（约 20 秒）",
		Metrics:     []plugin.MetricDef{{ID: string(ds.CNBreadthMA200), Name: "全A站上200日均线占比", Unit: "%"}}})
}

// Plugin 实现 derived.Derived。
type Plugin struct{}

func (Plugin) Name() string        { return Name }
func (Plugin) Metric() ds.MetricID { return ds.CNBreadthMA200 }

// Compute 增量（或全量）更新各股广度状态与日计数，并重写 cn.breadth.ma200 序列。
func (Plugin) Compute(ctx context.Context, env derived.Env, full bool) error {
	progress := env.Progress
	if progress == nil {
		progress = func(int, int) {}
	}
	if full {
		if err := env.Store.ResetBreadth(ctx); err != nil {
			return err
		}
	}
	local, err := env.Store.StockList(ctx)
	if err != nil {
		return err
	}
	raw, err := env.Store.BreadthStates(ctx)
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
		if s.Kind == ds.KindStock && s.LastDate != "" && st.LastDate < s.LastDate { // 广度只统计 A 股
			tasks = append(tasks, task{s.Code, st})
		}
	}
	progress(0, len(tasks))
	began := env.Now()
	states, counts := map[string]string{}, breadth.Counts{}
	flush := func() error {
		if len(states) == 0 {
			return nil
		}
		err := env.Store.SaveBreadth(context.WithoutCancel(ctx), states, counts)
		states, counts = map[string]string{}, breadth.Counts{}
		return err
	}
	for i, t := range tasks {
		if err := ctx.Err(); err != nil {
			_ = flush()
			return fmt.Errorf("已中断：%w", err)
		}
		bars, err := env.Store.StockBars(ctx, t.code, t.st.LastDate)
		if err != nil {
			return err
		}
		st := breadth.Feed(t.st, bars, MA, CountFrom, counts)
		b, _ := json.Marshal(st)
		states[t.code] = string(b)
		if (i+1)%500 == 0 {
			if err := flush(); err != nil {
				return err
			}
		}
		progress(i+1, len(tasks))
	}
	if err := flush(); err != nil {
		return err
	}

	dates, all, err := env.Store.BreadthCounts(ctx)
	if err != nil {
		return err
	}
	pts := breadth.Percent(dates, all)
	if _, err := env.Store.UpsertPoints(ctx, ds.CNBreadthMA200, Source, pts, env.Now()); err != nil {
		return err
	}
	rangeText := "无数据"
	if n := len(pts); n > 0 {
		last := all[pts[n-1].Date]
		rangeText = fmt.Sprintf("%s ~ %s，最新 %.1f%%（%d / %d）", pts[0].Date, pts[n-1].Date, pts[n-1].Value, last.Above, last.Total)
	}
	env.Logf("✓ 市场广度：更新 %d 只股票，共 %d 个交易日（%s），耗时 %v", len(tasks), len(pts), rangeText, env.Now().Sub(began).Round(time.Second))
	return nil
}
