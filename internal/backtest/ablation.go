package backtest

import "market-thermometer/internal/engine"

// AblationCase 定义一组用于定位失效因子的维度 / 规则消融试验。
type AblationCase struct {
	Key    string        `json:"key"`
	Label  string        `json:"label"`
	Desc   string        `json:"desc"`
	Params engine.Params `json:"params"`
}

// AblationCases 以 base 为基线构造单维度、留一法与结构开关消融组，用于量化各维度与规则开关的真实贡献。
func AblationCases(base engine.Params) []AblationCase {
	mk := func(key, label, desc string, fn func(*engine.Params)) AblationCase {
		p := base
		fn(&p)
		return AblationCase{Key: key, Label: label, Desc: desc, Params: p}
	}
	return []AblationCase{
		mk("base", "当前参数", "当前左侧面板配置的基线", func(*engine.Params) {}),
		mk("only_t1", "仅资金热度 (T1)", "仅保留成交额换手率维度", func(p *engine.Params) {
			p.Weights = [4]float64{1, 0, 0, 0}
			p.Signal.MinExtremeDims = 1
		}),
		mk("only_t2", "仅杠杆情绪 (T2)", "仅保留融资买入占比维度", func(p *engine.Params) {
			p.Weights = [4]float64{0, 1, 0, 0}
			p.Signal.MinExtremeDims = 1
		}),
		mk("only_t3", "仅市场广度 (T3)", "仅保留全A站上MA200占比", func(p *engine.Params) {
			p.Weights = [4]float64{0, 0, 1, 0}
			p.Signal.MinExtremeDims = 1
		}),
		mk("only_t4", "仅价格动量 (T4)", "仅保留日/周 RSI 动量维度", func(p *engine.Params) {
			p.Weights = [4]float64{0, 0, 0, 1}
			p.Signal.MinExtremeDims = 1
		}),
		mk("drop_t1", "去除资金热度 (T2+T3+T4)", "留一法：剔除资金热度", func(p *engine.Params) {
			p.Weights[0] = 0
			if p.Weights == [4]float64{} {
				p.Weights = [4]float64{0, 0.3, 0.4, 0.3}
			}
		}),
		mk("drop_t2", "去除杠杆情绪 (T1+T3+T4)", "留一法：剔除杠杆情绪", func(p *engine.Params) {
			p.Weights[1] = 0
			if p.Weights == [4]float64{} {
				p.Weights = [4]float64{0.3, 0, 0.4, 0.3}
			}
		}),
		mk("drop_t3", "去除市场广度 (T1+T2+T4)", "留一法：剔除市场广度", func(p *engine.Params) {
			p.Weights[2] = 0
			if p.Weights == [4]float64{} {
				p.Weights = [4]float64{0.35, 0.35, 0, 0.3}
			}
		}),
		mk("skip_tpct", "去掉二次分位（方案 C）", "温度直接用加权得分平滑，避免趋势市被二次拉伸", func(p *engine.Params) {
			p.SkipTPct = true
		}),
		mk("top_exp_c", "方案 C + 仅估值昂贵时卖顶", "关闭二次分位且估值便宜/合理时不发顶部卖出信号", func(p *engine.Params) {
			p.SkipTPct = true
			p.Signal.TopOnlyExpensive = true
		}),
	}
}

// AblationPeriod 记录某一段区间（全区间 / 样本内 / 样本外）的摘要。
type AblationPeriod struct {
	ThermoRet  float64 `json:"thermoRet"`
	ThermoDD   float64 `json:"thermoDD"`
	LayeredRet float64 `json:"layeredRet"`
	LayeredDD  float64 `json:"layeredDD"`
	ValRet     float64 `json:"valRet"`
	HoldRet    float64 `json:"holdRet"`
	Buys       int     `json:"buys"`
	Sells      int     `json:"sells"`
}

// AblationRow 是一组消融试验的全区间、样本内、样本外对照结果。
type AblationRow struct {
	Key    string         `json:"key"`
	Label  string         `json:"label"`
	Desc   string         `json:"desc"`
	Params engine.Params  `json:"params"`
	All    AblationPeriod `json:"all"`
	In     AblationPeriod `json:"in"`
	Out    AblationPeriod `json:"out"`
}

// SummarizeAblation 从给定区间回测结果提取核心对照数字。
func SummarizeAblation(r Result) AblationPeriod {
	o := Summarize(r)
	p := AblationPeriod{Buys: o.BuySignals, Sells: o.SellSignals}
	for _, s := range o.Strategies {
		switch s.Name {
		case "thermometer":
			p.ThermoRet, p.ThermoDD = s.TotalRet, s.MaxDD
		case "layered":
			p.LayeredRet, p.LayeredDD = s.TotalRet, s.MaxDD
		case "valuation":
			p.ValRet = s.TotalRet
		case "hold":
			p.HoldRet = s.TotalRet
		}
	}
	return p
}
