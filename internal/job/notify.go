package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/market"
	"market-thermometer/internal/notify"
	"market-thermometer/internal/store"
)

// 推送设置项（存 meta 表）。两个开关默认关闭。
const (
	// SettingSignals：实盘信号推送总开关（"on" 为开）。
	SettingSignals = "notify.signals"
	// SettingSignalsSince：打开开关时的最新交易日，只推送晚于它的事件，避免把历史事件补推一遍。
	SettingSignalsSince = "notify.signals_since"
	// SettingWeekly：周报开关（"on" 为开），与信号开关独立。
	SettingWeekly = "notify.weekly"
)

// pushEvents 是需要推送的强提醒事件（设计文档 §7）。
var pushEvents = map[string]bool{
	engine.EvArmedBottom: true, engine.EvArmedTop: true,
	engine.EvBuyDip: true, engine.EvSellPeak: true, engine.EvTierChange: true,
}

// Pusher 把信号事件与周报推送到已启用的渠道；按 notify_log 去重，可重复调用。
type Pusher struct {
	store *store.Store
	env   notify.Env
	mkt   market.Market
	now   func() time.Time
}

func NewPusher(st *store.Store, env notify.Env, mkt market.Market) *Pusher {
	return &Pusher{store: st, env: env, mkt: mkt, now: time.Now}
}

// ChannelResult 是单个渠道的发送结果。
type ChannelResult struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Error string `json:"error,omitempty"`
}

// SendTo 向指定渠道发送消息（不检查开关、不去重；测试发送也用它）。
func (p *Pusher) SendTo(ctx context.Context, chs []store.NotifyChannel, m notify.Message) []ChannelResult {
	out := make([]ChannelResult, 0, len(chs))
	for _, c := range chs {
		r := ChannelResult{ID: c.ID, Name: c.Name, Kind: c.Kind}
		n, err := notify.Build(c.Kind, c.Config, p.env)
		if err == nil {
			sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err = n.Send(sctx, m)
			cancel()
		}
		if err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out
}

// PushReport 是一次推送检查的摘要。
type PushReport struct {
	Sent    []string // 已推送的消息标题
	Skipped string   // 未推送的原因（开关关闭、无渠道等），仅供日志
}

// Dispatch 检查最新交易日的强提醒事件与周报，向全部已启用渠道推送尚未推送过的消息。
// 只要有一个渠道发送成功就记为已推送（不再重发）；全部失败时不记录，下次调用重试。
func (p *Pusher) Dispatch(ctx context.Context) (PushReport, error) {
	var rep PushReport
	signals, err := p.on(ctx, SettingSignals)
	if err != nil {
		return rep, err
	}
	weekly, err := p.on(ctx, SettingWeekly)
	if err != nil {
		return rep, err
	}
	if !signals && !weekly {
		rep.Skipped = "信号推送与周报均未开启"
		return rep, nil
	}
	chs, err := p.enabledChannels(ctx)
	if err != nil {
		return rep, err
	}
	if len(chs) == 0 {
		rep.Skipped = "没有启用的推送渠道"
		return rep, nil
	}
	params, err := p.store.ActiveParams(ctx, p.now())
	if err != nil {
		return rep, err
	}
	version := params.Version()
	last, phase, err := p.store.LatestPhase(ctx, p.mkt.ID, version)
	if err != nil || last == "" {
		return rep, err
	}
	lastT, _ := time.Parse("2006-01-02", last)
	days, err := p.store.Scores(ctx, p.mkt.ID, version, lastT.AddDate(0, 0, -14).Format("2006-01-02"), "")
	if err != nil {
		return rep, err
	}
	if len(days) == 0 {
		return rep, nil
	}
	events, err := p.events(ctx, version)
	if err != nil {
		return rep, err
	}
	day := days[len(days)-1]

	var msgs []keyed
	if signals {
		since, err := p.store.Setting(ctx, SettingSignalsSince)
		if err != nil {
			return rep, err
		}
		for _, e := range events {
			if e.Date == last && e.Date > since && pushEvents[e.Type] {
				msgs = append(msgs, keyed{fmt.Sprintf("event:%s:%s:%s", p.mkt.ID, e.Date, e.Type), eventMessage(e, day, version)})
			}
		}
	}
	// 周报：最新交易日是周五，且是最近几天的数据（避免数据陈旧时补发旧周报）。周五休市的那一周不发。
	if weekly && lastT.Weekday() == time.Friday && p.now().In(ds.Shanghai).Sub(lastT) < 72*time.Hour {
		y, w := lastT.ISOWeek()
		msgs = append(msgs, keyed{fmt.Sprintf("weekly:%s:%d-W%02d", p.mkt.ID, y, w), weeklyMessage(days, events, phase, version)})
	}

	var errs []error
	for _, km := range msgs {
		sent, err := p.store.NotifySent(ctx, km.key)
		if err != nil {
			return rep, err
		}
		if sent {
			continue
		}
		results := p.SendTo(ctx, chs, km.msg)
		ok, summary := 0, make([]string, 0, len(results))
		for _, r := range results {
			if r.Error == "" {
				ok++
				summary = append(summary, r.Name+"：成功")
			} else {
				summary = append(summary, r.Name+"："+r.Error)
			}
		}
		if ok == 0 {
			errs = append(errs, fmt.Errorf("%s 推送失败：%s", km.msg.Title, strings.Join(summary, "；")))
			continue
		}
		if err := p.store.LogNotify(ctx, store.NotifyLog{Key: km.key, Title: km.msg.Title, Result: strings.Join(summary, "；")}, p.now()); err != nil {
			return rep, err
		}
		rep.Sent = append(rep.Sent, km.msg.Title)
	}
	return rep, errors.Join(errs...)
}

type keyed struct {
	key string
	msg notify.Message
}

// SetSignals 打开或关闭信号推送；打开时把水位线设为当前最新交易日，更早的事件永不推送。
func (p *Pusher) SetSignals(ctx context.Context, on bool) error {
	if !on {
		return p.store.SetSetting(ctx, SettingSignals, "")
	}
	params, err := p.store.ActiveParams(ctx, p.now())
	if err != nil {
		return err
	}
	last, _, err := p.store.LatestPhase(ctx, p.mkt.ID, params.Version())
	if err != nil {
		return err
	}
	if err := p.store.SetSetting(ctx, SettingSignalsSince, last); err != nil {
		return err
	}
	return p.store.SetSetting(ctx, SettingSignals, "on")
}

func (p *Pusher) on(ctx context.Context, key string) (bool, error) {
	v, err := p.store.Setting(ctx, key)
	return v == "on", err
}

func (p *Pusher) enabledChannels(ctx context.Context) ([]store.NotifyChannel, error) {
	all, err := p.store.NotifyChannels(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.NotifyChannel
	for _, c := range all {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

// storedEvent 是 signal_event.payload_json 的结构（数值缺失时为 null）。
type storedEvent struct {
	Date    string   `json:"date"`
	Type    string   `json:"type"`
	Reason  string   `json:"reason"`
	Observe bool     `json:"observe"`
	Close   *float64 `json:"close"`
	TS      *float64 `json:"tS"`
	V       *float64 `json:"v"`
	Tier    string   `json:"tier"`
}

func (p *Pusher) events(ctx context.Context, version string) ([]storedEvent, error) {
	raw, err := p.store.Events(ctx, p.mkt.ID, version)
	if err != nil {
		return nil, err
	}
	out := make([]storedEvent, 0, len(raw))
	for _, r := range raw {
		var e storedEvent
		if err := json.Unmarshal(r, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// ---- 文案：只做客观描述（设计文档 §7），不出现“建议”“胜率”等字样 ----

var (
	eventText = map[string]string{
		engine.EvArmedBottom: "底部武装", engine.EvArmedTop: "顶部武装",
		engine.EvBuyDip: "底部信号", engine.EvSellPeak: "顶部信号",
		engine.EvDisarmed: "武装失效", engine.EvTierChange: "估值档切换", engine.EvDegraded: "数据降级",
	}
	eventIcon = map[string]string{
		engine.EvArmedBottom: "🟡", engine.EvArmedTop: "🟡", engine.EvBuyDip: "🟢", engine.EvSellPeak: "🔴", engine.EvTierChange: "🔵",
	}
	tierText  = map[string]string{engine.TierCheap: "便宜", engine.TierFair: "合理", engine.TierExpensive: "昂贵"}
	phaseText = map[string]string{
		string(engine.PhaseIdle): "观望", string(engine.PhaseBottomArmed): "底部武装中", string(engine.PhaseTopArmed): "顶部武装中",
		string(engine.PhaseBottomCooldown): "底部信号后冷却", string(engine.PhaseTopCooldown): "顶部信号后冷却",
	}
)

const disclaimer = "以上为指标的客观描述，不构成投资建议。"

func eventMessage(e storedEvent, d engine.Day, version string) notify.Message {
	title := fmt.Sprintf("%s【A股温度计 · %s】%s", eventIcon[e.Type], eventText[e.Type], e.Date)
	reason := e.Reason
	if e.Type == engine.EvTierChange {
		for k, v := range tierText {
			reason = strings.ReplaceAll(reason, k, v)
		}
	}
	lines := []string{
		"**沪深300**：" + money(d.Close),
		"**估值**：" + valuation(d),
		"**择时温度** T_s：" + num(d.TS, 1),
		dims(d),
		"**规则**：" + reason,
	}
	if e.Observe {
		lines = append(lines, "估值处于昂贵档，此底部信号仅作观察。")
	}
	if d.Degraded {
		lines = append(lines, "⚠ 当日有维度缺数，数据不完整。")
	}
	lines = append(lines, disclaimer+"参数版本 "+version+"。")
	return notify.Message{Title: title, Lines: lines}
}

func weeklyMessage(days []engine.Day, events []storedEvent, phase, version string) notify.Message {
	last := days[len(days)-1]
	lastT, _ := time.Parse("2006-01-02", last.Date)
	monday := lastT.AddDate(0, 0, -int(lastT.Weekday()-time.Monday)).Format("2006-01-02")
	var week []engine.Day
	prevClose := math.NaN()
	for _, d := range days {
		if d.Date >= monday {
			week = append(week, d)
		} else {
			prevClose = d.Close
		}
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, d := range week {
		if !math.IsNaN(d.TS) {
			lo, hi = math.Min(lo, d.TS), math.Max(hi, d.TS)
		}
	}
	change := ""
	if prevClose > 0 {
		change = fmt.Sprintf("（本周 %+.2f%%）", (last.Close/prevClose-1)*100)
	}
	tsLine := fmt.Sprintf("**择时温度** T_s：周初 %s → 周末 %s", num(week[0].TS, 1), num(last.TS, 1))
	if !math.IsInf(lo, 0) {
		tsLine += fmt.Sprintf("（周内 %.1f ~ %.1f）", lo, hi)
	}
	var evs []string
	for _, e := range events {
		if e.Date >= monday && e.Date <= last.Date && e.Type != engine.EvDegraded {
			evs = append(evs, fmt.Sprintf("%s %s", e.Date[5:], eventText[e.Type]))
		}
	}
	evLine := "**本周事件**：无"
	if len(evs) > 0 {
		evLine = "**本周事件**：" + strings.Join(evs, "，")
	}
	lines := []string{
		"**沪深300**：" + money(last.Close) + change,
		"**估值**：" + valuation(last),
		tsLine,
		dims(last),
		"**状态机**：" + phaseLabel(phase),
		evLine,
		disclaimer + "参数版本 " + version + "。",
	}
	return notify.Message{Title: fmt.Sprintf("📊【A股温度计 · 周报】%s ~ %s", monday, last.Date), Lines: lines}
}

func phaseLabel(p string) string {
	if t, ok := phaseText[p]; ok {
		return t
	}
	return "—"
}

func valuation(d engine.Day) string {
	t := tierText[d.Tier]
	if t == "" {
		t = "—"
	}
	s := fmt.Sprintf("%s（V %s", t, num(d.V, 1))
	if !math.IsNaN(d.ERP) {
		s += fmt.Sprintf("，ERP %.2f%%", d.ERP*100)
	}
	return s + "）"
}

func dims(d engine.Day) string {
	return fmt.Sprintf("资金热度 T1 %s｜杠杆情绪 T2 %s｜市场广度 T3 %s｜价格动量 T4 %s",
		num(d.T[0], 0), num(d.T[1], 0), num(d.T[2], 0), num(d.T[3], 0))
}

func num(x float64, digits int) string {
	if math.IsNaN(x) {
		return "—"
	}
	return fmt.Sprintf("%.*f", digits, x)
}

// money 格式化价格，带千分位：3933 → 3,933.00。
func money(x float64) string {
	if math.IsNaN(x) {
		return "—"
	}
	s := fmt.Sprintf("%.2f", x)
	intPart, frac := s[:len(s)-3], s[len(s)-3:]
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 && intPart[i-1] != '-' {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String() + frac
}
