package datasource

// StockExt 是个股日线的扩展字段（OHLC、成交、估值、市值），用于 K 线图与估值走势。
// 不复权价格；字段缺失时为 nil（如亏损股的 PE、来源不提供的字段）。
type StockExt struct {
	Date    string   `json:"date"`
	Open    float64  `json:"open"`
	High    float64  `json:"high"`
	Low     float64  `json:"low"`
	Volume  float64  `json:"volume"` // 股
	Amount  float64  `json:"amount"` // 元
	PETTM   *float64 `json:"peTtm"`
	PB      *float64 `json:"pb"`
	TotalMV *float64 `json:"totalMv"` // 元
	FloatMV *float64 `json:"floatMv"` // 元
}

// ExtCollector 是个股数据源的可选能力：拉取日线（Bars）时顺带记下扩展字段，由核心取走保存，
// 不为扩展字段额外发请求。
type ExtCollector interface {
	TakeExt() map[string][]StockExt
}
