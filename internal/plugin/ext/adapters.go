package ext

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/notify"
	"market-thermometer/internal/plugin"
)

// 外部插件按清单类型适配成核心已有的接口，并登记到对应注册表。目前支持指标数据源与推送渠道；
// 个股 / 基金源、派生指标、策略、交易通道的外部适配按需再加（协议文档中已预留方法名）。

const maxPoints = 200_000 // 单次返回的数据点上限

var (
	mu    sync.Mutex
	procs = map[string]*Process{} // 插件 ID → 进程
)

// Install 读取清单并把外部插件登记到对应注册表。须在构建数据源 Hub 之前调用（清单中的新指标在此定义）。
func Install(spec Spec) (*Process, error) {
	p, err := Load(spec)
	if err != nil {
		return nil, err
	}
	m := p.manifest
	if _, dup := plugin.Get(m.Kind, m.Name); dup {
		return nil, fmt.Errorf("插件 %s 与已有插件重名", m.ID())
	}
	switch m.Kind {
	case plugin.KindMetric:
		if len(m.Metrics) == 0 {
			return nil, fmt.Errorf("指标数据源插件 %s 未声明 metrics", m.Name)
		}
		for _, d := range m.Metrics {
			if _, ok := ds.Lookup(ds.MetricID(d.ID)); !ok {
				ds.Define(ds.Metric{ID: ds.MetricID(d.ID), Market: d.Market, Name: orDefault(d.Name, d.ID), Unit: d.Unit,
					LagDays: d.LagDays, Note: orDefault(d.Note, "外部插件 "+m.Name+" 定义")})
			}
		}
		ds.Register(m.Name, func(env ds.Env) (ds.Source, error) { return &metricSource{p: p, now: env.Now}, nil })
	case plugin.KindNotifier:
		notify.Register(notify.Kind{Name: m.Name, Label: m.Label, Fields: m.Config, External: true,
			New: func(cfg map[string]string, _ notify.Env) (notify.Notifier, error) {
				return &notifier{p: p, cfg: cfg}, nil
			}})
	case plugin.KindBroker:
		// 交易通道：通过 ext.GetBroker(name) 调用，遵循 docs/plugin-protocol.md §5
	default:
		return nil, fmt.Errorf("暂不支持外部 %s 类型的插件（%s）", plugin.KindLabel[m.Kind], m.Name)
	}
	if err := plugin.Register(m); err != nil {
		return nil, err
	}
	mu.Lock()
	procs[m.ID()] = p
	mu.Unlock()
	return p, nil
}

// Get 返回外部插件进程；不存在时返回 nil。
func Get(id string) *Process {
	mu.Lock()
	defer mu.Unlock()
	return procs[id]
}

// CloseAll 结束全部外部插件进程（服务退出时调用）。
func CloseAll() {
	mu.Lock()
	defer mu.Unlock()
	for _, p := range procs {
		p.Close()
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ---- 指标数据源 ----

type metricSource struct {
	p   *Process
	now func() time.Time
}

func (s *metricSource) Name() string { return s.p.manifest.Name }

func (s *metricSource) Provides() []ds.MetricID {
	var out []ds.MetricID
	for _, d := range s.p.manifest.Metrics {
		out = append(out, ds.MetricID(d.ID))
	}
	return out
}

func (s *metricSource) Fetch(ctx context.Context, m ds.MetricID, from, to time.Time) ([]ds.Point, error) {
	var res struct {
		Points []ds.Point `json:"points"`
	}
	params := map[string]string{"metric": string(m), "from": from.Format("2006-01-02"), "to": to.Format("2006-01-02")}
	if err := s.p.Call(ctx, "metric.fetch", params, &res); err != nil {
		return nil, err
	}
	pts, err := CleanPoints(res.Points, params["from"], params["to"])
	if err != nil {
		return nil, fmt.Errorf("插件 %s 返回的数据不合法：%w", s.Name(), err)
	}
	if s.now != nil {
		pts = ds.ClipFinal(pts, s.now())
	}
	return pts, nil
}

// CleanPoints 校验并整理插件返回的数据点：日期须为 YYYY-MM-DD 且在 [from, to] 内，数值有限；
// 按日期排序，同一天重复时报错。
func CleanPoints(pts []ds.Point, from, to string) ([]ds.Point, error) {
	if len(pts) > maxPoints {
		return nil, fmt.Errorf("数据点过多（%d > %d）", len(pts), maxPoints)
	}
	out := make([]ds.Point, 0, len(pts))
	for _, p := range pts {
		if _, err := time.Parse("2006-01-02", p.Date); err != nil {
			return nil, fmt.Errorf("日期 %q 格式应为 YYYY-MM-DD", truncate(p.Date, 20))
		}
		if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			return nil, fmt.Errorf("%s 的数值不是有限数", p.Date)
		}
		if p.Date >= from && p.Date <= to {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	for i := 1; i < len(out); i++ {
		if out[i].Date == out[i-1].Date {
			return nil, fmt.Errorf("日期 %s 重复", out[i].Date)
		}
	}
	return out, nil
}

// ---- 推送渠道 ----

type notifier struct {
	p   *Process
	cfg map[string]string
}

func (n *notifier) Send(ctx context.Context, m notify.Message) error {
	params := map[string]any{"config": n.cfg, "title": m.Title, "lines": m.Lines, "markdown": m.Markdown(), "text": m.Text()}
	err := n.p.Call(ctx, "notify.send", params, nil)
	var re *rpcError
	if errors.As(err, &re) {
		return fmt.Errorf("%s", re.Message)
	}
	return err
}
