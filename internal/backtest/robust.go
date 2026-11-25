package backtest

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"market-thermometer/internal/engine"
)

// 稳健性检验（设计文档 §8）：只展示参数扰动与分段表现，不挑选参数。

// PerturbRatio 是单参数扰动的幅度（±20%）。
const PerturbRatio = 0.2

// field 是一个可扰动的参数。upper=true 表示“上沿”阈值（如顶部武装线），按距 100 的距离扰动，
// 避免 85×1.2 超出 0–100；integer=true 时取整，取整后不变则改为 ±1。
type field struct {
	name    string
	get     func(*engine.Params) *float64
	geti    func(*engine.Params) *int
	upper   bool
	integer bool
}

// group 是一组同时扰动的参数（如三个估值档的底部武装线）。
type group struct {
	key, label string
	fields     []field
}

func f(name string, get func(*engine.Params) *float64) field { return field{name: name, get: get} }
func fu(name string, get func(*engine.Params) *float64) field {
	return field{name: name, get: get, upper: true}
}
func fi(name string, get func(*engine.Params) *int) field {
	return field{name: name, geti: get, integer: true}
}

var perturbGroups = []group{
	{"armLow", "底部武装线（三档）", []field{
		f("便宜", func(p *engine.Params) *float64 { return &p.Signal.ArmLowCheap }),
		f("合理", func(p *engine.Params) *float64 { return &p.Signal.ArmLowFair }),
		f("昂贵", func(p *engine.Params) *float64 { return &p.Signal.ArmLowExpensive }),
	}},
	{"armHigh", "顶部武装线（三档，按距 100 扰动）", []field{
		fu("便宜", func(p *engine.Params) *float64 { return &p.Signal.ArmHighCheap }),
		fu("合理", func(p *engine.Params) *float64 { return &p.Signal.ArmHighFair }),
		fu("昂贵", func(p *engine.Params) *float64 { return &p.Signal.ArmHighExpensive }),
	}},
	{"minExtremeDims", "武装所需极端维度数", []field{fi("", func(p *engine.Params) *int { return &p.Signal.MinExtremeDims })}},
	{"rebound", "右侧回暖幅度", []field{f("", func(p *engine.Params) *float64 { return &p.Signal.Rebound })}},
	{"riseDays", "连续上升天数", []field{fi("", func(p *engine.Params) *int { return &p.Signal.RiseDays })}},
	{"pullback", "顶部回落幅度", []field{f("", func(p *engine.Params) *float64 { return &p.Signal.Pullback })}},
	{"rsiExit", "顶部 RSI 上限", []field{f("", func(p *engine.Params) *float64 { return &p.Signal.RSIExit })}},
	{"divergenceGap", "背离幅度", []field{f("", func(p *engine.Params) *float64 { return &p.Signal.DivergenceGap })}},
	{"armTimeout", "武装超时（交易日）", []field{fi("", func(p *engine.Params) *int { return &p.Signal.ArmTimeout })}},
	{"disarmMid", "武装失效线", []field{f("", func(p *engine.Params) *float64 { return &p.Signal.DisarmMid })}},
	{"cooldown", "冷却复位线（底 / 顶，顶按距 100 扰动）", []field{
		f("底", func(p *engine.Params) *float64 { return &p.Signal.CooldownLowEx }),
		fu("顶", func(p *engine.Params) *float64 { return &p.Signal.CooldownHighEx }),
	}},
	{"tier", "估值档界线（便宜 / 昂贵，昂贵按距 100 扰动）", []field{
		f("便宜", func(p *engine.Params) *float64 { return &p.CheapBelow }),
		fu("昂贵", func(p *engine.Params) *float64 { return &p.ExpensiveAbove }),
	}},
	{"extreme", "极端区阈值", []field{f("", func(p *engine.Params) *float64 { return &p.Extreme })}},
	{"emaSpan", "T_s 平滑天数", []field{fi("", func(p *engine.Params) *int { return &p.EMASpan })}},
}

// Variant 是一组扰动后的参数。
type Variant struct {
	Group  string        `json:"group"`
	Label  string        `json:"label"`
	Level  int           `json:"level"`  // -1 / +1：减小 / 增大（上沿阈值的“减小”指离 100 更近）
	Values string        `json:"values"` // 实际取值，如 "便宜 24 · 合理 16 · 昂贵 12"
	Skip   string        `json:"skip,omitempty"`
	Params engine.Params `json:"-"`
}

// Perturb 生成每组参数 −20% / +20% 的变体（其余参数保持 base 不变）。不合法的变体标记 Skip。
func Perturb(base engine.Params) []Variant {
	var out []Variant
	for _, g := range perturbGroups {
		for _, level := range []int{-1, +1} {
			p := base
			var vals []string
			changed := false
			for _, fd := range g.fields {
				var s string
				if fd.integer {
					ptr := fd.geti(&p)
					old := *ptr
					v := int(math.Round(float64(old) * (1 + float64(level)*PerturbRatio)))
					if v == old {
						v = old + level
					}
					*ptr = v
					changed = changed || v != old
					s = strconv.Itoa(v)
				} else {
					ptr := fd.get(&p)
					old := *ptr
					v := old * (1 + float64(level)*PerturbRatio)
					if fd.upper {
						v = 100 - (100-old)*(1+float64(level)*PerturbRatio)
					}
					*ptr = math.Round(v*10) / 10
					changed = changed || *ptr != old
					s = strconv.FormatFloat(*ptr, 'f', -1, 64)
				}
				if fd.name != "" {
					s = fd.name + " " + s
				}
				vals = append(vals, s)
			}
			v := Variant{Group: g.key, Label: g.label, Level: level, Values: strings.Join(vals, " · "), Params: p}
			if err := p.Validate(); err != nil {
				v.Skip = "参数不合法：" + err.Error()
			} else if !changed {
				v.Skip = "取值未变化"
			}
			out = append(out, v)
		}
	}
	return out
}

// Outcome 是一次回测中用于比较的摘要。
type Outcome struct {
	BuySignals  int     `json:"buySignals"`  // 计入持仓的底部信号（不含观察级）
	SellSignals int     `json:"sellSignals"` // 顶部信号
	Strategies  []Brief `json:"strategies"`
	Few         bool    `json:"few"` // 信号太少（< 5），统计意义弱
	Error       string  `json:"error,omitempty"`
}

// Brief 是一个策略的收益与回撤。
type Brief struct {
	Name     string  `json:"name"`
	TotalRet float64 `json:"totalRet"`
	CAGR     float64 `json:"cagr"`
	MaxDD    float64 `json:"maxDD"`
}

// Summarize 从回测结果中取出比较用的摘要。
func Summarize(r Result) Outcome {
	var o Outcome
	for _, s := range r.Signals {
		if s.Source != "thermometer" {
			continue
		}
		switch {
		case s.Side > 0 && !s.Observe:
			o.BuySignals++
		case s.Side < 0:
			o.SellSignals++
		}
	}
	for _, s := range r.Strategies {
		o.Strategies = append(o.Strategies, Brief{Name: s.Name, TotalRet: s.TotalRet, CAGR: s.CAGR, MaxDD: s.MaxDD})
	}
	o.Few = o.BuySignals+o.SellSignals < 5
	return o
}

// Window 是一个统计区间的结果（参数固定，不在区间内重新校准）。
type Window struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Label   string  `json:"label"`
	Outcome Outcome `json:"outcome"`
}

// Years 按自然年切分统计区间（首尾年可能不完整）。持仓状态由区间之前的事件延续。
func Years(days []engine.Day, events []engine.Event, cfg Config) []Window {
	if len(days) == 0 {
		return nil
	}
	first, last := max(cfg.From, days[0].Date), days[len(days)-1].Date
	if cfg.To != "" && cfg.To < last {
		last = cfg.To
	}
	var out []Window
	for y, _ := strconv.Atoi(first[:4]); fmt.Sprint(y) <= last[:4]; y++ {
		c := cfg
		c.From, c.To = max(first, fmt.Sprintf("%d-01-01", y)), min(last, fmt.Sprintf("%d-12-31", y))
		r := Run(days, events, c)
		if r.Days < 2 {
			continue
		}
		label := strconv.Itoa(y)
		if r.From > fmt.Sprintf("%d-01-10", y) || r.To < fmt.Sprintf("%d-12-20", y) {
			label += "（不完整）"
		}
		out = append(out, Window{From: r.From, To: r.To, Label: label, Outcome: Summarize(r)})
	}
	return out
}
