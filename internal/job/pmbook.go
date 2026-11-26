package job

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/store"
)

// PMBookCollector 定时采集短期涨跌盘系列“当前 + 下一个”市场的盘口快照与同时刻的现货价，存进本地。
// 历史盘口无法回填，模拟盘的成交模型和短期盘定价都靠它：
//   - 盘口只存第一个结果代币（Up）的前 N 档：Down 的盘口是它的镜像；
//   - 订单簿摘要（hash）没变时不存；
//   - 现货价（如 BTCUSDT）每轮都存，用来估算计价起始时刻的预言机价格、校准 USDT 基差。
type PMBookCollector struct {
	store  *store.Store
	src    ds.PredictionSource
	spot   func(ctx context.Context, symbol string) (float64, string, error) // 为 nil 时不采现货价
	series []string
	every  time.Duration
	levels int
	keep   time.Duration
	now    func() time.Time
	logf   func(string, ...any)

	refreshEvery time.Duration // 目标市场列表多久从本地库刷新一次
	lastHash     map[string]string
	keys         map[string]int64

	mu      sync.Mutex
	running bool
	targets []PMBookTarget
	books   int64 // 本进程写入的盘口快照数
	spots   int64
	ticks   int64
	lastRun time.Time
	lastErr string
	errAt   time.Time
	errs    int // 连续失败次数
}

// PMBookTarget 是一个正在采集的市场。
type PMBookTarget struct {
	Series string    `json:"series"`
	Slug   string    `json:"slug"`
	Token  string    `json:"token"`
	Symbol string    `json:"symbol,omitempty"` // 对应的现货品种
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

func NewPMBookCollector(st *store.Store, src ds.PredictionSource, series []string, every time.Duration, levels, keepDays int) *PMBookCollector {
	if every <= 0 {
		every = 2500 * time.Millisecond
	}
	if levels <= 0 {
		levels = 10
	}
	if keepDays <= 0 {
		keepDays = 14
	}
	return &PMBookCollector{store: st, src: src, series: series, every: every, levels: levels, keep: time.Duration(keepDays) * 24 * time.Hour,
		now: time.Now, logf: log.Printf, refreshEvery: 30 * time.Second, lastHash: map[string]string{}, keys: map[string]int64{}}
}

// WithSpot 设置现货价来源（如加密行情同步器的 Price）。
func (c *PMBookCollector) WithSpot(f func(ctx context.Context, symbol string) (float64, string, error)) *PMBookCollector {
	c.spot = f
	return c
}

// Configured 表示是否可以采集。
func (c *PMBookCollector) Configured() bool { return c != nil && c.src != nil && len(c.series) > 0 }

// SpotSymbol 由系列名推出现货品种：btc-up-or-down-15m → BTCUSDT。
func SpotSymbol(series string) string {
	asset, _, _ := strings.Cut(series, "-")
	if asset == "" {
		return ""
	}
	return strings.ToUpper(asset) + "USDT"
}

// Run 按间隔采集，直到 ctx 结束。连续失败时按指数退避（最长 1 分钟）。
func (c *PMBookCollector) Run(ctx context.Context) {
	c.mu.Lock()
	c.running = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()
	t := time.NewTicker(c.every)
	defer t.Stop()
	var refreshed, pruned, wait time.Time
	for {
		now := c.now()
		if now.Sub(refreshed) >= c.refreshEvery {
			if err := c.refreshTargets(ctx); err != nil {
				c.fail(err)
			} else {
				refreshed = now
			}
		}
		if now.Sub(pruned) >= time.Hour {
			if n, err := c.store.PrunePMBooks(context.WithoutCancel(ctx), now.Add(-c.keep).UnixMilli()); err != nil {
				c.fail(err)
			} else {
				pruned = now
				if n > 0 {
					c.logf("盘口采集：清理 %d 条过期快照", n)
				}
			}
		}
		if !now.Before(wait) {
			if err := c.Tick(ctx); err != nil {
				if ctx.Err() != nil { // 正在退出：被取消的请求不算失败
					return
				}
				c.fail(err)
				c.mu.Lock()
				backoff := min(c.every<<min(c.errs, 5), time.Minute)
				c.mu.Unlock()
				wait = now.Add(backoff)
			} else {
				wait = time.Time{}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *PMBookCollector) fail(err error) {
	if err == nil || err == context.Canceled {
		return
	}
	c.mu.Lock()
	c.errs++
	c.lastErr, c.errAt = err.Error(), c.now()
	n := c.errs
	c.mu.Unlock()
	if n == 1 || n%100 == 0 {
		c.logf("盘口采集：%v（连续 %d 次）", err, n)
	}
}

// refreshTargets 从本地库取每个系列未结束的前两个市场（当前进行中的和下一个）。
func (c *PMBookCollector) refreshTargets(ctx context.Context) error {
	open := false
	var out []PMBookTarget
	for _, s := range c.series {
		evs, err := c.store.PMEvents(ctx, store.PMEventQuery{Series: s, Closed: &open, From: c.now().Unix(), Asc: true, Limit: 2})
		if err != nil {
			return err
		}
		for _, e := range evs {
			for _, m := range e.Markets {
				if len(m.Tokens) == 0 {
					continue
				}
				out = append(out, PMBookTarget{Series: s, Slug: e.Slug, Token: m.Tokens[0], Symbol: SpotSymbol(s), Start: m.Start, End: m.End})
			}
		}
	}
	keep := map[string]bool{}
	for _, t := range out {
		keep[t.Token] = true
	}
	for tok := range c.lastHash { // 只留当前目标的缓存（refreshTargets 与 Tick 在同一个 goroutine 里调用）
		if !keep[tok] {
			delete(c.lastHash, tok)
			delete(c.keys, tok)
		}
	}
	c.mu.Lock()
	c.targets = out
	c.mu.Unlock()
	return nil
}

// Tick 采集一轮：一次请求取全部目标的盘口，另取各现货价，在一个事务里写入。
func (c *PMBookCollector) Tick(ctx context.Context) error {
	c.mu.Lock()
	targets := append([]PMBookTarget(nil), c.targets...)
	c.mu.Unlock()
	if len(targets) == 0 {
		return nil
	}
	now := c.now()
	tokens := make([]string, 0, len(targets))
	symbols := map[string]bool{}
	for _, t := range targets {
		tokens = append(tokens, t.Token)
		if t.Symbol != "" {
			symbols[t.Symbol] = true
		}
	}
	books, err := c.src.Books(ctx, tokens)
	if err != nil {
		return fmt.Errorf("订单簿：%w", err)
	}
	var rows []store.PMBookRow
	var hashes [][2]string
	for _, b := range books {
		if b.Hash != "" && b.Hash == c.lastHash[b.Token] {
			continue
		}
		k, ok := c.keys[b.Token]
		if !ok {
			if k, err = c.store.PMBookKey(context.WithoutCancel(ctx), b.Token); err != nil {
				return err
			}
			c.keys[b.Token] = k
		}
		t := b.Time.UnixMilli()
		if b.Time.IsZero() || t <= 0 {
			t = now.UnixMilli()
		}
		rows = append(rows, store.PMBookRow{Key: k, T: t, Bids: b.Bids[:min(len(b.Bids), c.levels)], Asks: b.Asks[:min(len(b.Asks), c.levels)]})
		hashes = append(hashes, [2]string{b.Token, b.Hash})
	}
	var spots []store.PMSpot
	if c.spot != nil {
		names := make([]string, 0, len(symbols))
		for s := range symbols {
			names = append(names, s)
		}
		sort.Strings(names)
		for _, s := range names {
			p, _, err := c.spot(ctx, s)
			if err != nil {
				return fmt.Errorf("现货价 %s：%w", s, err)
			}
			spots = append(spots, store.PMSpot{Symbol: s, T: now.UnixMilli(), P: p})
		}
	}
	if err := c.store.SavePMSnapshot(context.WithoutCancel(ctx), rows, spots); err != nil {
		return err
	}
	for _, h := range hashes {
		c.lastHash[h[0]] = h[1]
	}
	c.mu.Lock()
	c.books += int64(len(rows))
	c.spots += int64(len(spots))
	c.ticks++
	c.lastRun, c.errs = now, 0
	c.mu.Unlock()
	return nil
}

// PMBookStatus 是采集器状态。
type PMBookStatus struct {
	Running bool           `json:"running"`
	Every   string         `json:"every"`
	Targets []PMBookTarget `json:"targets"`
	Ticks   int64          `json:"ticks"`
	Books   int64          `json:"books"` // 本进程启动以来写入的快照
	Spots   int64          `json:"spots"`
	LastRun time.Time      `json:"lastRun,omitzero"`
	Errors  int            `json:"errors"` // 连续失败次数
	LastErr string         `json:"lastErr,omitempty"`
	ErrAt   time.Time      `json:"errAt,omitzero"`
}

func (c *PMBookCollector) Status() PMBookStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return PMBookStatus{Running: c.running, Every: c.every.String(), Targets: append([]PMBookTarget{}, c.targets...), Ticks: c.ticks,
		Books: c.books, Spots: c.spots, LastRun: c.lastRun, Errors: c.errs, LastErr: c.lastErr, ErrAt: c.errAt}
}
