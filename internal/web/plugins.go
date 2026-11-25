package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/plugin"
	"market-thermometer/internal/plugin/ext"
	"market-thermometer/internal/store"
)

// 插件管理：内置插件只读（配置在 config.yaml）；外部插件可在这里启停、修改清单声明的配置字段。
// 任何接口都不接受可执行路径或命令。

type pluginOut struct {
	plugin.Manifest
	ID        string            `json:"id"`
	KindLabel string            `json:"kindLabel"`
	Enabled   bool              `json:"enabled"`
	State     string            `json:"state"` // 状态说明
	Editable  bool              `json:"editable"`
	Values    map[string]string `json:"values"` // 当前配置（敏感字段打码）
	Process   *ext.Status       `json:"process,omitempty"`
	Testable  bool              `json:"testable"`
}

func (s *Server) plugins(w http.ResponseWriter, r *http.Request) {
	stocks, funds, fundNAV := s.runner.SourceNames()
	chs, err := s.store.NotifyChannels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []pluginOut{}
	for _, m := range plugin.All() {
		o := pluginOut{Manifest: m, ID: m.ID(), KindLabel: plugin.KindLabel[m.Kind], Values: map[string]string{}}
		if p := ext.Get(m.ID()); p != nil {
			st := p.Status()
			o.Process, o.Editable, o.Enabled = &st, true, !st.Disabled
			o.Values = plugin.Mask(m.Config, p.Config())
			switch {
			case st.Disabled:
				o.State = "已停用"
			case st.Running:
				o.State = fmt.Sprintf("运行中（PID %d）", st.PID)
			default:
				o.State = "空闲（首次调用时启动）"
			}
		} else {
			for k, v := range s.opt.PluginOptions[m.ID()] {
				o.Values[k] = fmt.Sprint(v)
			}
			o.Values = plugin.Mask(m.Config, o.Values)
			o.Enabled, o.State = builtinState(m, s.hub, stocks, funds, fundNAV, chs)
		}
		if m.Kind == plugin.KindMetric && len(m.Metrics) == 0 {
			for _, id := range s.hub.Provides(m.Name) {
				md, _ := ds.Lookup(id)
				o.Metrics = append(o.Metrics, plugin.MetricDef{ID: string(id), Name: md.Name, Unit: md.Unit})
			}
		}
		o.Testable = o.Enabled && (m.Kind == plugin.KindMetric || m.Kind == plugin.KindStock)
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, out)
}

func builtinState(m plugin.Manifest, hub *ds.Hub, stocks, funds, fundNAV []string, chs []store.NotifyChannel) (bool, string) {
	switch m.Kind {
	case plugin.KindMetric:
		if slices.Contains(hub.Sources(), m.Name) {
			return true, "已启用"
		}
		return false, "未启用（config.yaml 的 sources.enabled）"
	case plugin.KindStock:
		var uses []string
		if slices.Contains(stocks, m.Name) {
			uses = append(uses, "个股")
		}
		if slices.Contains(funds, m.Name) {
			uses = append(uses, "场内基金")
		}
		if len(uses) == 0 {
			return false, "未启用（config.yaml 的 stocks.sources）"
		}
		return true, fmt.Sprintf("已启用：%v", uses)
	case plugin.KindFund:
		if slices.Contains(fundNAV, m.Name) {
			return true, "已启用"
		}
		return false, "未启用（config.yaml 的 funds.sources）"
	case plugin.KindNotifier:
		n := 0
		for _, c := range chs {
			if c.Kind == m.Name {
				n++
			}
		}
		return true, fmt.Sprintf("可用，已配置 %d 个渠道（在设置页管理）", n)
	}
	return true, "内置"
}

// savePlugin 保存外部插件的启停与配置（只接受清单声明的字段；敏感字段提交打码值或空值时保持原值）。
func (s *Server) savePlugin(w http.ResponseWriter, r *http.Request) {
	m, p, ok := s.externalPlugin(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled bool              `json:"enabled"`
		Config  map[string]string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	cfg := plugin.Merge(m.Config, p.Config(), req.Config)
	if err := plugin.ValidateConfig(m.Config, cfg); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SavePluginConfig(r.Context(), m.ID(), store.PluginConfig{Enabled: req.Enabled, Config: cfg}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p.SetConfig(cfg)
	p.SetEnabled(req.Enabled)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) externalPlugin(w http.ResponseWriter, r *http.Request) (plugin.Manifest, *ext.Process, bool) {
	m, ok := plugin.Get(plugin.Kind(r.PathValue("kind")), r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusNotFound, "插件不存在")
		return m, nil, false
	}
	p := ext.Get(m.ID())
	if p == nil {
		writeErr(w, http.StatusBadRequest, "内置插件的配置在 config.yaml 中修改")
		return m, nil, false
	}
	return m, p, true
}

// testPlugin 对指标源取第一个指标最近 30 天的数据，对个股源取 600000 最近 30 天的日线（会请求数据源）。
func (s *Server) testPlugin(w http.ResponseWriter, r *http.Request) {
	m, ok := plugin.Get(plugin.Kind(r.PathValue("kind")), r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusNotFound, "插件不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	to := s.now()
	from := to.AddDate(0, 0, -30)
	began := time.Now()
	res := map[string]any{}
	var err error
	switch m.Kind {
	case plugin.KindMetric:
		metrics := s.hub.Provides(m.Name)
		if len(metrics) == 0 {
			err = fmt.Errorf("插件 %s 未启用", m.Name)
			break
		}
		var pts []ds.Point
		pts, err = s.hub.FetchFrom(ctx, m.Name, metrics[0], from, to)
		res["target"], res["count"] = string(metrics[0]), len(pts)
		if n := len(pts); n > 0 {
			res["last"] = pts[n-1]
		}
	case plugin.KindStock:
		var bars []ds.StockBar
		bars, err = s.runner.TestStockSource(ctx, m.Name, "600000", from.Format("2006-01-02"))
		res["target"], res["count"] = "600000", len(bars)
		if n := len(bars); n > 0 {
			res["last"] = bars[n-1]
		}
	default:
		err = fmt.Errorf("%s 不支持在这里测试", plugin.KindLabel[m.Kind])
	}
	res["elapsedMs"] = time.Since(began).Milliseconds()
	if err != nil {
		res["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, res)
}
