// Package strategy 是策略插件：输入按市场角色对齐的指标序列与参数，输出逐日得分、状态机状态与信号事件。
// 策略必须是纯函数（第 t 天只用第 0..t 天的数据），回测与实盘共用同一份实现。
package strategy

import (
	"fmt"
	"sort"
	"sync"

	"market-thermometer/internal/engine"
	"market-thermometer/internal/plugin"
)

// Result 是一次策略运行的输出。
type Result struct {
	Days   []engine.Day
	States []engine.State
	Events []engine.Event
}

// Strategy 是一个策略。
type Strategy interface {
	Name() string
	Run(in engine.Inputs, p engine.Params) Result
}

// Default 是当前生效的策略名（多策略并存前先固定为温度计）。
const Default = "thermometer"

var (
	mu  sync.RWMutex
	reg = map[string]Strategy{}
)

// Register 由内置策略在 init() 中调用，同时登记插件清单。
func Register(s Strategy, m plugin.Manifest) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := reg[s.Name()]; dup {
		panic("strategy: duplicate " + s.Name())
	}
	reg[s.Name()] = s
	m.Name, m.Kind = s.Name(), plugin.KindStrategy
	plugin.MustRegister(m)
}

// Get 按名称取策略。
func Get(name string) (Strategy, error) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := reg[name]
	if !ok {
		return nil, fmt.Errorf("策略 %q 未注册", name)
	}
	return s, nil
}

// Names 返回已注册的策略名。
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	var out []string
	for n := range reg {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
