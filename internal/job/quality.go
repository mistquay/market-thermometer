package job

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/market"
	"market-thermometer/internal/quality"
	"market-thermometer/internal/store"
)

// ---- 数据质量报告（只读本地数据，不发网络请求）----

const (
	qualityWindowDays = 365  // 缺口检测窗口（自然日）
	coverageDays      = 60   // 按日覆盖展示的交易日数
	coverageRatio     = 0.95 // 低于中位数的该比例视为异常
	recentTradingDays = 10   // 近期：数据源可能仍是临时值（与同步时往回重拉的范围一致）
	maxGapStocks      = 1000
)

// Freshness 是一个数据项的最新日期与应有日期。
type Freshness struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Last     string `json:"last"`
	Expected string `json:"expected"`
	Behind   int    `json:"behind"` // 落后的交易日数；-1 表示没有数据
}

// StockGap 是一只证券在窗口内的缺口。
type StockGap struct {
	Code    string        `json:"code"`
	Missing int           `json:"missing"`
	Runs    []quality.Run `json:"runs"`
}

// QualityReport 是数据质量页的内容。
type QualityReport struct {
	CalendarLast string             `json:"calendarLast"`
	Freshness    []Freshness        `json:"freshness"`
	Coverage     []quality.DayCount `json:"coverage"`
	WindowFrom   string             `json:"windowFrom"`
	GapStocks    int                `json:"gapStocks"`  // 有缺口的股票数
	GapDays      int                `json:"gapDays"`    // 缺失的股票·交易日总数
	Gaps         []StockGap         `json:"gaps"`       // 缺失最多的前 1000 只
	CrossCheck   *CrossCheckResult  `json:"crossCheck"` // 最近一次交叉核对
	ElapsedMs    int64              `json:"elapsedMs"`
}

// Quality 生成数据质量报告（约 1–3 秒：一次扫描近一年的个股日线日期）。
func Quality(ctx context.Context, st *store.Store, hub *ds.Hub, mkt market.Market, now time.Time) (*QualityReport, error) {
	began := time.Now()
	p, err := st.ActiveParams(ctx, now)
	if err != nil {
		return nil, err
	}
	from := now.AddDate(0, 0, -qualityWindowDays-30).Format("2006-01-02")
	cal, err := st.Calendar(ctx, mkt.ID, p.Version(), from)
	if err != nil {
		return nil, err
	}
	if len(cal) == 0 {
		return nil, errors.New("还没有得分数据（交易日历），先同步指标")
	}
	rep := &QualityReport{CalendarLast: cal[len(cal)-1]}
	expected := func(lag int) string { return cal[max(0, len(cal)-1-lag)] }

	// 指标新鲜度：取各来源中最新的一个
	cov, err := st.Coverage(ctx)
	if err != nil {
		return nil, err
	}
	latest := map[ds.MetricID]store.Coverage{}
	for _, c := range cov {
		if c.Last > latest[c.Metric].Last {
			latest[c.Metric] = c
		}
	}
	for _, m := range ds.Metrics() {
		c, have := latest[m.ID]
		if !have && len(hub.Route(m.ID)) == 0 {
			continue // 未启用且无数据
		}
		exp := expected(m.LagDays)
		rep.Freshness = append(rep.Freshness, Freshness{ID: string(m.ID), Name: m.Name, Source: c.Source, Last: c.Last,
			Expected: exp, Behind: quality.Behind(cal, c.Last, exp)})
	}
	for _, k := range []struct{ kind, name string }{{ds.KindStock, "个股日线"}, {ds.KindFund, "场内基金日线"}} {
		last, err := st.KindLastDate(ctx, k.kind)
		if err != nil {
			return nil, err
		}
		exp := expected(0)
		rep.Freshness = append(rep.Freshness, Freshness{ID: "stock_bar:" + k.kind, Name: k.name, Source: "local", Last: last,
			Expected: exp, Behind: quality.Behind(cal, last, exp)})
	}

	// 个股缺口与按日覆盖（只统计 A 股；场内基金上市晚、停牌少，暂不检测）
	rep.WindowFrom = now.AddDate(0, 0, -qualityWindowDays).Format("2006-01-02")
	dates, err := st.StockDates(ctx, ds.KindStock, rep.WindowFrom)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var gaps []StockGap
	for code, days := range dates {
		for _, d := range days {
			counts[d]++
		}
		runs := quality.Gaps(cal, days)
		if len(runs) == 0 {
			continue
		}
		g := StockGap{Code: code, Runs: runs}
		for _, r := range runs {
			g.Missing += r.Days
		}
		gaps = append(gaps, g)
		rep.GapDays += g.Missing
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Missing != gaps[j].Missing {
			return gaps[i].Missing > gaps[j].Missing
		}
		return gaps[i].Code < gaps[j].Code
	})
	rep.GapStocks = len(gaps)
	rep.Gaps = gaps[:min(len(gaps), maxGapStocks)]
	stockLast, err := st.KindLastDate(ctx, ds.KindStock)
	if err != nil {
		return nil, err
	}
	recent := cal[max(0, len(cal)-coverageDays):]
	rep.Coverage = quality.Coverage(recent, counts, stockLast, coverageRatio)

	var cc CrossCheckResult
	if ok, err := st.LoadJSON(ctx, SettingCrossCheck, &cc); err != nil {
		return nil, err
	} else if ok {
		rep.CrossCheck = &cc
	}
	rep.ElapsedMs = time.Since(began).Milliseconds()
	return rep, nil
}

// ---- 多来源交叉核对（后台任务，会请求数据源）----

// SettingCrossCheck：最近一次交叉核对结果（JSON，存 meta）。
const SettingCrossCheck = "quality.crosscheck"

const (
	crossSample    = 30  // 默认抽样股票数（另固定包含 600000）
	crossMaxDiffs  = 300 // 结果中最多保留的差异明细
	crossMaxErrors = 3   // 某来源连续失败次数达到后跳过其余股票（如腾讯熔断）
)

// CrossSource 是某个数据源与本地比对的汇总。
type CrossSource struct {
	Source     string   `json:"source"`
	Codes      int      `json:"codes"`      // 成功比对的股票数
	Compared   int      `json:"compared"`   // 双方都有的日线根数
	Close      int      `json:"close"`      // 收盘价不一致（不含近期）
	Mult       int      `json:"mult"`       // 除权乘数不一致（不含近期）
	OnlySource int      `json:"onlySource"` // 来源有、本地缺（不含近期）
	OnlyLocal  int      `json:"onlyLocal"`  // 本地有、来源缺（不含近期）
	Recent     int      `json:"recent"`     // 近期差异（可能是临时值）
	Errors     []string `json:"errors,omitempty"`
	Skipped    string   `json:"skipped,omitempty"`
}

// CrossCheckResult 是一次交叉核对的结果。
type CrossCheckResult struct {
	At         time.Time      `json:"at"`
	From       string         `json:"from"`
	RecentFrom string         `json:"recentFrom"`
	Codes      []string       `json:"codes"`
	Sources    []CrossSource  `json:"sources"`
	Diffs      []quality.Diff `json:"diffs"`
	Truncated  bool           `json:"truncated"`
}

// StartCrossCheck 在后台抽样比对本地个股日线与各数据源（codes 为空时随机抽样）。
func (r *Runner) StartCrossCheck(codes []string) (*Status, error) {
	if len(r.stocks) == 0 {
		return nil, ErrNoStockSource
	}
	id, err := r.begin("crosscheck")
	if err != nil {
		return nil, err
	}
	go func() {
		state, detail := "done", "交叉核对完成"
		if err := r.crossCheck(context.Background(), codes); err != nil {
			state, detail = "failed", err.Error()
			r.logf("✗ %v", err)
		}
		r.finish(id, state, detail)
	}()
	return r.Current(), nil
}

func (r *Runner) crossCheck(ctx context.Context, codes []string) error {
	now := r.now()
	p, err := r.store.ActiveParams(ctx, now)
	if err != nil {
		return err
	}
	from := now.AddDate(0, 0, -qualityWindowDays).Format("2006-01-02")
	cal, err := r.store.Calendar(ctx, r.mkt.ID, p.Version(), from)
	if err != nil {
		return err
	}
	if len(cal) == 0 {
		return errors.New("还没有交易日历，先同步指标")
	}
	if len(codes) == 0 {
		if codes, err = r.sampleStocks(ctx, now); err != nil {
			return err
		}
	}
	res := CrossCheckResult{At: now, From: from, RecentFrom: cal[max(0, len(cal)-recentTradingDays)], Codes: codes}
	r.logf("交叉核对：%d 只股票，%s 起，以本地数据为基准；%s 及以后的差异视为近期（可能是临时值）", len(codes), from, res.RecentFrom)
	r.update(func(s *Status) { s.Total, s.Done, s.Current = len(codes)*len(r.stocks), 0, "crosscheck" })

	for _, src := range r.stocks {
		cs := CrossSource{Source: src.Name()}
		fails := 0
		for _, code := range codes {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.update(func(s *Status) { s.Done++ })
			if cs.Skipped != "" {
				continue
			}
			sb, err := src.Bars(ctx, code, from, "")
			if err != nil {
				fails++
				cs.Errors = append(cs.Errors, fmt.Sprintf("%s：%v", code, err))
				if fails >= crossMaxErrors {
					cs.Skipped = fmt.Sprintf("连续 %d 次失败，跳过其余股票", fails)
					r.logf("✗ %s %s", src.Name(), cs.Skipped)
				}
				continue
			}
			fails = 0
			lb, err := r.store.StockBars(ctx, code, "")
			if err != nil {
				return err
			}
			n, diffs := quality.CompareBars(code, toQBars(lb, from), toQBars(sb, from), res.RecentFrom)
			if n == 0 {
				continue
			}
			cs.Codes++
			cs.Compared += n
			for _, d := range diffs {
				if d.Recent {
					cs.Recent++
				} else {
					switch d.Kind {
					case "close":
						cs.Close++
					case "mult":
						cs.Mult++
					case "only_source":
						cs.OnlySource++
					case "only_local":
						cs.OnlyLocal++
					}
				}
				if len(res.Diffs) < crossMaxDiffs {
					d.Src = src.Name()
					res.Diffs = append(res.Diffs, d)
				} else {
					res.Truncated = true
				}
			}
		}
		r.logf("✓ %s：比对 %d 只、%d 根日线；收盘价不一致 %d、除权乘数不一致 %d、来源有本地缺 %d、本地有来源缺 %d；近期差异 %d",
			cs.Source, cs.Codes, cs.Compared, cs.Close, cs.Mult, cs.OnlySource, cs.OnlyLocal, cs.Recent)
		res.Sources = append(res.Sources, cs)
	}
	return r.store.SaveJSON(context.WithoutCancel(ctx), SettingCrossCheck, res)
}

func toQBars(bars []ds.StockBar, from string) []quality.Bar {
	out := make([]quality.Bar, 0, len(bars))
	for _, b := range bars {
		if b.Date >= from {
			out = append(out, quality.Bar{Date: b.Date, Close: b.Close, Mult: b.Mult})
		}
	}
	return out
}

// sampleStocks 从本地在市且有日线的股票中随机抽样（以日期为种子，同一天结果相同），固定包含 600000。
func (r *Runner) sampleStocks(ctx context.Context, now time.Time) ([]string, error) {
	list, err := r.store.StockList(ctx)
	if err != nil {
		return nil, err
	}
	var codes []string
	for _, s := range list {
		if s.Kind == ds.KindStock && !s.Delisted && s.LastDate != "" && s.Code != "600000" {
			codes = append(codes, s.Code)
		}
	}
	seed, _ := strconv.ParseUint(now.In(ds.Shanghai).Format("20060102"), 10, 64)
	rnd := rand.New(rand.NewPCG(seed, seed))
	rnd.Shuffle(len(codes), func(i, j int) { codes[i], codes[j] = codes[j], codes[i] })
	return append([]string{"600000"}, codes[:min(len(codes), crossSample)]...), nil
}
