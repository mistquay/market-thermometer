package datasource

import "context"

// NameSource 是能提供证券名称（代码 → 简称）的数据源能力，用于组合、行情等页面展示。
// 指标数据源可以额外实现它；核心通过类型断言发现。
type NameSource interface {
	SecurityNames(ctx context.Context) (map[string]string, error)
}

// NameSources 返回已启用数据源中实现了 NameSource 的。
func (h *Hub) NameSources() []NameSource {
	var out []NameSource
	for _, name := range h.order {
		if ns, ok := h.sources[name].(NameSource); ok {
			out = append(out, ns)
		}
	}
	return out
}
