package job

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/store"
)

// CryptoSyncer 定时把加密货币 K 线同步进本地库（7×24，独立于 A 股的每日调度与任务队列）。
// 首次按 history 配置回填，之后每次从本地最后一根往回重拉 2 根（覆盖可能的修正）。
type CryptoSyncer struct {
	store    *store.Store
	sources  []ds.KlineSource
	symbols  []string
	history  map[string]int // 周期 → 首次回填天数
	every    time.Duration
	now      func() time.Time
	logf     func(string, ...any)
	mu       sync.Mutex
	running  bool
	lastRun  time.Time
	lastLog  []string
	lastErrs int
}

// DefaultCryptoHistory 是各周期首次回填的天数（1d 约 8 年，1h 2 年，15m 半年）。
var DefaultCryptoHistory = map[string]int{"1d": 3000, "1h": 730, "15m": 180}

func NewCryptoSyncer(st *store.Store, sources []ds.KlineSource, symbols []string, history map[string]int, every time.Duration) *CryptoSyncer {
	if len(history) == 0 {
		history = DefaultCryptoHistory
	}
	if every <= 0 {
		every = 15 * time.Minute
	}
	return &CryptoSyncer{store: st, sources: sources, symbols: symbols, history: history, every: every, now: time.Now, logf: log.Printf}
}

// Configured 表示是否配置了数据源与品种。
func (c *CryptoSyncer) Configured() bool { return c != nil && len(c.sources) > 0 && len(c.symbols) > 0 }

// Symbols 返回配置的品种。
func (c *CryptoSyncer) Symbols() []string { return append([]string(nil), c.symbols...) }

// Intervals 返回同步的周期（按时长升序）。
func (c *CryptoSyncer) Intervals() []string {
	var out []string
	for iv := range c.history {
		if _, ok := ds.KlineIntervals[iv]; ok {
			out = append(out, iv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return ds.KlineIntervals[out[i]] < ds.KlineIntervals[out[j]] })
	return out
}

// Run 立即同步一次，之后按间隔循环，直到 ctx 结束。
func (c *CryptoSyncer) Run(ctx context.Context) {
	t := time.NewTicker(c.every)
	defer t.Stop()
	for {
		if err := c.SyncOnce(ctx); err != nil && !errors.Is(err, errBusySync) {
			c.logf("加密行情同步：%v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

var errBusySync = errors.New("加密行情同步正在进行")

// SyncOnce 同步全部品种与周期；已在同步时返回 errBusySync。
func (c *CryptoSyncer) SyncOnce(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errBusySync
	}
	c.running = true
	c.mu.Unlock()
	var lines []string
	errs := 0
	logf := func(f string, a ...any) {
		lines = append(lines, c.now().Format("15:04:05 ")+fmt.Sprintf(f, a...))
	}
	defer func() {
		c.mu.Lock()
		c.running, c.lastRun, c.lastLog, c.lastErrs = false, c.now(), lines, errs
		c.mu.Unlock()
	}()
	for _, sym := range c.symbols {
		for _, iv := range c.Intervals() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, src, err := c.syncOne(ctx, sym, iv)
			if err != nil {
				errs++
				logf("✗ %s %s：%v", sym, iv, err)
				continue
			}
			if n > 0 {
				logf("✓ %s %s：%d 根（%s）", sym, iv, n, src)
			}
		}
	}
	if errs > 0 {
		return fmt.Errorf("%d 项失败", errs)
	}
	return nil
}

func (c *CryptoSyncer) syncOne(ctx context.Context, sym, iv string) (int, string, error) {
	d := ds.KlineIntervals[iv]
	now := c.now().UnixMilli()
	last, err := c.store.KlineLast(ctx, sym, iv)
	if err != nil {
		return 0, "", err
	}
	from := now - int64(c.history[iv])*24*time.Hour.Milliseconds()
	if last > 0 {
		from = last - 2*d.Milliseconds()
	}
	from -= from % d.Milliseconds() // 对齐到周期边界
	var errs []error
	for _, s := range c.sources {
		ks, err := s.Klines(ctx, sym, iv, from, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
			continue
		}
		return len(ks), s.Name(), c.store.SaveKlines(context.WithoutCancel(ctx), sym, iv, s.Name(), ks)
	}
	return 0, "", errors.Join(errs...)
}

// Price 依次从数据源取最新价。
func (c *CryptoSyncer) Price(ctx context.Context, sym string) (float64, string, error) {
	var errs []error
	for _, s := range c.sources {
		p, err := s.Price(ctx, sym)
		if err == nil {
			return p, s.Name(), nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
	}
	return 0, "", errors.Join(errs...)
}

// CryptoStatus 是同步器状态，供 Web 展示。
type CryptoStatus struct {
	Running bool      `json:"running"`
	LastRun time.Time `json:"lastRun,omitzero"`
	Errors  int       `json:"errors"`
	Log     []string  `json:"log"`
	Every   string    `json:"every"`
}

func (c *CryptoSyncer) Status() CryptoStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CryptoStatus{Running: c.running, LastRun: c.lastRun, Errors: c.lastErrs, Log: append([]string{}, c.lastLog...), Every: c.every.String()}
}
