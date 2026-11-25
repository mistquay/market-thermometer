package datasource

import "context"

// NameSource 是能提供证券名称（代码 → 简称）的数据源能力，用于组合、行情等页面展示。
// 指标数据源可以额外实现它；核心通过类型断言发现。
//
// 名称列表通常要分页拉取，而免费接口有限流：每次调用最多取若干页，返回下一次的游标（cursor），
// 游标为空表示已取完。出错时同样返回已取到的部分与可继续的游标。
type NameSource interface {
	SecurityNames(ctx context.Context, cursor string) (names map[string]string, next string, err error)
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
