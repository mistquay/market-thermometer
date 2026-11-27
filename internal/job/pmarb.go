package job

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/pmarb"
	"market-thermometer/internal/store"
)

// PMArbSnapshot 是一次套利扫描的完整结果。
type PMArbSnapshot struct {
	ScannedEvents int                 `json:"scannedEvents"`
	ScannedTokens int                 `json:"scannedTokens"`
	TargetShares  float64             `json:"targetShares"`
	Discover      bool                `json:"discover"`
	At            time.Time           `json:"at"`
	Opportunities []pmarb.Opportunity `json:"opportunities"`
	PositiveCount int                 `json:"positiveCount"`
}

// PMArbHit 是一条历史记录的正期望套利机会。
type PMArbHit struct {
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	Slug       string    `json:"slug"`
	Title      string    `json:"title"`
	Shares     float64   `json:"shares"`
	RawSumAsk  float64   `json:"rawSumAsk"`
	NetPerUnit float64   `json:"netPerUnit"`
	NetProfit  float64   `json:"netProfit"`
}

// PMArbMonitor 定期扫描本地系列、关注事件及全站高成交 NegRisk 事件的扣费套利机会，
// 发现净收益为正的机会时记录历史并通过运维告警推送。
type PMArbMonitor struct {
	store  *store.Store
	src    ds.PredictionSource
	series []string
	every  time.Duration
	shares float64
	pusher *Pusher
	now    func() time.Time
	logf   func(string, ...any)

	mu      sync.RWMutex
	last    *PMArbSnapshot
	history []PMArbHit
}

func NewPMArbMonitor(st *store.Store, src ds.PredictionSource, series []string, every time.Duration, pusher *Pusher) *PMArbMonitor {
	if every <= 0 {
		every = 90 * time.Second
	}
	return &PMArbMonitor{store: st, src: src, series: series, every: every, shares: 50, pusher: pusher, now: time.Now, logf: log.Printf}
}

func (m *PMArbMonitor) Run(ctx context.Context) {
	t := time.NewTicker(m.every)
	defer t.Stop()
	// 启动 15 秒后首次扫描，避免和启动期同步抢资源
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	for {
		if _, err := m.Scan(ctx, m.shares, true); err != nil && ctx.Err() == nil {
			m.logf("套利监控：%v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Scan 执行一次套利扫描；discover=true 时额外从全站高成交活跃事件发现 NegRisk 多结果套利。
func (m *PMArbMonitor) Scan(ctx context.Context, targetShares float64, discover bool) (PMArbSnapshot, error) {
	if targetShares <= 0 || targetShares > 10000 {
		targetShares = 50
	}
	now := m.now()
	open := false
	seenSlug := map[string]bool{}
	var events []ds.PMEvent

	for _, ser := range m.series {
		evs, err := m.store.PMEvents(ctx, store.PMEventQuery{Series: ser, Closed: &open, From: now.Unix(), Asc: true, Limit: 2})
		if err != nil {
			continue
		}
		for _, e := range evs {
			if !seenSlug[e.Slug] {
				seenSlug[e.Slug] = true
				events = append(events, e)
			}
		}
	}
	if watched, err := m.store.PMWatched(ctx); err == nil {
		for _, slug := range watched {
			if seenSlug[slug] {
				continue
			}
			if e, err := m.store.PMEventBySlug(ctx, slug); err == nil && !e.Closed {
				seenSlug[slug] = true
				events = append(events, e)
			}
		}
	}
	if discover {
		if act, ok := m.src.(ds.PredictionActivity); ok {
			if top, err := act.TopActiveEvents(ctx, 25); err == nil {
				for _, e := range top {
					// 跳过子市场过多（>20 条腿）的赛事盘口，保持批量查盘口速度
					if !seenSlug[e.Slug] && !e.Closed && len(e.Markets) >= 2 && len(e.Markets) <= 20 && e.NegRisk {
						seenSlug[e.Slug] = true
						events = append(events, e)
					}
				}
			}
		}
	}

	tokSet := map[string]bool{}
	var tokens []string
	for _, e := range events {
		for _, mk := range e.Markets {
			if !mk.Closed && len(mk.Tokens) > 0 && !tokSet[mk.Tokens[0]] {
				tokSet[mk.Tokens[0]] = true
				tokens = append(tokens, mk.Tokens[0])
			}
		}
	}
	bookMap := map[string]ds.Book{}
	for i := 0; i < len(tokens); i += 50 {
		batch := tokens[i:min(i+50, len(tokens))]
		bs, err := m.src.Books(ctx, batch)
		if err != nil {
			return PMArbSnapshot{}, err
		}
		for _, b := range bs {
			bookMap[b.Token] = b
		}
	}
	opps := []pmarb.Opportunity{}
	pos := 0
	for _, e := range events {
		if e.NegRisk && len(e.Markets) >= 2 {
			if opp, ok := pmarb.ScanNegRisk(e, bookMap, targetShares); ok {
				opps = append(opps, opp)
				if opp.NetProfit > 0 {
					pos++
				}
			}
			continue
		}
		for _, mk := range e.Markets {
			if len(mk.Tokens) >= 2 {
				if b, ok := bookMap[mk.Tokens[0]]; ok {
					if opp, ok := pmarb.ScanBinary(e, mk, b, targetShares); ok {
						opps = append(opps, opp)
						if opp.NetProfit > 0 {
							pos++
						}
					}
				}
			}
		}
	}
	pmarb.SortOpportunities(opps)
	snap := PMArbSnapshot{
		ScannedEvents: len(events), ScannedTokens: len(tokens), TargetShares: targetShares,
		Discover: discover, At: now, Opportunities: opps, PositiveCount: pos,
	}

	m.mu.Lock()
	m.last = &snap
	for _, o := range opps {
		if o.NetPerUnit >= 0.005 && o.NetProfit >= 0.5 {
			m.history = append([]PMArbHit{{
				At: now, Kind: o.Kind, Slug: o.Slug, Title: o.Title, Shares: o.Shares,
				RawSumAsk: o.RawSumAsk, NetPerUnit: o.NetPerUnit, NetProfit: o.NetProfit,
			}}, m.history...)
			if m.pusher != nil && o.NetPerUnit >= 0.01 && o.NetProfit >= 1 {
				go m.pusher.SendAlert(context.Background(), "pm-arb-"+o.Slug, "💰【Polymarket 扣费正套利机会】"+o.Title, []string{
					fmt.Sprintf("类型：%s｜可配对 %.1f 份｜卖一合计 %.2f¢", o.Kind, o.Shares, o.RawSumAsk*100),
					fmt.Sprintf("含费总成本 $%.2f → 到期兑付 $%.2f｜净利 +$%.2f（每份 +%.2f¢，ROI %.2f%%）",
						o.TotalCost, o.Payout, o.NetProfit, o.NetPerUnit*100, o.ROI*100),
				})
			}
		}
	}
	if len(m.history) > 100 {
		m.history = m.history[:100]
	}
	m.mu.Unlock()
	return snap, nil
}

// Last 返回最近一次后台扫描快照与正收益套利历史。
func (m *PMArbMonitor) Last() (*PMArbSnapshot, []PMArbHit) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.last, append([]PMArbHit(nil), m.history...)
}
