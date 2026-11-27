// Package pmeval 提供 Polymarket 影子策略信号的严格统计评估与基于本地盘口快照的历史回放。
// 纯函数，不直接访问数据库或网络（通过回调读取盘口与现货心跳）。
//
// 评估遵循 docs/handoff.md 口径：
//   - 默认按“每个市场第一条信号”去重统计，避免 30 秒冷却内重复信号放大样本量；
//   - 按时间聚类（默认 15 分钟桶）衡量有效独立样本数，消除 BTC/ETH 与多周期高度相关；
//   - 区分起始价来源（exact / spot / bar）与剩余时间分桶（τ）；
//   - 回放时按“信号时刻 + 延迟”之后的下一张盘口快照吃单（IOC），并用 pm_spot 作心跳，心跳超时则拒单。
package pmeval

import (
	"math"
	"sort"
	"strings"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/pmsim"
	"market-thermometer/internal/pmstrat"
)

// Signal 是一条待评估的策略信号（已关联市场元数据与结算结果）。
type Signal struct {
	ID      int64
	T       int64 // Unix 毫秒
	Slug    string
	Series  string
	Cond    string
	Token0  string // Up / Yes 代币 ID
	Outcome int    // 0=Up, 1=Down
	EndT    int64  // Unix 秒
	P       float64
	Ask     float64
	AskSize float64
	FeeRate float64
	FeeExp  float64
	MinSize float64
	Edge    float64
	K       float64
	KSrc    string // exact / spot / bar
	SdBp    float64
	Tau     int64 // 秒
	Spot    float64
	Winner  int // -1 未结算, 0/1 赢家
}

// Config 是评估与回放参数。
type Config struct {
	MinEdge        float64 `json:"minEdge"`        // 最小预期收益筛选（默认 0.03）
	AskMin         float64 `json:"askMin"`         // 卖一价格下限过滤（0 不限）
	AskMax         float64 `json:"askMax"`         // 卖一价格上限过滤（0 不限）
	KSrc           string  `json:"kSrc"`           // "" 全部 | exact | spot | bar
	Series         string  `json:"series"`         // "" 全部或指定系列
	FirstPerMarket bool    `json:"firstPerMarket"` // 每个市场只取第一条信号（默认 true）
	ExecMode       string  `json:"execMode"`       // taker（默认，吃卖一扣费）| maker（挂买一 0 费率，需后续盘口穿越）
	MakerOffset    float64 `json:"makerOffset"`    // maker 模式下相对买一加价幅度（美元，默认 0）
	DelayMs        int64   `json:"delayMs"`        // 模拟下单延迟（毫秒，默认 1000）
	HeartbeatMs    int64   `json:"heartbeatMs"`    // 现货心跳最大允许滞后（毫秒，默认 15000）
	ClusterMin     int     `json:"clusterMin"`     // 时间聚类窗口（分钟，默认 15）
	InitialCash    float64 `json:"initialCash"`    // 回放初始资金（USDC，默认 1000）
	Sizing         string  `json:"sizing"`         // fixed | kelly（默认 fixed）
	OrderUSD       float64 `json:"orderUsd"`       // fixed 模式金额 / kelly 模式上限（默认 20）
	KellyFrac      float64 `json:"kellyFrac"`      // kelly 系数（默认 0.25）
	MaxAssetUSD    float64 `json:"maxAssetUsd"`    // 单资产（BTC/ETH）同时在途成本上限（默认 100，0 不限）
	UseFairLimit   bool    `json:"useFairLimit"`   // 是否以模型公允价扣费后作为限价（防止滑点翻负）
}

func DefaultConfig() Config {
	return Config{
		MinEdge: 0.03, FirstPerMarket: true, ExecMode: "taker", DelayMs: 1000, HeartbeatMs: 15_000, ClusterMin: 15,
		InitialCash: 1000, Sizing: "fixed", OrderUSD: 20, KellyFrac: 0.25, MaxAssetUSD: 100, UseFairLimit: true,
	}
}

func (c Config) normalize() Config {
	if c.ClusterMin <= 0 {
		c.ClusterMin = 15
	}
	if c.HeartbeatMs <= 0 {
		c.HeartbeatMs = 15_000
	}
	if c.InitialCash <= 0 {
		c.InitialCash = 1000
	}
	if c.OrderUSD <= 0 {
		c.OrderUSD = 20
	}
	if c.KellyFrac <= 0 {
		c.KellyFrac = 0.25
	}
	if c.Sizing == "" {
		c.Sizing = "fixed"
	}
	if c.ExecMode != "maker" {
		c.ExecMode = "taker"
	}
	return c
}

// Feed 提供本地历史盘口与现货心跳查询（由调用方注入 Store 实现）。
type Feed struct {
	// NextBook 返回 token 在 [atMs, endMs] 内的第一条快照。
	NextBook func(token string, atMs, endMs int64) (ds.Book, bool, error)
	// PrevBook 返回 token 在 atMs 之前（含）的最近一条快照（仅当心跳存活时，表明盘口 hash 未变化）。
	PrevBook func(token string, atMs int64) (ds.Book, bool, error)
	// BooksInRange 返回 token 在 [fromMs, toMs] 内的全部快照（升序，用于 Maker 限价挂单穿越检测）。
	BooksInRange func(token string, fromMs, toMs int64) ([]ds.Book, error)
	// LastSpotT 返回 symbol 在 atMs 之前（含）最近一条现货价的时间戳（Unix 毫秒）。
	LastSpotT func(symbol string, atMs int64) (int64, bool, error)
}

// BucketStat 是一组信号的统计汇总。
type BucketStat struct {
	Key            string  `json:"key"`
	N              int     `json:"n"`        // 信号数
	Resolved       int     `json:"resolved"` // 已结算数
	Wins           int     `json:"wins"`
	WinRate        float64 `json:"winRate"`
	AvgModelP      float64 `json:"avgModelP"`
	AvgAsk         float64 `json:"avgAsk"`
	AvgEdge        float64 `json:"avgEdge"`
	Brier          float64 `json:"brier"`          // Brier Score = mean((p - y)^2)，越低越准
	TheoreticalPnL float64 `json:"theoreticalPnL"` // 按信号时刻卖一每笔买 1 股（扣费）的累计盈亏
}

// CalibBin 是概率校准分桶。
type CalibBin struct {
	Label      string  `json:"label"`
	N          int     `json:"n"`
	MeanP      float64 `json:"meanP"`
	ActualRate float64 `json:"actualRate"`
	Gap        float64 `json:"gap"` // actualRate - meanP
}

// EquityPoint 是回放资金曲线的一个点。
type EquityPoint struct {
	T      int64   `json:"t"` // Unix 毫秒（结算或成交时刻）
	Equity float64 `json:"equity"`
	CumPnL float64 `json:"cumPnl"`
	Trades int     `json:"trades"`
}

// ReplayTrade 是一笔回放交易记录。
type ReplayTrade struct {
	ID         int64   `json:"id"`
	T          int64   `json:"t"`
	Slug       string  `json:"slug"`
	Series     string  `json:"series"`
	Outcome    int     `json:"outcome"`
	KSrc       string  `json:"kSrc"`
	Tau        int64   `json:"tau"`
	P          float64 `json:"p"`
	SignalAsk  float64 `json:"signalAsk"`
	FillAvg    float64 `json:"fillAvg"`
	Slippage   float64 `json:"slippage"`
	Shares     float64 `json:"shares"`
	Gross      float64 `json:"gross"`
	Fee        float64 `json:"fee"`
	PnL        float64 `json:"pnl"`
	Won        bool    `json:"won"`
	Status     string  `json:"status"`     // filled | skipped
	SkipReason string  `json:"skipReason"` // stale | no_book | risk_cap | min_size | no_fill | unresolved
	BookSource string  `json:"bookSource"` // next | carried
}

// ReplaySummary 是历史盘口回放汇总。
type ReplaySummary struct {
	Executed       int           `json:"executed"`
	Wins           int           `json:"wins"`
	Losses         int           `json:"losses"`
	WinRate        float64       `json:"winRate"`
	SkippedStale   int           `json:"skippedStale"`
	SkippedNoBook  int           `json:"skippedNoBook"`
	SkippedRisk    int           `json:"skippedRisk"`
	SkippedNoFill  int           `json:"skippedNoFill"`
	InitialCash    float64       `json:"initialCash"`
	FinalEquity    float64       `json:"finalEquity"`
	NetPnL         float64       `json:"netPnl"`
	TotalGross     float64       `json:"totalGross"`
	TotalFees      float64       `json:"totalFees"`
	ROIOnVolume    float64       `json:"roiOnVolume"`
	MaxDD          float64       `json:"maxDD"` // 相对权益峰值的最大回撤（小数，≤0）
	AvgSlippage    float64       `json:"avgSlippage"`
	Clusters       int           `json:"clusters"`
	ClusterWinRate float64       `json:"clusterWinRate"` // 按时间聚类汇总后正收益聚类占比
	ClusterSharpe  float64       `json:"clusterSharpe"`  // 聚类收益均值 / 标准差（衡量扣除相关性后的显著性）
	Curve          []EquityPoint `json:"curve"`
	Trades         []ReplayTrade `json:"trades"` // 最近 200 笔
}

// Report 是完整的评估与回放报告。
type Report struct {
	Config          Config        `json:"config"`
	RawSignals      int           `json:"rawSignals"`
	FilteredSignals int           `json:"filteredSignals"`
	UniqueMarkets   int           `json:"uniqueMarkets"`
	Clusters        int           `json:"clusters"`
	Overall         BucketStat    `json:"overall"`
	ByKSrc          []BucketStat  `json:"byKSrc"`
	BySeries        []BucketStat  `json:"bySeries"`
	ByTau           []BucketStat  `json:"byTau"`
	ByEdge          []BucketStat  `json:"byEdge"`
	ByAsk           []BucketStat  `json:"byAsk"`
	Calibration     []CalibBin    `json:"calibration"`
	Replay          ReplaySummary `json:"replay"`
}

func assetOf(series, slug string) string {
	s := series
	if s == "" {
		s = slug
	}
	a, _, _ := strings.Cut(s, "-")
	return strings.ToLower(a)
}

func symbolOf(series, slug string) string {
	a := assetOf(series, slug)
	if a == "" {
		return ""
	}
	return strings.ToUpper(a) + "USDT"
}

func tauBucket(tau int64) string {
	switch {
	case tau < 60:
		return "<60s"
	case tau < 180:
		return "60-180s"
	case tau < 300:
		return "180-300s"
	case tau < 900:
		return "300-900s"
	default:
		return ">900s"
	}
}

var (
	tauOrder  = map[string]int{"<60s": 0, "60-180s": 1, "180-300s": 2, "300-900s": 3, ">900s": 4}
	edgeOrder = map[string]int{"3-5¢": 0, "5-8¢": 1, "8-15¢": 2, "≥15¢": 3}
	askOrder  = map[string]int{"0-20¢": 0, "20-40¢": 1, "40-60¢": 2, "60-80¢": 3, "80-100¢": 4}
)

func edgeBucket(edge float64) string {
	switch {
	case edge < 0.05:
		return "3-5¢"
	case edge < 0.08:
		return "5-8¢"
	case edge < 0.15:
		return "8-15¢"
	default:
		return "≥15¢"
	}
}

func askBucket(ask float64) string {
	switch {
	case ask < 0.20:
		return "0-20¢"
	case ask < 0.40:
		return "20-40¢"
	case ask < 0.60:
		return "40-60¢"
	case ask < 0.80:
		return "60-80¢"
	default:
		return "80-100¢"
	}
}

// Evaluate 对升序信号列表执行过滤、分层统计、校准检验与（当 feed != nil 时）历史盘口回放。
func Evaluate(signals []Signal, cfg Config, feed *Feed) Report {
	cfg = cfg.normalize()
	rep := Report{Config: cfg, RawSignals: len(signals)}

	// 1. 过滤与按市场去重
	seenMarket := map[string]bool{}
	var filtered []Signal
	for _, s := range signals {
		if s.Edge < cfg.MinEdge-1e-12 {
			continue
		}
		if cfg.AskMin > 0 && s.Ask < cfg.AskMin-1e-12 {
			continue
		}
		if cfg.AskMax > 0 && s.Ask > cfg.AskMax+1e-12 {
			continue
		}
		if cfg.KSrc != "" && s.KSrc != cfg.KSrc {
			continue
		}
		if cfg.Series != "" && s.Series != cfg.Series {
			continue
		}
		if cfg.FirstPerMarket && seenMarket[s.Cond] {
			continue
		}
		seenMarket[s.Cond] = true
		filtered = append(filtered, s)
	}
	rep.FilteredSignals = len(filtered)
	markets := map[string]bool{}
	clusters := map[int64]bool{}
	clusterSpan := int64(cfg.ClusterMin) * 60 * 1000
	for _, s := range filtered {
		markets[s.Cond] = true
		clusters[s.T/clusterSpan] = true
	}
	rep.UniqueMarkets = len(markets)
	rep.Clusters = len(clusters)

	// 2. 分层统计与概率校准
	rep.Overall = calcBucket("all", filtered)
	rep.ByKSrc = groupBy(filtered, func(s Signal) string { return s.KSrc }, nil)
	rep.BySeries = groupBy(filtered, func(s Signal) string {
		if s.Series != "" {
			return s.Series
		}
		return "unknown"
	}, nil)
	rep.ByTau = groupBy(filtered, func(s Signal) string { return tauBucket(s.Tau) }, func(a, b string) bool {
		return tauOrder[a] < tauOrder[b]
	})
	rep.ByEdge = groupBy(filtered, func(s Signal) string { return edgeBucket(s.Edge) }, func(a, b string) bool {
		return edgeOrder[a] < edgeOrder[b]
	})
	rep.ByAsk = groupBy(filtered, func(s Signal) string { return askBucket(s.Ask) }, func(a, b string) bool {
		return askOrder[a] < askOrder[b]
	})
	rep.Calibration = calcCalibration(filtered)

	// 3. 本地盘口回放
	rep.Replay = runReplay(filtered, cfg, feed)
	return rep
}

func calcBucket(key string, list []Signal) BucketStat {
	b := BucketStat{Key: key, N: len(list)}
	if len(list) == 0 {
		return b
	}
	var sumP, sumAsk, sumEdge, brierSum, theoPnL float64
	for _, s := range list {
		sumP += s.P
		sumAsk += s.Ask
		sumEdge += s.Edge
		if s.Winner < 0 {
			continue
		}
		b.Resolved++
		y := 0.0
		if s.Winner == s.Outcome {
			b.Wins++
			y = 1.0
		}
		d := s.P - y
		brierSum += d * d
		fee := pmsim.Fee{Rate: s.FeeRate, Exp: s.FeeExp}.Of(s.Ask, 1)
		theoPnL += y - s.Ask - fee
	}
	n := float64(len(list))
	b.AvgModelP = round4(sumP / n)
	b.AvgAsk = round4(sumAsk / n)
	b.AvgEdge = round4(sumEdge / n)
	if b.Resolved > 0 {
		r := float64(b.Resolved)
		b.WinRate = round4(float64(b.Wins) / r)
		b.Brier = round4(brierSum / r)
		b.TheoreticalPnL = round4(theoPnL)
	}
	return b
}

func groupBy(list []Signal, keyFn func(Signal) string, less func(a, b string) bool) []BucketStat {
	m := map[string][]Signal{}
	for _, s := range list {
		k := keyFn(s)
		m[k] = append(m[k], s)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if less != nil {
			return less(keys[i], keys[j])
		}
		return keys[i] < keys[j]
	})
	out := make([]BucketStat, 0, len(keys))
	for _, k := range keys {
		out = append(out, calcBucket(k, m[k]))
	}
	return out
}

func calcCalibration(list []Signal) []CalibBin {
	labels := []string{"0–20%", "20–40%", "40–60%", "60–80%", "80–100%"}
	var bins [5]struct {
		n    int
		sumP float64
		wins int
	}
	for _, s := range list {
		if s.Winner < 0 {
			continue
		}
		idx := int(math.Floor(s.P * 5))
		if idx < 0 {
			idx = 0
		}
		if idx > 4 {
			idx = 4
		}
		bins[idx].n++
		bins[idx].sumP += s.P
		if s.Winner == s.Outcome {
			bins[idx].wins++
		}
	}
	out := make([]CalibBin, 0, 5)
	for i, b := range bins {
		cb := CalibBin{Label: labels[i], N: b.n}
		if b.n > 0 {
			cb.MeanP = round4(b.sumP / float64(b.n))
			cb.ActualRate = round4(float64(b.wins) / float64(b.n))
			cb.Gap = round4(cb.ActualRate - cb.MeanP)
		}
		out = append(out, cb)
	}
	return out
}

func runReplay(list []Signal, cfg Config, feed *Feed) ReplaySummary {
	res := ReplaySummary{InitialCash: cfg.InitialCash, FinalEquity: cfg.InitialCash, Curve: []EquityPoint{}, Trades: []ReplayTrade{}}
	if feed == nil || len(list) == 0 {
		return res
	}
	type openPos struct {
		asset string
		cost  float64
		endMs int64
		pnl   float64
		won   bool
		t     int64
	}
	cash := cfg.InitialCash
	equity := cfg.InitialCash
	peak := equity
	var open []openPos
	var allTrades []ReplayTrade
	clusterSpan := int64(cfg.ClusterMin) * 60 * 1000
	clusterPnL := map[int64]float64{}
	var slipSum float64

	settleUntil := func(nowMs int64) {
		sort.Slice(open, func(i, j int) bool { return open[i].endMs < open[j].endMs })
		remain := open[:0]
		for _, p := range open {
			if p.endMs <= nowMs {
				cash += p.cost + p.pnl
				equity = cash
				for _, r := range remain {
					equity += r.cost
				}
				if equity > peak {
					peak = equity
				}
				if peak > 0 {
					dd := equity/peak - 1
					if dd < res.MaxDD {
						res.MaxDD = dd
					}
				}
				res.Curve = append(res.Curve, EquityPoint{
					T:      p.endMs,
					Equity: round2(equity),
					CumPnL: round2(equity - cfg.InitialCash),
					Trades: res.Executed,
				})
			} else {
				remain = append(remain, p)
			}
		}
		open = remain
	}

	assetOpenCost := func(asset string) float64 {
		sum := 0.0
		for _, p := range open {
			if p.asset == asset {
				sum += p.cost
			}
		}
		return sum
	}

	for _, s := range list {
		execMs := s.T + cfg.DelayMs
		settleUntil(execMs)
		tr := ReplayTrade{
			ID: s.ID, T: s.T, Slug: s.Slug, Series: s.Series, Outcome: s.Outcome,
			KSrc: s.KSrc, Tau: s.Tau, P: round4(s.P), SignalAsk: round4(s.Ask), Status: "skipped",
		}
		if s.Winner < 0 {
			tr.SkipReason = "unresolved"
			allTrades = append(allTrades, tr)
			continue
		}
		// 心跳检查（防止盘口 hash 去重把采集中断误当成盘口未变）
		sym := symbolOf(s.Series, s.Slug)
		if feed.LastSpotT != nil && sym != "" {
			st, ok, err := feed.LastSpotT(sym, execMs)
			if err != nil || !ok || execMs-st > cfg.HeartbeatMs {
				res.SkippedStale++
				tr.SkipReason = "stale"
				allTrades = append(allTrades, tr)
				continue
			}
		}
		// 查盘口：优先取执行时刻之后的下一张快照，若期间无变化（且心跳存活）则沿用执行时刻前的最近快照
		endMs := s.EndT * 1000
		if endMs <= execMs {
			endMs = execMs + 60_000
		}
		var book ds.Book
		var ok bool
		if s.Token0 != "" && feed.NextBook != nil {
			book, ok, _ = feed.NextBook(s.Token0, execMs, endMs)
			if ok {
				tr.BookSource = "next"
			}
		}
		if !ok && s.Token0 != "" && feed.PrevBook != nil {
			book, ok, _ = feed.PrevBook(s.Token0, execMs)
			if ok {
				tr.BookSource = "carried"
			}
		}
		if !ok {
			res.SkippedNoBook++
			tr.SkipReason = "no_book"
			allTrades = append(allTrades, tr)
			continue
		}
		ob := pmsim.OutcomeBook(book, s.Outcome)
		if len(ob.Asks) == 0 {
			res.SkippedNoFill++
			tr.SkipReason = "no_fill"
			allTrades = append(allTrades, tr)
			continue
		}
		bestAsk := ob.Asks[0].Price
		// 仓位计算
		curEquity := cash
		for _, p := range open {
			curEquity += p.cost
		}
		usd := pmstrat.SizeUSD(curEquity, s.P, bestAsk, s.FeeRate, pmstrat.SizingConfig{
			Mode: cfg.Sizing, FixedUSD: cfg.OrderUSD, KellyFrac: cfg.KellyFrac, MinUSD: 5,
		})
		if usd <= 0 {
			res.SkippedNoFill++
			tr.SkipReason = "no_fill"
			allTrades = append(allTrades, tr)
			continue
		}
		// 单资产同时在途敞口检查
		asset := assetOf(s.Series, s.Slug)
		if cfg.MaxAssetUSD > 0 && assetOpenCost(asset)+usd > cfg.MaxAssetUSD+1e-9 {
			res.SkippedRisk++
			tr.SkipReason = "risk_cap"
			allTrades = append(allTrades, tr)
			continue
		}
		var fill pmsim.Fill
		minSize := s.MinSize
		if minSize <= 0 {
			minSize = 5
		}
		if cfg.ExecMode == "maker" {
			// Maker 被动挂单（0 手续费）：挂在本方买一 + MakerOffset（不超过卖一减 1¢ 且不超公允价），
			// 若后续快照出现对侧卖一 ≤ 挂单价则视为被动成交。
			if len(ob.Bids) == 0 || feed.BooksInRange == nil {
				res.SkippedNoFill++
				tr.SkipReason = "no_fill"
				allTrades = append(allTrades, tr)
				continue
			}
			myBid := ob.Bids[0].Price + cfg.MakerOffset
			if bestAsk > 0.02 && myBid >= bestAsk {
				myBid = bestAsk - 0.01
			}
			if cfg.UseFairLimit && myBid > s.P-cfg.MinEdge {
				myBid = s.P - cfg.MinEdge
			}
			myBid = math.Floor(myBid*100) / 100
			if myBid <= 0.01 || myBid >= 0.99 {
				res.SkippedNoFill++
				tr.SkipReason = "no_fill"
				allTrades = append(allTrades, tr)
				continue
			}
			snaps, _ := feed.BooksInRange(s.Token0, execMs, endMs)
			crossed := false
			for _, sn := range snaps {
				sob := pmsim.OutcomeBook(sn, s.Outcome)
				if len(sob.Asks) > 0 && sob.Asks[0].Price <= myBid+1e-9 {
					crossed = true
					break
				}
			}
			if !crossed {
				res.SkippedNoFill++
				tr.SkipReason = "no_fill"
				allTrades = append(allTrades, tr)
				continue
			}
			shares := math.Floor((usd/myBid)*100) / 100
			fill = pmsim.Fill{Shares: shares, Gross: round4(shares * myBid), Fee: 0}
			tr.BookSource = "maker"
		} else {
			limit := 0.0
			if cfg.UseFairLimit {
				feeAtP := pmsim.Fee{Rate: s.FeeRate, Exp: s.FeeExp}.Of(bestAsk, 1)
				limit = s.P - feeAtP
				if limit <= 0 {
					res.SkippedNoFill++
					tr.SkipReason = "no_fill"
					allTrades = append(allTrades, tr)
					continue
				}
			}
			fill = pmsim.Match(ob, pmsim.Buy, 0, usd, limit, pmsim.Fee{Rate: s.FeeRate, Exp: s.FeeExp})
		}
		if fill.Shares < minSize || fill.Gross+fill.Fee > cash+1e-9 {
			res.SkippedNoFill++
			tr.SkipReason = "no_fill"
			allTrades = append(allTrades, tr)
			continue
		}
		cost := fill.Gross + fill.Fee
		cash -= cost
		won := s.Winner == s.Outcome
		payout := 0.0
		if won {
			payout = fill.Shares
			res.Wins++
		} else {
			res.Losses++
		}
		pnl := payout - cost
		res.Executed++
		res.TotalGross += fill.Gross
		res.TotalFees += fill.Fee
		avg := fill.Avg()
		slip := avg - s.Ask
		slipSum += slip
		clusterPnL[s.T/clusterSpan] += pnl
		open = append(open, openPos{asset: asset, cost: cost, endMs: endMs, pnl: pnl, won: won, t: s.T})
		tr.Status = "filled"
		tr.FillAvg = round4(avg)
		tr.Slippage = round4(slip)
		tr.Shares = round2(fill.Shares)
		tr.Gross = round2(fill.Gross)
		tr.Fee = round4(fill.Fee)
		tr.PnL = round2(pnl)
		tr.Won = won
		allTrades = append(allTrades, tr)
	}
	settleUntil(1 << 62)

	res.FinalEquity = round2(cash)
	res.NetPnL = round2(cash - cfg.InitialCash)
	res.TotalGross = round2(res.TotalGross)
	res.TotalFees = round2(res.TotalFees)
	res.MaxDD = round4(res.MaxDD)
	if res.Executed > 0 {
		res.WinRate = round4(float64(res.Wins) / float64(res.Executed))
		res.AvgSlippage = round4(slipSum / float64(res.Executed))
	}
	if res.TotalGross > 0 {
		res.ROIOnVolume = round4(res.NetPnL / res.TotalGross)
	}
	if len(clusterPnL) > 0 {
		res.Clusters = len(clusterPnL)
		var xs []float64
		posC := 0
		for _, v := range clusterPnL {
			xs = append(xs, v)
			if v > 0 {
				posC++
			}
		}
		res.ClusterWinRate = round4(float64(posC) / float64(len(xs)))
		if len(xs) >= 2 {
			mean := 0.0
			for _, v := range xs {
				mean += v
			}
			mean /= float64(len(xs))
			var vsum float64
			for _, v := range xs {
				d := v - mean
				vsum += d * d
			}
			sd := math.Sqrt(vsum / float64(len(xs)-1))
			if sd > 0 {
				res.ClusterSharpe = round4(mean / sd)
			}
		}
	}
	// 保留最近 200 笔（倒序，最新在前）
	for i, j := 0, len(allTrades)-1; i < j; i, j = i+1, j-1 {
		allTrades[i], allTrades[j] = allTrades[j], allTrades[i]
	}
	if len(allTrades) > 200 {
		allTrades = allTrades[:200]
	}
	res.Trades = allTrades
	return res
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
