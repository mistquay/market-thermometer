package job

import (
	"context"
	"fmt"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/market"
	"market-thermometer/internal/store"
	"market-thermometer/internal/strategy"
)

// ScoreResult 是一次重算的摘要。
type ScoreResult struct {
	Version string
	Days    int
	Last    engine.Day
	Sources map[market.Role]string
	Events  int
	State   engine.State
}

// LoadDays 读取该市场各角色的原始数据，按参数 p 计算全部交易日得分（只读，不写库）。
func LoadDays(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, p engine.Params) ([]engine.Day, map[market.Role]string, error) {
	res, sources, err := Run(ctx, st, hub, mkt, p)
	return res.Days, sources, err
}

// Run 读取该市场各角色的原始数据，用当前策略插件计算得分、状态与事件（只读，不写库）。
func Run(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, p engine.Params) (strategy.Result, map[market.Role]string, error) {
	strat, err := strategy.Get(strategy.Default)
	if err != nil {
		return strategy.Result{}, nil, err
	}
	cov, err := st.Coverage(ctx)
	if err != nil {
		return strategy.Result{}, nil, err
	}
	have := map[ds.MetricID]map[string]bool{}
	for _, c := range cov {
		if have[c.Metric] == nil {
			have[c.Metric] = map[string]bool{}
		}
		have[c.Metric][c.Source] = true
	}
	sources := map[market.Role]string{}
	raw := map[market.Role][]ds.Point{}
	for role, metric := range mkt.Roles {
		src := pickSource(hub.Route(metric), have[metric])
		if src == "" {
			if role == market.RoleClose {
				return strategy.Result{}, nil, fmt.Errorf("缺少标的收盘价 %s，无法建立交易日历", metric)
			}
			continue // 缺维度由引擎按降级处理
		}
		pts, err := st.Series(ctx, metric, src, "", "")
		if err != nil {
			return strategy.Result{}, nil, err
		}
		raw[role] = pts
		sources[role] = src
	}
	if _, ok := raw[market.RoleBreadth]; !ok {
		p.Weights[2] = 0 // 完全没有广度数据（未配置个股数据源）：视为不启用，而不是每天都降级
	}
	return strat.Run(engine.Align(raw, p.MaxFillDays), p), sources, nil
}

// Score 按生效参数重算该市场全部得分、状态机与事件并写库（全量重算，幂等）。
func Score(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, now time.Time) (*ScoreResult, error) {
	p, err := st.ActiveParams(ctx, now)
	if err != nil {
		return nil, err
	}
	out, sources, err := Run(ctx, st, hub, mkt, p)
	if err != nil {
		return nil, err
	}
	days, states, events := out.Days, out.States, out.Events
	res := &ScoreResult{Version: p.Version(), Sources: sources}
	if err := st.ReplaceScores(ctx, mkt.ID, res.Version, days); err != nil {
		return nil, err
	}
	phases := make([]string, len(states))
	dates := make([]string, len(days))
	for i := range states {
		phases[i], dates[i] = string(states[i].Phase), days[i].Date
	}
	if err := st.UpdateStates(ctx, mkt.ID, res.Version, dates, phases); err != nil {
		return nil, err
	}
	if err := st.ReplaceEvents(ctx, mkt.ID, res.Version, events); err != nil {
		return nil, err
	}
	if err := st.PruneDerived(ctx, mkt.ID, res.Version); err != nil {
		return nil, err
	}
	res.Days, res.Events = len(days), len(events)
	if n := len(days); n > 0 {
		res.Last, res.State = days[n-1], states[n-1]
	}
	return res, nil
}

// pickSource：按路由优先级取第一个库里有数据的来源；路由里都没有时退而取任一有数据的来源。
func pickSource(route []string, have map[string]bool) string {
	for _, s := range route {
		if have[s] {
			return s
		}
	}
	for s := range have {
		return s
	}
	return ""
}
