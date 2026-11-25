package job

import (
	"context"
	"errors"
	"log"
	"time"

	ds "market-thermometer/internal/datasource"
)

// SettingScheduleDone：调度已处理完的最后一个日期（YYYY-MM-DD），保证同一天只处理一次、重启后不重复。
const SettingScheduleDone = "schedule.done"

// Scheduler 是 mt serve 内置的每日任务（设计文档 §6.4）：
// 工作日 17:30 起增量同步并打分；当日数据不完整时每 30 分钟重试，22:00 截止。
// “完整”以引擎的口径判断：最新得分日期为当天且不是降级日。交易日以沪深300 当日有数据为准
// （打分的交易日历即沪深300 收盘价），到 22:00 仍没有当日得分则视为非交易日，静默结束。
// 完成后调用推送（推送自身按开关与去重决定是否发送）。
type Scheduler struct {
	Start, Deadline time.Duration // 距当日 0 点（北京时间）
	Retry           time.Duration

	runner *Runner
	pusher *Pusher
	now    func() time.Time
	logf   func(format string, args ...any)

	// 以下可在测试中替换
	sync     func(ctx context.Context) error
	complete func(ctx context.Context, date string) (have, degraded bool, err error)
	dispatch func(ctx context.Context) (PushReport, error)

	nextTry time.Time
}

func NewScheduler(r *Runner, p *Pusher) *Scheduler {
	s := &Scheduler{
		Start: 17*time.Hour + 30*time.Minute, Deadline: 22 * time.Hour, Retry: 30 * time.Minute,
		runner: r, pusher: p, now: time.Now, logf: log.Printf,
	}
	s.sync = func(ctx context.Context) error {
		st, err := r.RunSync(ctx, false)
		if err == nil && st.State == "failed" {
			err = errors.New(st.Kind + " 任务失败，详见数据页任务日志")
		}
		return err
	}
	s.complete = s.scoredToday
	s.dispatch = p.Dispatch
	return s
}

// Run 每分钟检查一次，直到 ctx 结束。
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	now := s.now().In(ds.Shanghai)
	if wd := now.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return
	}
	today := now.Format("2006-01-02")
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ds.Shanghai)
	if now.Before(midnight.Add(s.Start)) || now.Before(s.nextTry) {
		return
	}
	if done, err := s.runner.store.Setting(ctx, SettingScheduleDone); err != nil || done >= today {
		if err != nil {
			s.logf("调度：读取状态失败：%v", err)
		}
		return
	}
	deadline := midnight.Add(s.Deadline)
	final := !now.Before(deadline) // 已到截止时间：本次之后不再重试

	if err := s.sync(ctx); err != nil {
		if errors.Is(err, ErrBusy) {
			s.logf("调度：已有任务在运行，%v 后重试", s.Retry)
		} else {
			s.logf("调度：同步出错：%v", err)
		}
		if !final {
			s.retryLater(now, deadline)
			return
		}
	}
	have, degraded, err := s.complete(ctx, today)
	if err != nil {
		s.logf("调度：检查数据完整性失败：%v", err)
		s.retryLater(now, deadline)
		return
	}
	switch {
	case have && !degraded:
		s.logf("调度：%s 数据完整", today)
	case final && have:
		s.logf("调度：%s 截止时数据仍不完整（降级日），照常推送并注明", today)
	case final:
		s.logf("调度：截至 %v 没有 %s 的沪深300 数据，视为非交易日", s.Deadline, today)
		s.finish(ctx, today)
		return
	default:
		s.logf("调度：%s 数据尚不完整，%v 后重试", today, s.Retry)
		s.retryLater(now, deadline)
		return
	}
	rep, err := s.dispatch(ctx)
	switch {
	case err != nil && !final: // 发送失败的消息未记录为已推送，重试时只补发它们
		s.logf("调度：推送出错，%v 后重试：%v", s.Retry, err)
		s.retryLater(now, deadline)
		return
	case err != nil:
		s.logf("调度：推送出错（已到截止时间，不再重试）：%v", err)
	case len(rep.Sent) > 0:
		s.logf("调度：已推送 %d 条：%v", len(rep.Sent), rep.Sent)
	case rep.Skipped != "":
		s.logf("调度：未推送（%s）", rep.Skipped)
	}
	s.finish(ctx, today)
}

// retryLater 安排下一次尝试，不晚于截止时间（截止时刻还会再试最后一次）。
func (s *Scheduler) retryLater(now, deadline time.Time) {
	s.nextTry = now.Add(s.Retry)
	if s.nextTry.After(deadline) {
		s.nextTry = deadline
	}
}

func (s *Scheduler) finish(ctx context.Context, today string) {
	if err := s.runner.store.SetSetting(ctx, SettingScheduleDone, today); err != nil {
		s.logf("调度：保存状态失败：%v", err)
	}
}

// scoredToday 判断生效参数下是否已有 date 的得分，以及该日是否降级。
func (s *Scheduler) scoredToday(ctx context.Context, date string) (have, degraded bool, err error) {
	st := s.runner.store
	p, err := st.ActiveParams(ctx, s.now())
	if err != nil {
		return false, false, err
	}
	days, err := st.Scores(ctx, s.runner.mkt.ID, p.Version(), date, date)
	if err != nil || len(days) == 0 {
		return false, false, err
	}
	return true, days[0].Degraded, nil
}
