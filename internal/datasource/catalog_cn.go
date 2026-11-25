package datasource

// A股标准指标。插件声明自己能提供其中哪些，路由由配置决定。
const (
	CNHS300Close     MetricID = "cn.hs300.close"
	CNHS300PE        MetricID = "cn.hs300.pe_ttm"
	CNBondY10        MetricID = "cn.bond.y10"
	CNCSIAllTurnover MetricID = "cn.csiall.turnover"
	CNMarketAmount   MetricID = "cn.market.amount"
	CNCSIAllAmount   MetricID = "cn.csiall.amount"
	CNMarginBuy      MetricID = "cn.margin.buy_amount"
	CNMarketFloatMV  MetricID = "cn.market.float_mv"
	CNBreadthMA200   MetricID = "cn.breadth.ma200"
)

func init() {
	for _, m := range []Metric{
		{ID: CNHS300Close, Market: "CN", Name: "沪深300 收盘价", Unit: "点"},
		{ID: CNHS300PE, Market: "CN", Name: "沪深300 市盈率(TTM)", Unit: "倍",
			Note: "各来源口径不同，切换来源后须重算分位"},
		{ID: CNBondY10, Market: "CN", Name: "中国10年期国债收益率", Unit: "%"},
		{ID: CNCSIAllTurnover, Market: "CN", Name: "中证全指换手率", Unit: "%"},
		{ID: CNMarketAmount, Market: "CN", Name: "沪深两市成交额", Unit: "元",
			Note: "上证指数 + 深证成指成交额，深市口径待核对"},
		{ID: CNCSIAllAmount, Market: "CN", Name: "中证全指成交额", Unit: "元",
			Note: "与沪深两市成交额口径不同，不可互为回退"},
		{ID: CNMarginBuy, Market: "CN", Name: "两融 融资买入额", Unit: "元", LagDays: 1},
		{ID: CNMarketFloatMV, Market: "CN", Name: "全A流通市值", Unit: "元", LagDays: 1,
			Note: "随两融数据 T+1 发布"},
		{ID: CNBreadthMA200, Market: "CN", Name: "全A站上200日均线占比", Unit: "%",
			Note: "由个股数据派生（后复权，含退市股），在数据页单独回填"},
	} {
		Define(m)
	}
}
