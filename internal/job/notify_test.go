package job

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/market"
	"market-thermometer/internal/notify"
	"market-thermometer/internal/store"
)

// fakeBox 记录 fake 渠道收到的消息；fail 非空时发送失败。
var fakeBox struct {
	sync.Mutex
	got  []notify.Message
	fail error
}

type fakeNotifier struct{}

func (fakeNotifier) Send(_ context.Context, m notify.Message) error {
	fakeBox.Lock()
	defer fakeBox.Unlock()
	if fakeBox.fail != nil {
		return fakeBox.fail
	}
	fakeBox.got = append(fakeBox.got, m)
	return nil
}

func init() {
	notify.Register(notify.Kind{Name: "fake", New: func(map[string]string, notify.Env) (notify.Notifier, error) { return fakeNotifier{}, nil }})
}

func takeFake() []notify.Message {
	fakeBox.Lock()
	defer fakeBox.Unlock()
	got := fakeBox.got
	fakeBox.got = nil
	return got
}

// seedScores 写入 [2026-11-16 周一, …] 的前 n 个交易日得分与给定事件。
func seedScores(t *testing.T, st *store.Store, n int, evs []engine.Event) string {
	t.Helper()
	ctx := context.Background()
	p, err := st.ActiveParams(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dates := []string{"2026-11-13", "2026-11-16", "2026-11-17", "2026-11-18", "2026-11-19", "2026-11-20", "2026-11-23"}[:n]
	var days []engine.Day
	phases := make([]string, n)
	for i, d := range dates {
		days = append(days, engine.Day{Date: d, Close: 3900 + float64(i)*10, ERP: 0.05, V: 40, Tier: engine.TierFair,
			T: [4]float64{20, 30, 40, 50}, TS: 30 + float64(i), TPct: 30})
		phases[i] = string(engine.PhaseIdle)
	}
	if err := st.ReplaceScores(ctx, market.CN.ID, p.Version(), days); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateStates(ctx, market.CN.ID, p.Version(), dates, phases); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceEvents(ctx, market.CN.ID, p.Version(), evs); err != nil {
		t.Fatal(err)
	}
	return p.Version()
}

func TestDispatch(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	friday18 := time.Date(2026, 11, 20, 18, 0, 0, 0, ds.Shanghai)
	p := NewPusher(st, notify.Env{}, market.CN)
	p.now = func() time.Time { return friday18 }
	takeFake()

	ev := func(date, typ string) engine.Event {
		return engine.Event{Date: date, Type: typ, Reason: "r", Close: 3950, TS: 25, V: 40, Tier: engine.TierFair}
	}
	// 截至周四：历史上有一次底部信号
	seedScores(t, st, 5, []engine.Event{ev("2026-11-19", engine.EvBuyDip)})
	if _, err := st.SaveNotifyChannel(ctx, store.NotifyChannel{Kind: "fake", Name: "测试", Config: map[string]string{}, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// 开关关闭：不推送
	if rep, err := p.Dispatch(ctx); err != nil || len(rep.Sent) != 0 || len(takeFake()) != 0 {
		t.Fatalf("switch off: %+v %v", rep, err)
	}
	// 打开开关：水位线 = 周四，周四（及更早）的事件不补推
	if err := p.SetSignals(ctx, true); err != nil {
		t.Fatal(err)
	}
	if rep, err := p.Dispatch(ctx); err != nil || len(rep.Sent) != 0 {
		t.Fatalf("history pushed: %+v %v", rep, err)
	}
	// 周五：底部信号 + 武装失效（后者不推送）；全量重算后事件被删除重插，仍按 key 去重
	seedScores(t, st, 6, []engine.Event{ev("2026-11-19", engine.EvBuyDip), ev("2026-11-20", engine.EvSellPeak), ev("2026-11-20", engine.EvDisarmed)})
	rep, err := p.Dispatch(ctx)
	if err != nil || len(rep.Sent) != 1 {
		t.Fatalf("friday: %+v %v", rep, err)
	}
	got := takeFake()
	if len(got) != 1 || !strings.Contains(got[0].Title, "顶部信号") || !strings.Contains(got[0].Title, "2026-11-20") {
		t.Fatalf("message: %+v", got)
	}
	body := strings.ReplaceAll(got[0].Markdown(), disclaimer, "")
	for _, bad := range []string{"建议", "胜率"} {
		if strings.Contains(body, bad) {
			t.Fatalf("message contains %q: %s", bad, body)
		}
	}
	seedScores(t, st, 6, []engine.Event{ev("2026-11-19", engine.EvBuyDip), ev("2026-11-20", engine.EvSellPeak)})
	if rep, err := p.Dispatch(ctx); err != nil || len(rep.Sent) != 0 {
		t.Fatalf("dedup: %+v %v", rep, err)
	}

	// 周报：渠道全部失败时不记录，下次重试成功后只发一次
	if err := st.SetSetting(ctx, SettingWeekly, "on"); err != nil {
		t.Fatal(err)
	}
	fakeBox.Lock()
	fakeBox.fail = errors.New("boom")
	fakeBox.Unlock()
	if _, err := p.Dispatch(ctx); err == nil {
		t.Fatal("want error when all channels fail")
	}
	fakeBox.Lock()
	fakeBox.fail = nil
	fakeBox.Unlock()
	if rep, err := p.Dispatch(ctx); err != nil || len(rep.Sent) != 1 {
		t.Fatalf("weekly: %+v %v", rep, err)
	}
	got = takeFake()
	if len(got) != 1 || !strings.Contains(got[0].Title, "2026-11-16 ~ 2026-11-20") {
		t.Fatalf("weekly message: %+v", got)
	}
	w := got[0].Markdown()
	if !strings.Contains(w, "本周 +1.28%") || !strings.Contains(w, "11-20 顶部信号") || strings.Contains(w, "11-13") {
		t.Fatalf("weekly body: %s", w)
	}
	if rep, _ := p.Dispatch(ctx); len(rep.Sent) != 0 {
		t.Fatalf("weekly resent: %+v", rep)
	}

	// 关闭信号开关后，新事件不再推送
	if err := p.SetSignals(ctx, false); err != nil {
		t.Fatal(err)
	}
	seedScores(t, st, 7, []engine.Event{ev("2026-11-23", engine.EvBuyDip)})
	if rep, _ := p.Dispatch(ctx); len(rep.Sent) != 0 {
		t.Fatalf("switch off again: %+v", rep)
	}
}

func TestMoney(t *testing.T) {
	for in, want := range map[float64]string{3933: "3,933.00", 123.456: "123.46", 1234567.8: "1,234,567.80", -1234: "-1,234.00"} {
		if got := money(in); got != want {
			t.Errorf("money(%v) = %s, want %s", in, got, want)
		}
	}
}
