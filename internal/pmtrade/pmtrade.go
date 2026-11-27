// Package pmtrade 是预测市场交易的编排层：Broker 接口（下单、账户状态），模拟盘是第一个实现。
// 以后海外服务器上的实盘插件实现同一个接口，策略层不用改。风控（熔断、上限）由这里强制执行。
package pmtrade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/pmsim"
	"market-thermometer/internal/store"
)

// HaltKey 是全局熔断开关在 meta 表里的键：值为 "1" 时拒绝一切新委托（模拟与实盘）。
const HaltKey = "pm.halt"

// OrderReq 是下单请求。
type OrderReq struct {
	Account int64      `json:"account"`
	Slug    string     `json:"slug"`
	Cond    string     `json:"conditionId,omitempty"` // 多市场事件中的市场；为空取第一个
	Outcome int        `json:"outcome"`               // 结果下标（0 = Up / Yes）
	Side    pmsim.Side `json:"side"`
	Shares  float64    `json:"shares,omitempty"` // 按股数（卖出必须用股数）
	Amount  float64    `json:"amount,omitempty"` // 按金额买（USDC，不含手续费）
	Limit   float64    `json:"limit,omitempty"`  // 买：最高价；卖：最低价；0 不限
	Source  string     `json:"source,omitempty"` // manual / strategy:名称
	DryRun  bool       `json:"dryRun,omitempty"` // 只试算不入账
}

// AccountView 是账户状态（含估值）。
type AccountView struct {
	Account    store.PMSimAccount `json:"account"`
	State      pmsim.State        `json:"state"`
	Equity     float64            `json:"equity"`     // 现金 + 未结算持仓按买一估值
	Unrealized float64            `json:"unrealized"` // 未结算持仓估值 − 成本
	Orders     []pmsim.Order      `json:"orders"`     // 最近的在前
	GlobalHalt bool               `json:"globalHalt"`
}

// Broker 是交易接口。
type Broker interface {
	Place(ctx context.Context, req OrderReq) (pmsim.Order, error)
	Account(ctx context.Context, id int64) (AccountView, error)
}

// ErrInvalid 表示请求本身无效（与风控拒单不同：风控拒单会作为 rejected 委托记账）。
var ErrInvalid = errors.New("无效请求")

// Sim 是模拟盘 Broker：按实时（或注入的）盘口逐档吃单撮合。
type Sim struct {
	mu    sync.Mutex // 下单串行化：避免并发下单同时通过现金与上限检查
	store *store.Store
	src   ds.PredictionSource
	now   func() time.Time
	// BookOf 返回第一个结果代币的盘口；默认实时取。策略回测可以换成“决策时刻之后的第一张本地快照”。
	BookOf func(ctx context.Context, firstToken string) (ds.Book, error)
}

func NewSim(st *store.Store, src ds.PredictionSource) *Sim {
	s := &Sim{store: st, src: src, now: time.Now}
	s.BookOf = func(ctx context.Context, tok string) (ds.Book, error) { return src.Book(ctx, tok) }
	return s
}

func invalid(f string, a ...any) error {
	return fmt.Errorf("%w：%s", ErrInvalid, fmt.Sprintf(f, a...))
}

// market 找到事件与市场：优先用本地（同步器与关注列表会刷新它），本地没有时才实时取并存入本地。
func (s *Sim) market(ctx context.Context, slug, cond string) (ds.PMEvent, ds.PMMarket, error) {
	ev, err := s.store.PMEventBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) && s.src != nil {
		evs, ferr := s.src.Events(ctx, ds.PMEventQuery{Slug: slug, Limit: 1})
		if ferr == nil && len(evs) == 0 {
			ferr = errors.New("事件不存在")
		}
		if ferr != nil {
			return ev, ds.PMMarket{}, invalid("%v", ferr)
		}
		ev, err = evs[0], s.store.SavePMEvents(context.WithoutCancel(ctx), evs[:1], s.now())
	}
	if err != nil {
		return ev, ds.PMMarket{}, err
	}
	for _, m := range ev.Markets {
		if cond == "" || m.ConditionID == cond {
			return ev, m, nil
		}
	}
	return ev, ds.PMMarket{}, invalid("事件中没有这个市场")
}

func (s *Sim) Place(ctx context.Context, req OrderReq) (pmsim.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acct, err := s.store.PMSimAccountByID(ctx, req.Account)
	if errors.Is(err, sql.ErrNoRows) {
		return pmsim.Order{}, invalid("账户不存在")
	} else if err != nil {
		return pmsim.Order{}, err
	}
	if req.Side != pmsim.Buy && req.Side != pmsim.Sell {
		return pmsim.Order{}, invalid("方向应为 buy 或 sell")
	}
	if req.Side == pmsim.Sell && req.Shares <= 0 {
		return pmsim.Order{}, invalid("卖出请填写股数")
	}
	if req.Side == pmsim.Buy && req.Shares <= 0 && req.Amount <= 0 {
		return pmsim.Order{}, invalid("请填写股数或金额")
	}
	if req.Limit < 0 || req.Limit >= 1 {
		return pmsim.Order{}, invalid("限价应在 0–1 之间")
	}
	ev, m, err := s.market(ctx, req.Slug, req.Cond)
	if err != nil {
		return pmsim.Order{}, err
	}
	if len(m.Tokens) < 2 || req.Outcome < 0 || req.Outcome >= len(m.Tokens) || req.Outcome >= len(m.Outcomes) {
		return pmsim.Order{}, invalid("结果下标无效（只支持二元市场）")
	}
	src := req.Source
	if src == "" {
		src = "manual"
	}
	now := s.now()
	o := pmsim.Order{Account: acct.ID, Time: now, Slug: ev.Slug, Cond: m.ConditionID, Token: m.Tokens[req.Outcome], Outcome: req.Outcome,
		OutcomeName: m.Outcomes[req.Outcome], Question: m.Question, Side: req.Side, ReqShares: req.Shares, ReqAmount: req.Amount, Limit: req.Limit, Source: src}
	if req.Side == pmsim.Buy && req.Shares > 0 {
		o.ReqAmount = 0 // 同时给了股数与金额时按股数
	}
	reject := func(reason string) (pmsim.Order, error) {
		o.Status, o.Reason = pmsim.StatusRejected, reason
		return s.save(ctx, o, req.DryRun)
	}

	// 账户状态：回放委托 + 结算
	view, err := s.state(ctx, acct, false)
	if err != nil {
		return pmsim.Order{}, err
	}
	halt, err := s.store.Meta(ctx, HaltKey)
	if err != nil {
		return pmsim.Order{}, err
	}
	in := pmsim.CheckInput{Side: req.Side, Shares: req.Shares, Cash: view.State.Cash, GlobalHalt: halt == "1"}
	targetAsset := slugAsset(ev.Slug)
	for _, p := range view.State.Positions {
		if p.Settled {
			continue
		}
		if p.Cond == m.ConditionID {
			in.MarketCost += p.Cost
		}
		if targetAsset != "" && slugAsset(p.Slug) == targetAsset {
			in.AssetCost += p.Cost
		}
		if p.Token == o.Token {
			in.Held = p.Shares
		}
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, x := range view.Orders {
		if x.Side == pmsim.Buy && x.Status != pmsim.StatusRejected && !x.Time.Before(day) {
			in.DailyBought += x.Fill.Gross
		}
	}
	// 熔断与卖空在撮合前就能判断
	if err := pmsim.Check(acct.Params, pmsim.CheckInput{Side: req.Side, Shares: in.Shares, Held: in.Held, Cash: 1e18, GlobalHalt: in.GlobalHalt}); err != nil {
		return reject(err.Error())
	}
	switch {
	case m.Closed || (!m.End.IsZero() && !now.Before(m.End)):
		return reject("市场已结束")
	}

	book, err := s.BookOf(ctx, m.Tokens[0])
	if err != nil {
		return pmsim.Order{}, fmt.Errorf("取盘口：%w", err)
	}
	o.BookTime = book.Time
	ob := pmsim.OutcomeBook(book, req.Outcome)
	o.Fill = pmsim.Match(ob, req.Side, req.Shares, o.ReqAmount, req.Limit, pmsim.Fee{Rate: m.FeeRate, Exp: m.FeeExp})
	switch {
	case o.Fill.Shares <= 0:
		o.Fill = pmsim.Fill{}
		return reject("盘口没有可成交的量（或都超出限价）")
	case m.MinSize > 0 && o.Fill.Shares < m.MinSize:
		reason := fmt.Sprintf("可成交 %.2f 股，低于最小下单 %.0f 股", o.Fill.Shares, m.MinSize)
		o.Fill = pmsim.Fill{}
		return reject(reason)
	}
	in.Amount = o.Fill.Gross
	if err := pmsim.Check(acct.Params, in); err != nil {
		o.Fill = pmsim.Fill{}
		return reject(err.Error())
	}
	if req.Side == pmsim.Buy && o.Fill.Gross+o.Fill.Fee > view.State.Cash+1e-9 {
		need := o.Fill.Gross + o.Fill.Fee
		o.Fill = pmsim.Fill{}
		return reject(fmt.Sprintf("现金不足：需要 %.2f（含手续费），现金 %.2f", need, view.State.Cash))
	}
	if !req.DryRun {
		// 不在同步系列里的事件（如搜索来的）：自动关注，让同步器刷新它的结果，持仓才能结算
		if _, through, err := s.store.PMSeriesSync(ctx, ev.Series); err != nil {
			return pmsim.Order{}, err
		} else if ev.Series == "" || through == 0 {
			if err := s.store.WatchPM(context.WithoutCancel(ctx), ev.Slug, now); err != nil {
				return pmsim.Order{}, err
			}
		}
	}
	// 吃不完的部分取消（盘口深度不够或超出限价）：按股数时差 0.01 股以上、按金额时差 1 美分以上算部分成交
	o.Status = pmsim.StatusFilled
	if (o.ReqAmount > 0 && o.Fill.Gross < o.ReqAmount-0.01) || (o.ReqAmount == 0 && o.Fill.Shares < req.Shares-0.005) {
		o.Status = pmsim.StatusPartial
	}
	return s.save(ctx, o, req.DryRun)
}

func (s *Sim) save(ctx context.Context, o pmsim.Order, dry bool) (pmsim.Order, error) {
	if dry {
		return o, nil
	}
	id, err := s.store.AddPMSimOrder(context.WithoutCancel(ctx), o)
	o.ID = id
	return o, err
}

func (s *Sim) Account(ctx context.Context, id int64) (AccountView, error) {
	acct, err := s.store.PMSimAccountByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountView{}, invalid("账户不存在")
	} else if err != nil {
		return AccountView{}, err
	}
	return s.state(ctx, acct, true)
}

// state 回放账户；mark 为 true 时用实时盘口给未结算持仓估值（买一）。
func (s *Sim) state(ctx context.Context, acct store.PMSimAccount, mark bool) (AccountView, error) {
	orders, err := s.store.PMSimOrders(ctx, acct.ID)
	if err != nil {
		return AccountView{}, err
	}
	markets, err := s.store.PMSimMarkets(ctx, acct.ID)
	if err != nil {
		return AccountView{}, err
	}
	v := AccountView{Account: acct, State: pmsim.Replay(acct.Cash, orders, markets), Orders: orders}
	halt, err := s.store.Meta(ctx, HaltKey)
	if err != nil {
		return v, err
	}
	v.GlobalHalt = halt == "1"
	v.Equity = v.State.Cash
	if mark {
		s.markPositions(ctx, &v, markets)
	}
	for i := range v.State.Positions {
		p := v.State.Positions[i]
		if !p.Settled {
			v.Equity += p.Shares * p.Mark
			v.Unrealized += p.Shares*p.Mark - p.Cost
		}
	}
	v.Equity, v.Unrealized = round2(v.Equity), round2(v.Unrealized)
	// 最近的委托在前
	for i, j := 0, len(v.Orders)-1; i < j; i, j = i+1, j-1 {
		v.Orders[i], v.Orders[j] = v.Orders[j], v.Orders[i]
	}
	return v, nil
}

// markPositions 用实时盘口（一次批量请求）给未结算持仓估值：取该结果的买一；没有买盘时为 0。
func (s *Sim) markPositions(ctx context.Context, v *AccountView, markets map[string]pmsim.Market) {
	var firsts []string
	for _, p := range v.State.Positions {
		if m, ok := markets[p.Cond]; ok && !p.Settled && len(m.Tokens) > 0 {
			firsts = append(firsts, m.Tokens[0])
		}
	}
	if len(firsts) == 0 || s.src == nil {
		return
	}
	books, err := s.src.Books(ctx, firsts)
	if err != nil {
		return // 估值失败不影响查看：按 0 估值
	}
	byTok := map[string]ds.Book{}
	for _, b := range books {
		byTok[b.Token] = b
	}
	for i := range v.State.Positions {
		p := &v.State.Positions[i]
		m, ok := markets[p.Cond]
		if !ok || p.Settled || len(m.Tokens) == 0 {
			continue
		}
		if b, ok := byTok[m.Tokens[0]]; ok {
			if ob := pmsim.OutcomeBook(b, p.Outcome); len(ob.Bids) > 0 {
				p.Mark = ob.Bids[0].Price
			}
		}
	}
}

// SetGlobalHalt 打开 / 关闭全局熔断。
func SetGlobalHalt(ctx context.Context, st *store.Store, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return st.SetMeta(ctx, HaltKey, v)
}

// GlobalHalt 返回全局熔断是否打开。
func GlobalHalt(ctx context.Context, st *store.Store) (bool, error) {
	v, err := st.Meta(ctx, HaltKey)
	return v == "1", err
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

// slugAsset 从事件 slug 提取底层加密资产前缀（如 btc-updown-5m-123 → btc），用于跨周期联合敞口风控。
func slugAsset(slug string) string {
	a, rest, ok := strings.Cut(strings.ToLower(slug), "-")
	if !ok || !strings.HasPrefix(rest, "updown-") && !strings.HasPrefix(rest, "up-or-down-") {
		return ""
	}
	return a
}
