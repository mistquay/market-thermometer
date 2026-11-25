package job

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/market"
	"market-thermometer/internal/store"
)

type schedProbe struct {
	syncs, dispatches int
	syncErr           error
	have, degraded    bool
	dispatchErr       error
	logs              []string
}

func newTestScheduler(t *testing.T, clock *time.Time) (*Scheduler, *schedProbe) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	pr := &schedProbe{}
	s := NewScheduler(NewRunner(st, nil, market.CN, time.Now()), nil)
	s.now = func() time.Time { return *clock }
	s.logf = func(f string, a ...any) { pr.logs = append(pr.logs, fmt.Sprintf(f, a...)) }
	s.sync = func(context.Context) error { pr.syncs++; return pr.syncErr }
	s.complete = func(context.Context, string) (bool, bool, error) { return pr.have, pr.degraded, nil }
	s.dispatch = func(context.Context) (PushReport, error) { pr.dispatches++; return PushReport{}, pr.dispatchErr }
	return s, pr
}

func at(day, hhmm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hhmm, ds.Shanghai)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSchedulerRetryUntilComplete(t *testing.T) {
	ctx := context.Background()
	clock := at("2026-11-25", "17:00") // 周三
	s, pr := newTestScheduler(t, &clock)

	s.tick(ctx) // 17:30 之前不动
	if pr.syncs != 0 {
		t.Fatalf("ran before start: %d", pr.syncs)
	}
	clock = at("2026-11-25", "17:30")
	s.tick(ctx) // 第一次：数据不完整 → 30 分钟后重试
	if pr.syncs != 1 || pr.dispatches != 0 {
		t.Fatalf("first: %+v", pr)
	}
	clock = at("2026-11-25", "17:45")
	s.tick(ctx) // 未到重试时间
	if pr.syncs != 1 {
		t.Fatalf("retried too early: %d", pr.syncs)
	}
	clock = at("2026-11-25", "18:00")
	pr.have = true
	s.tick(ctx) // 完整 → 推送并标记完成
	if pr.syncs != 2 || pr.dispatches != 1 {
		t.Fatalf("complete: %+v", pr)
	}
	clock = at("2026-11-25", "19:00")
	s.tick(ctx) // 当天不再重复
	if pr.syncs != 2 || pr.dispatches != 1 {
		t.Fatalf("ran twice: %+v", pr)
	}
	// 重启后（新的 Scheduler、同一个库）也不重复
	s2 := NewScheduler(s.runner, nil)
	s2.now, s2.sync = s.now, func(context.Context) error { pr.syncs++; return nil }
	s2.tick(ctx)
	if pr.syncs != 2 {
		t.Fatalf("ran after restart: %d", pr.syncs)
	}
	// 第二天重新开始
	clock = at("2026-11-26", "17:31")
	s.tick(ctx)
	if pr.syncs != 3 || pr.dispatches != 2 {
		t.Fatalf("next day: %+v", pr)
	}
}

func TestSchedulerDeadline(t *testing.T) {
	ctx := context.Background()
	clock := at("2026-10-01", "21:59") // 国庆休市（周四）
	s, pr := newTestScheduler(t, &clock)
	s.tick(ctx)
	clock = at("2026-10-01", "22:00")
	s.tick(ctx) // 截止：没有当日数据 → 视为非交易日，不推送
	if pr.syncs != 2 || pr.dispatches != 0 {
		t.Fatalf("holiday: %+v", pr)
	}
	clock = at("2026-10-01", "23:00")
	s.tick(ctx)
	if pr.syncs != 2 {
		t.Fatalf("ran after deadline: %+v", pr)
	}

	// 截止时有当日数据但降级：照常推送
	clock = at("2026-10-09", "22:00")
	pr.have, pr.degraded = true, true
	s.tick(ctx)
	if pr.dispatches != 1 {
		t.Fatalf("degraded final: %+v", pr)
	}
}

func TestSchedulerWeekendBusyAndPushRetry(t *testing.T) {
	ctx := context.Background()
	clock := at("2026-11-28", "18:00") // 周六
	s, pr := newTestScheduler(t, &clock)
	s.tick(ctx)
	if pr.syncs != 0 {
		t.Fatalf("weekend: %+v", pr)
	}
	clock = at("2026-11-27", "17:30") // 周五：有手动任务在运行
	pr.syncErr = ErrBusy
	s.tick(ctx)
	if pr.syncs != 1 || pr.dispatches != 0 {
		t.Fatalf("busy: %+v", pr)
	}
	// 30 分钟后：数据完整，但推送全部失败 → 继续重试
	clock = at("2026-11-27", "18:00")
	pr.syncErr, pr.have, pr.dispatchErr = nil, true, errors.New("all channels failed")
	s.tick(ctx)
	if pr.dispatches != 1 {
		t.Fatalf("push fail: %+v", pr)
	}
	clock = at("2026-11-27", "18:30")
	pr.dispatchErr = nil
	s.tick(ctx)
	if pr.dispatches != 2 {
		t.Fatalf("push retry: %+v", pr)
	}
	clock = at("2026-11-27", "19:00")
	s.tick(ctx)
	if pr.dispatches != 2 {
		t.Fatalf("after success: %+v", pr)
	}
}
