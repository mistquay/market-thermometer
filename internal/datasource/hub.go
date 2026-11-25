package datasource

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Hub 持有已启用的插件实例，并按路由为每个指标选择数据源。
type Hub struct {
	sources map[string]Source
	order   []string
	routes  map[MetricID][]string
}

// NewHub 构造已启用的插件。enabled 为空时启用全部已注册插件。
// routes 为空的指标，默认路由为"所有声明提供它的插件，按启用顺序"。
func NewHub(env Env, enabled []string, routes map[string][]string, options map[string]map[string]any) (*Hub, error) {
	if len(enabled) == 0 {
		enabled = Registered()
	}
	h := &Hub{sources: map[string]Source{}, routes: map[MetricID][]string{}}
	for _, name := range enabled {
		e := env
		e.Options = options[name]
		src, err := build(name, e)
		if err != nil {
			return nil, err
		}
		h.sources[name] = src
		h.order = append(h.order, name)
	}
	for m, names := range routes {
		for _, n := range names {
			if _, ok := h.sources[n]; !ok {
				return nil, fmt.Errorf("datasource: route %s references disabled or unknown plugin %q", m, n)
			}
		}
		h.routes[MetricID(m)] = names
	}
	return h, nil
}

// Sources 返回已启用的插件名。
func (h *Hub) Sources() []string { return append([]string(nil), h.order...) }

// Route 返回指标的数据源优先级列表。
func (h *Hub) Route(m MetricID) []string {
	if r, ok := h.routes[m]; ok {
		return r
	}
	var out []string
	for _, name := range h.order {
		if slices.Contains(h.sources[name].Provides(), m) {
			out = append(out, name)
		}
	}
	return out
}

// Fetch 依路由顺序尝试各数据源，返回第一个成功的结果及其来源名。
func (h *Hub) Fetch(ctx context.Context, m MetricID, from, to time.Time) (string, []Point, error) {
	route := h.Route(m)
	if len(route) == 0 {
		return "", nil, fmt.Errorf("no source provides %s", m)
	}
	var errs []error
	for _, name := range route {
		pts, err := h.sources[name].Fetch(ctx, m, from, to)
		if err == nil {
			return name, pts, nil
		}
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}
	return "", nil, errors.Join(errs...)
}
