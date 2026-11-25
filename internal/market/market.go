// Package market 定义市场：引擎按"角色"取数，市场定义把角色映射到标准指标。
// 新增市场（如美股）时，只需新增一份定义和对应的数据源插件，引擎不变。
package market

import ds "market-thermometer/internal/datasource"

// Role 是引擎需要的输入角色。
type Role string

const (
	RoleClose     Role = "index_close" // 标的指数收盘价（同时作为交易日历）
	RolePE        Role = "pe_ttm"      // 估值：市盈率
	RoleBondY10   Role = "bond_y10"    // 估值：10 年期国债收益率（%）
	RoleAmount    Role = "amount"      // T1/T2：全市场成交额
	RoleFloatMV   Role = "float_mv"    // T1：全市场流通市值（T+1 发布）
	RoleMarginBuy Role = "margin_buy"  // T2：融资买入额（T+1 发布）
	RoleBreadth   Role = "breadth"     // T3：站上 200 日均线的个股占比（%）
)

// SlowRoles 是允许前值填充的慢变量（最多 Params.MaxFillDays 个交易日）。
var SlowRoles = map[Role]bool{RolePE: true, RoleBondY10: true}

type Market struct {
	ID    string
	Name  string
	Index string // 标的指数名称，用于展示
	Roles map[Role]ds.MetricID
}

var CN = Market{
	ID: "CN", Name: "A股", Index: "沪深300",
	Roles: map[Role]ds.MetricID{
		RoleClose:     ds.CNHS300Close,
		RolePE:        ds.CNHS300PE,
		RoleBondY10:   ds.CNBondY10,
		RoleAmount:    ds.CNCSIAllAmount,
		RoleFloatMV:   ds.CNMarketFloatMV,
		RoleMarginBuy: ds.CNMarginBuy,
		RoleBreadth:   ds.CNBreadthMA200,
	},
}
