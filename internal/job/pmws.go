package job

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/ws"
)

// PMWSFeed 维护两条后台 WebSocket 连接（币安现货 bookTicker + Polymarket CLOB market 频道），
// 断线自动指数退避重连；盘口采集器优先读这里的新鲜实时数据，过期或断线时无缝回退到 REST。
type PMWSFeed struct {
	proxySpot string
	proxyPM   string
	logf      func(string, ...any)

	mu        sync.RWMutex
	symbols   []string
	tokens    []string
	spotOK    bool
	bookOK    bool
	spots     map[string]wsSpot
	books     map[string]ds.Book
	resubSpot chan struct{}
	resubBook chan struct{}
}

type wsSpot struct {
	p  float64
	at time.Time
}

func NewPMWSFeed(proxySpot, proxyPM string) *PMWSFeed {
	return &PMWSFeed{
		proxySpot: proxySpot, proxyPM: proxyPM, logf: log.Printf,
		spots: map[string]wsSpot{}, books: map[string]ds.Book{},
		resubSpot: make(chan struct{}, 1), resubBook: make(chan struct{}, 1),
	}
}

// SetTargets 更新需要订阅的现货品种与 Polymarket 代币列表；发生变化时通知后台重订。
func (f *PMWSFeed) SetTargets(symbols, tokens []string) {
	sort.Strings(symbols)
	sort.Strings(tokens)
	f.mu.Lock()
	spotChanged := !slicesEqual(f.symbols, symbols)
	bookChanged := !slicesEqual(f.tokens, tokens)
	if spotChanged {
		f.symbols = append([]string(nil), symbols...)
	}
	if bookChanged {
		f.tokens = append([]string(nil), tokens...)
	}
	f.mu.Unlock()
	if spotChanged {
		select {
		case f.resubSpot <- struct{}{}:
		default:
		}
	}
	if bookChanged {
		select {
		case f.resubBook <- struct{}{}:
		default:
		}
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FreshSpot 返回不超过 maxAge 的实时现价。
func (f *PMWSFeed) FreshSpot(symbol string, now time.Time, maxAge time.Duration) (float64, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s, ok := f.spots[symbol]
	if !ok || s.p <= 0 || now.Sub(s.at) > maxAge {
		return 0, false
	}
	return s.p, true
}

// FreshBook 返回不超过 maxAge 的实时盘口快照。
func (f *PMWSFeed) FreshBook(token string, now time.Time, maxAge time.Duration) (ds.Book, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	b, ok := f.books[token]
	if !ok || b.Time.IsZero() || now.Sub(b.Time) > maxAge {
		return ds.Book{}, false
	}
	return b, true
}

func (f *PMWSFeed) Status() (spotOK, bookOK bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.spotOK, f.bookOK
}

// Run 启动现货与盘口两条 WebSocket 维护循环，直到 ctx 取消。
func (f *PMWSFeed) Run(ctx context.Context) {
	go f.runSpotLoop(ctx)
	go f.runBookLoop(ctx)
}

func (f *PMWSFeed) runSpotLoop(ctx context.Context) {
	backoff := 2 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		f.mu.RLock()
		syms := append([]string(nil), f.symbols...)
		f.mu.RUnlock()
		if len(syms) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-f.resubSpot:
			case <-time.After(3 * time.Second):
			}
			continue
		}
		streams := make([]string, 0, len(syms))
		for _, s := range syms {
			streams = append(streams, strings.ToLower(s)+"@bookTicker")
		}
		u := "wss://stream.binance.com:9443/stream?streams=" + strings.Join(streams, "/")
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		conn, err := ws.Dial(dctx, u, f.proxySpot)
		cancel()
		if err != nil {
			f.setSpotOK(false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = 2 * time.Second
		f.setSpotOK(true)
		f.readSpotConn(ctx, conn)
		conn.Close()
		f.setSpotOK(false)
	}
}

func (f *PMWSFeed) readSpotConn(ctx context.Context, conn *ws.Conn) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-f.resubSpot:
			conn.Close()
		case <-done:
		}
	}()
	for {
		raw, err := conn.ReadMessage(30 * time.Second)
		if err != nil {
			return
		}
		var env struct {
			Data struct {
				S string `json:"s"`
				B string `json:"b"`
				A string `json:"a"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Data.S == "" {
			continue
		}
		b, err1 := strconv.ParseFloat(env.Data.B, 64)
		a, err2 := strconv.ParseFloat(env.Data.A, 64)
		if err1 != nil || err2 != nil || b <= 0 || a <= 0 {
			continue
		}
		mid := (b + a) / 2
		f.mu.Lock()
		f.spots[env.Data.S] = wsSpot{p: mid, at: time.Now()}
		f.mu.Unlock()
	}
}

func (f *PMWSFeed) runBookLoop(ctx context.Context) {
	backoff := 2 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		f.mu.RLock()
		toks := append([]string(nil), f.tokens...)
		f.mu.RUnlock()
		if len(toks) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-f.resubBook:
			case <-time.After(3 * time.Second):
			}
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		conn, err := ws.Dial(dctx, "wss://ws-subscriptions-clob.polymarket.com/ws/market", f.proxyPM)
		cancel()
		if err != nil {
			f.setBookOK(false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		sub, _ := json.Marshal(map[string]any{"assets_ids": toks, "type": "market"})
		if err := conn.WriteText(sub); err != nil {
			conn.Close()
			continue
		}
		backoff = 2 * time.Second
		f.setBookOK(true)
		f.readBookConn(ctx, conn)
		conn.Close()
		f.setBookOK(false)
	}
}

func (f *PMWSFeed) readBookConn(ctx context.Context, conn *ws.Conn) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				conn.Close()
				return
			case <-f.resubBook:
				conn.Close()
				return
			case <-ping.C:
				_ = conn.WriteText([]byte("PING"))
			case <-done:
				return
			}
		}
	}()
	for {
		raw, err := conn.ReadMessage(45 * time.Second)
		if err != nil {
			return
		}
		s := strings.TrimSpace(string(raw))
		if s == "" || s == "PONG" {
			continue
		}
		// CLOB WS 可能发单条对象或数组
		var items []json.RawMessage
		if strings.HasPrefix(s, "[") {
			if json.Unmarshal(raw, &items) != nil {
				continue
			}
		} else {
			items = []json.RawMessage{raw}
		}
		now := time.Now()
		for _, it := range items {
			var ev struct {
				EventType string `json:"event_type"`
				AssetID   string `json:"asset_id"`
				Hash      string `json:"hash"`
				Bids      []struct {
					Price string `json:"price"`
					Size  string `json:"size"`
				} `json:"bids"`
				Asks []struct {
					Price string `json:"price"`
					Size  string `json:"size"`
				} `json:"asks"`
			}
			if json.Unmarshal(it, &ev) != nil || ev.EventType != "book" || ev.AssetID == "" {
				continue
			}
			b := ds.Book{Token: ev.AssetID, Time: now, Hash: ev.Hash}
			for _, l := range ev.Bids {
				p, e1 := strconv.ParseFloat(l.Price, 64)
				sz, e2 := strconv.ParseFloat(l.Size, 64)
				if e1 == nil && e2 == nil && sz > 0 {
					b.Bids = append(b.Bids, ds.BookLevel{Price: p, Size: sz})
				}
			}
			for _, l := range ev.Asks {
				p, e1 := strconv.ParseFloat(l.Price, 64)
				sz, e2 := strconv.ParseFloat(l.Size, 64)
				if e1 == nil && e2 == nil && sz > 0 {
					b.Asks = append(b.Asks, ds.BookLevel{Price: p, Size: sz})
				}
			}
			sort.Slice(b.Bids, func(i, j int) bool { return b.Bids[i].Price > b.Bids[j].Price })
			sort.Slice(b.Asks, func(i, j int) bool { return b.Asks[i].Price < b.Asks[j].Price })
			if b.Hash == "" {
				b.Hash = fmt.Sprintf("ws-%d-%d", len(b.Bids), len(b.Asks))
			}
			f.mu.Lock()
			f.books[ev.AssetID] = b
			f.mu.Unlock()
		}
	}
}

func (f *PMWSFeed) setSpotOK(ok bool) {
	f.mu.Lock()
	f.spotOK = ok
	f.mu.Unlock()
}

func (f *PMWSFeed) setBookOK(ok bool) {
	f.mu.Lock()
	f.bookOK = ok
	f.mu.Unlock()
}
