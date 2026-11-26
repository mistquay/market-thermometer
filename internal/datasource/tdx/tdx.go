// Package tdx 是通达信行情协议的个股 / 场内基金日线插件（TCP 7709，经 github.com/bensema/gotdx）。
//
// 实测（2026-11-26）：
//   - 每页最多稳定取 400 根日线（800 根时库解析失败）；600000 全历史 6,435 根约 0.9 秒；
//   - 同一连接连续 838 次请求、平均 69 毫秒，没有被断开；
//   - 已退市股票取不到日线（除权记录还在）：退市股的历史只能来自其他来源（如 stockdb）或本地已有数据；
//   - 场内基金从上市日起都有，除权事件里有 ETF 分红与扩缩股（腾讯没有）；
//   - 2005 年以来 600000 的收盘价与 stockdb 一致（只有 stockdb 未校对的最近两天不同），按除权事件算出的乘数
//     与 stockdb 一致（stockdb 只保留 3 位小数）；
//   - 部分深市老股票 1991 年前后的页面日期解析失败（库的问题），我们只取 2005 年以后，不受影响。
//
// 协议是逆向得来的，数据没有使用授权，服务器地址可能变化：只作为可替换的数据来源之一。
package tdx

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bensema/gotdx"
	"github.com/bensema/gotdx/proto"
	"github.com/bensema/gotdx/types"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/plugin"
)

const (
	Name     = "tdx"
	pageSize = 400 // 每页日线根数（800 时库解析失败）
)

func init() {
	plugin.MustRegister(plugin.Manifest{Name: Name, Kind: plugin.KindStock, Label: "通达信行情",
		Description:  "通达信行情协议（TCP 7709）：证券清单与名称、不复权日线、开高低与成交、除权除息（含 ETF 分红）；不含已退市股票",
		Capabilities: []string{"list", "bars", "ext", "names"},
		Config: []plugin.Field{
			{Key: "hosts", Label: "服务器", Help: "逗号分隔的 IP:端口，空为内置列表"},
			{Key: "connections", Label: "连接数", Type: "number", Help: "默认 4"},
		}})
	ds.RegisterStock(Name, func(env ds.Env) (ds.StockSource, error) {
		s := &Source{now: env.Now, hosts: gotdx.MainHostAddresses(), conns: 4}
		if v := optString(env.Options["hosts"]); v != "" {
			s.hosts = nil
			for h := range strings.SplitSeq(v, ",") {
				if h = strings.TrimSpace(h); h != "" {
					s.hosts = append(s.hosts, h)
				}
			}
		}
		if n, ok := env.Options["connections"].(int); ok && n > 0 && n <= 16 {
			s.conns = n
		}
		if len(s.hosts) == 0 {
			return nil, fmt.Errorf("tdx: 没有可用的服务器地址")
		}
		s.pool = make(chan *gotdx.Client, s.conns)
		return s, nil
	})
}

func optString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, p := range x {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, ",")
	}
	return ""
}

type Source struct {
	hosts []string
	conns int
	now   func() time.Time
	pool  chan *gotdx.Client // 空闲连接；出错的连接直接丢弃

	mu    sync.Mutex
	names map[string]string
	ext   map[string][]ds.StockExt
}

func (s *Source) Name() string { return Name }

// client 取一个空闲连接，没有则新建（随机选起始服务器，分散压力）。
func (s *Source) client() (*gotdx.Client, error) {
	select {
	case c := <-s.pool:
		return c, nil
	default:
	}
	hosts := append([]string(nil), s.hosts...)
	rand.Shuffle(len(hosts), func(i, j int) { hosts[i], hosts[j] = hosts[j], hosts[i] })
	c := gotdx.New(gotdx.WithTCPAddress(hosts[0]), gotdx.WithTCPAddressPool(hosts[1:]...), gotdx.WithTimeoutSec(10))
	if _, err := c.Connect(); err != nil {
		_ = c.Disconnect()
		return nil, fmt.Errorf("tdx 连接：%w", err)
	}
	return c, nil
}

// release 归还连接；出过错的连接不再复用。
func (s *Source) release(c *gotdx.Client, err error) {
	if err != nil {
		_ = c.Disconnect()
		return
	}
	select {
	case s.pool <- c:
	default:
		_ = c.Disconnect()
	}
}

func market(code string) uint8 {
	if ds.MarketPrefix(code) == "sh" {
		return types.MarketSH.Uint8()
	}
	return types.MarketSZ.Uint8()
}

// ListStocks 返回沪深两市在市的 A 股与场内基金（不含已退市），顺带记下名称。
func (s *Source) ListStocks(ctx context.Context) (out []ds.Stock, err error) {
	c, err := s.client()
	if err != nil {
		return nil, err
	}
	defer func() { s.release(c, err) }()
	names := map[string]string{}
	for _, m := range []uint8{types.MarketSZ.Uint8(), types.MarketSH.Uint8()} {
		for start := uint32(0); ; {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			r, err := c.GetSecurityListRange(m, start, types.DefaultSecurityListCount)
			if err != nil {
				return nil, fmt.Errorf("tdx 证券清单：%w", err)
			}
			for _, x := range r.List {
				code := strings.TrimSpace(x.Code)
				// 代码格式与市场要对得上（深市列表里也有沪市代码段的其他品种）
				if k := ds.KindOf(code); k != "" && (ds.MarketPrefix(code) == "sh") == (m == types.MarketSH.Uint8()) {
					out = append(out, ds.Stock{Code: code, Kind: k})
					if n := strings.TrimSpace(strings.TrimRight(x.Name, "\x00")); n != "" {
						names[code] = n
					}
				}
			}
			if len(r.List) == 0 || len(r.List) < int(types.DefaultSecurityListCount) {
				break
			}
			start += uint32(len(r.List))
		}
	}
	if len(out) < 1000 {
		return nil, fmt.Errorf("tdx 证券清单只有 %d 只，疑似服务器异常", len(out))
	}
	s.mu.Lock()
	s.names = names
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

// TakeNames 实现 ds.NameCollector。
func (s *Source) TakeNames() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.names
	s.names = nil
	return out
}

// TakeExt 实现 ds.ExtCollector。
func (s *Source) TakeExt() map[string][]ds.StockExt {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.ext
	s.ext = nil
	return out
}

// Bars 返回 [from, to] 内已定稿的不复权日线与除权乘数，顺带记下开高低与成交（扩展字段）。
func (s *Source) Bars(ctx context.Context, code, from, to string) (bars []ds.StockBar, err error) {
	if ds.KindOf(code) == "" {
		return nil, fmt.Errorf("tdx: bad code %q", code)
	}
	if from == "" {
		from = "1990-01-01"
	}
	if to == "" {
		to = "9999-12-31"
	}
	c, err := s.client()
	if err != nil {
		return nil, err
	}
	defer func() { s.release(c, err) }()
	raw, err := s.daily(ctx, c, code, from)
	if err != nil {
		return nil, fmt.Errorf("tdx %s：%w", code, err)
	}
	if len(raw) == 0 {
		return nil, nil // 已退市或尚未上市：没有日线
	}
	x, err := c.GetXDXRInfo(market(code), code)
	if err != nil {
		return nil, fmt.Errorf("tdx %s 除权：%w", code, err)
	}
	all, ext := build(raw, events(x.List))
	last := ds.LatestFinalDate(s.now())
	var outExt []ds.StockExt
	for i, b := range all {
		if b.Date >= from && b.Date <= to && b.Date <= last {
			bars = append(bars, b)
			outExt = append(outExt, ext[i])
		}
	}
	if len(outExt) > 0 {
		s.mu.Lock()
		if s.ext == nil {
			s.ext = map[string][]ds.StockExt{}
		}
		s.ext[code] = outExt
		s.mu.Unlock()
	}
	return bars, nil
}

// daily 从最新一页向前翻，直到覆盖 from 之前的一根（计算 from 当天的除权乘数要用前一日收盘）。
func (s *Source) daily(ctx context.Context, c *gotdx.Client, code, from string) ([]proto.SecurityBar, error) {
	var all []proto.SecurityBar
	for start := uint16(0); ; start += pageSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := c.GetKLine(types.KLINE_TYPE_DAILY, market(code), code, start, pageSize, 1, types.AdjustNone)
		if err != nil {
			// 库解析不了上世纪 90 年代初部分页面的日期；已取到的部分覆盖了 from 时照常返回
			if len(all) > 0 && strings.Contains(err.Error(), "invalid kline datetime") && dateOf(all[0]) < from {
				break
			}
			return nil, err
		}
		all = append(append([]proto.SecurityBar{}, r.List...), all...)
		if len(r.List) < pageSize || (len(all) > 0 && dateOf(all[0]) < from) || start > 65535-2*pageSize {
			break
		}
	}
	return all, nil
}

func dateOf(b proto.SecurityBar) string { return b.DateTime.Format("2006-01-02") }

// event 是一次影响价格的除权事件（数值均为每 10 股口径，与通达信一致）。
type event struct {
	date                    string
	fenhong, peigujia       float64 // 每 10 股派现（元）、配股价（元）
	songzhuan, peigu, suogu float64 // 每 10 股送转、每 10 股配股、扩缩股比例（如 1.1456 表示每份变为 1.1456 份）
}

func f32(p *float32) float64 {
	if p == nil {
		return 0
	}
	return float64(*p)
}

// events 取出影响价格的事件：类别 1 除权除息、类别 11 扩缩股（其余为股本变动，不影响价格）。
func events(items []proto.XDXRItem) []event {
	var out []event
	for _, it := range items {
		e := event{date: it.Date.Format("2006-01-02")}
		switch it.Category {
		case 1:
			e.fenhong, e.peigujia, e.songzhuan, e.peigu = f32(it.Fenhong), f32(it.Peigujia), f32(it.Songzhuangu), f32(it.Peigu)
		case 11:
			e.suogu = f32(it.Suogu)
		default:
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].date < out[j].date })
	return out
}

// mult 按交易所除权公式计算乘数：参考价 = (前收 − 每股派现 + 配股价 × 每股配股) ÷ (1 + 每股送转 + 每股配股)，
// 乘数 = 前收 ÷ 参考价；扩缩股的乘数即比例。无法计算时返回 1。
func (e event) mult(prevClose float64) float64 {
	if e.suogu > 0 {
		return e.suogu
	}
	ref := (prevClose - e.fenhong/10 + e.peigujia*e.peigu/10) / (1 + (e.songzhuan+e.peigu)/10)
	if ref <= 0 || prevClose <= 0 {
		return 1
	}
	return prevClose / ref
}

// build 去掉停牌（无成交）的日线，按日期去重排序，把每个除权事件计入其后第一个交易日的乘数。
func build(raw []proto.SecurityBar, evs []event) ([]ds.StockBar, []ds.StockExt) {
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].DateTime.Before(raw[j].DateTime) })
	bars := make([]ds.StockBar, 0, len(raw))
	ext := make([]ds.StockExt, 0, len(raw))
	k := 0
	for _, r := range raw {
		d := dateOf(r)
		if r.Close <= 0 || r.Vol <= 0 || (len(bars) > 0 && d <= bars[len(bars)-1].Date) {
			continue
		}
		m := 1.0
		for ; k < len(evs) && evs[k].date <= d; k++ {
			if len(bars) > 0 { // 第一根之前的事件（如上市前的扩缩股）没有前收，忽略
				m *= evs[k].mult(bars[len(bars)-1].Close)
			}
		}
		bars = append(bars, ds.StockBar{Date: d, Close: r.Close, Mult: round6(m)})
		ext = append(ext, ds.StockExt{Date: d, Open: r.Open, High: r.High, Low: r.Low, Volume: r.Vol, Amount: r.Amount})
	}
	return bars, ext
}

func round6(x float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 6, 64), 64)
	return v
}
