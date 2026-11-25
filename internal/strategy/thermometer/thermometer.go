// Package thermometer 是内置策略插件“市场波段温度计”：估值层 V + 择时层 T 打分，状态机出信号（设计文档 §3–§4）。
package thermometer

import (
	"market-thermometer/internal/engine"
	"market-thermometer/internal/plugin"
	"market-thermometer/internal/strategy"
)

func init() {
	strategy.Register(Thermometer{}, plugin.Manifest{Label: "市场波段温度计",
		Description:  "估值层（ERP 分位）定背景，择时层（资金热度、杠杆情绪、市场广度、价格动量）出温度，状态机产生武装 / 底部 / 顶部信号；参数在回测页校准",
		Capabilities: []string{"score", "signals", "position"}})
}

// Thermometer 实现 strategy.Strategy。
type Thermometer struct{}

func (Thermometer) Name() string { return strategy.Default }

func (Thermometer) Run(in engine.Inputs, p engine.Params) strategy.Result {
	days := engine.Compute(in, p)
	states, events := engine.RunSignals(days, p.Signal)
	return strategy.Result{Days: days, States: states, Events: events}
}
