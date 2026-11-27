package job

import (
	"context"
	"fmt"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/market"
	"market-thermometer/internal/portfolio"
	"market-thermometer/internal/store"
)

// AnalyzePortfolio 读取本地数据分析组合：股票、场内基金用后复权价（由不复权收盘价与除权乘数链式计算），
// 场外基金用累计净值；交易日历与基准为该市场的标的指数（沪深300）。
func AnalyzePortfolio(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, p store.Portfolio, start string, rebalance string, dca float64) (portfolio.Output, error) {
	bench, err := loadMetric(ctx, st, hub, mkt.Roles[market.RoleClose])
	if err != nil {
		return portfolio.Output{}, err
	}
	in := portfolio.Input{Bench: map[string]float64{}, Start: start, Rebalance: rebalance, DCA: dca}
	for _, pt := range bench {
		in.Calendar = append(in.Calendar, pt.Date)
		in.Bench[pt.Date] = pt.Value
	}
	for _, it := range p.Items {
		a := portfolio.Asset{Key: it.Kind + ":" + it.Code, Label: it.Name, Weight: it.Weight, Prices: map[string]float64{}}
		if a.Label == "" {
			a.Label = it.Code
		}
		switch it.Kind {
		case store.SecStock, store.SecFund:
			bars, err := st.StockBars(ctx, it.Code, "")
			if err != nil {
				return portfolio.Output{}, err
			}
			dates, closes, mults := make([]string, len(bars)), make([]float64, len(bars)), make([]float64, len(bars))
			for i, b := range bars {
				dates[i], closes[i], mults[i] = b.Date, b.Close, b.Mult
			}
			a.Prices = portfolio.HFQ(dates, closes, mults)
		case store.SecOFund:
			navs, err := st.FundNAV(ctx, it.Code)
			if err != nil {
				return portfolio.Output{}, err
			}
			for _, n := range navs {
				if n.AccNAV > 0 {
					a.Prices[n.Date] = n.AccNAV
				}
			}
		default:
			return portfolio.Output{}, fmt.Errorf("未知的证券类别 %q", it.Kind)
		}
		in.Assets = append(in.Assets, a)
	}
	return portfolio.Analyze(in), nil
}

// loadMetric 按路由优先级取库中有数据的来源，读出整段序列。
func loadMetric(ctx context.Context, st *store.Store, hub *ds.Hub, metric ds.MetricID) ([]ds.Point, error) {
	cov, err := st.Coverage(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, c := range cov {
		if c.Metric == metric {
			have[c.Source] = true
		}
	}
	src := pickSource(hub.Route(metric), have)
	if src == "" {
		return nil, fmt.Errorf("本地没有 %s 的数据，先同步指标", metric)
	}
	return st.Series(ctx, metric, src, "", "")
}
