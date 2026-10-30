package iq

import (
	"context"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"gpt-pro-monitor/internal/model"
)

func TestFirstAnchorAlignsToIntervalMultiples(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	origin := anchorOrigin(shanghai)
	at := func(clock string) time.Time {
		parsed, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-06 "+clock, shanghai)
		return parsed
	}
	cases := []struct {
		interval, jitter time.Duration
		now, want        string
	}{
		{5 * time.Minute, time.Minute, "10:01:00", "10:05:00"},    // 普通情况：下一个 5 分钟整点
		{5 * time.Minute, time.Minute, "10:03:59", "10:05:00"},    // 距锚点 61 秒 ≥ jitter，来得及
		{5 * time.Minute, time.Minute, "10:04:30", "10:10:00"},    // 距锚点只有 30 秒 < jitter：顺延一个周期
		{5 * time.Minute, time.Minute, "10:05:00", "10:10:00"},    // 恰好在锚点上：下一个
		{time.Hour, 5 * time.Minute, "10:20:00", "11:00:00"},      // 1 小时：北京时间整点
		{3 * time.Hour, 10 * time.Minute, "10:20:00", "12:00:00"}, // 3 小时：北京时间 0/3/6/9/12 点，不是 UTC 的 2/5/8/11 点
		{7 * time.Minute, 0, "10:20:00", "10:22:00"},              // 不整除一天的周期：从固定起点连续计数，不在每天午夜重置（锚点照样稳定不漂移）
	}
	for _, c := range cases {
		if got := firstAnchor(at(c.now), origin, c.interval, c.jitter); !got.Equal(at(c.want)) {
			t.Errorf("interval=%s now=%s: got %s want %s", c.interval, c.now, got.In(shanghai).Format("15:04:05"), c.want)
		}
	}
}

func TestNextAnchorSkipsMissedWindows(t *testing.T) {
	interval, jitter := 5*time.Minute, time.Minute
	base := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	// 这一轮按时跑完：下一个锚点就是 +1 周期，不跳过
	if anchor, skipped := nextAnchorAfter(base, base.Add(90*time.Second), interval, jitter); !anchor.Equal(base.Add(interval)) || skipped != 0 {
		t.Fatalf("按时跑完不应跳过: %s skipped=%d", anchor, skipped)
	}
	// 跑到 10:05:30：下一个锚点 10:05 的窗口（到 10:06）还没关，照常排它，不跳
	if anchor, skipped := nextAnchorAfter(base, base.Add(5*time.Minute+30*time.Second), interval, jitter); !anchor.Equal(base.Add(interval)) || skipped != 0 {
		t.Fatalf("窗口没关不应跳过: %s skipped=%d", anchor, skipped)
	}
	// 跑到 10:12：10:05 的窗口（到 10:06）已过，跳到 10:10（窗口到 10:11 也已过）→ 10:15，跳过 2 个
	if anchor, skipped := nextAnchorAfter(base, base.Add(12*time.Minute), interval, jitter); !anchor.Equal(base.Add(15*time.Minute)) || skipped != 2 {
		t.Fatalf("应跳过错过的周期: %s skipped=%d", anchor, skipped)
	}
}

func TestPlanSlotStaysWithinJitterOfAnchor(t *testing.T) {
	rng := mrand.New(mrand.NewPCG(5, 6))
	anchor := time.Date(2026, 10, 6, 10, 5, 0, 0, time.UTC)
	jitter := 60 * time.Second
	early, late := false, false
	for i := 0; i < 2000; i++ {
		slot := planSlot(anchor, jitter, rng)
		offset := slot.FireAt.Sub(anchor)
		if !slot.Anchor.Equal(anchor) || offset < -jitter || offset > jitter {
			t.Fatalf("开跑时刻越界: %s", offset)
		}
		early = early || offset < -50*time.Second
		late = late || offset > 50*time.Second
	}
	if !early || !late {
		t.Fatalf("随机范围应覆盖锚点前后两侧")
	}
}

func TestSampleSizeFor(t *testing.T) {
	ratio := func(percent, minimum int) model.IQScheduleConfig {
		return model.IQScheduleConfig{SampleMode: model.IQSampleRatio, SamplePercent: percent, SampleMin: minimum}
	}
	count := func(n int) model.IQScheduleConfig {
		return model.IQScheduleConfig{SampleMode: model.IQSampleCount, SampleCount: n}
	}
	cases := []struct {
		config    model.IQScheduleConfig
		available int
		want      int
	}{
		{ratio(10, 2), 51, 6},   // ceil(5.1)
		{ratio(10, 2), 5, 2},    // 下限
		{ratio(10, 2), 1, 1},    // 不超过可用总数
		{ratio(100, 1), 51, 51}, // 全测
		{ratio(33, 1), 10, 4},   // ceil(3.3)
		{count(7), 51, 7},
		{count(7), 3, 3},
		{count(7), 0, 0},
	}
	for _, c := range cases {
		if got := SampleSizeFor(c.config, c.available); got != c.want {
			t.Errorf("SampleSizeFor(%+v, %d) = %d want %d", c.config, c.available, got, c.want)
		}
	}
}

func TestDecodeScheduleFallsBackOnBadData(t *testing.T) {
	defaults := DefaultSchedule(5*time.Minute, time.Minute)
	if got, ok := decodeSchedule(`{"enabled":false,"interval_seconds":600}`, defaults); !ok || got.Enabled || got.IntervalSeconds != 600 || got.JitterSeconds != 60 || got.SamplePercent != 10 {
		t.Fatalf("缺的字段应用默认值补齐: %+v ok=%v", got, ok)
	}
	for _, raw := range []string{"not json", `{"interval_seconds":5}`, `{"sample_mode":"random"}`} {
		if got, ok := decodeSchedule(raw, defaults); ok || got != defaults {
			t.Errorf("坏数据 %q 应整份退回默认: %+v", raw, got)
		}
	}
}

// TestRunLoopFollowsScheduleChanges 用真实时钟跑调度循环：
// 关闭时不发题；改成 1 分钟周期、0 随机后按锚点开跑；再关掉立刻停。
func TestRunLoopFollowsScheduleChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("用真实时钟，跑约 2 秒")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tester := &fakeTester{version: "lmuai-0.2.13.2"}
	// 可「快进」的时钟：真实时间加一个偏移，让 1 分钟周期的锚点在测试里马上到。偏移跨 goroutine 读写，用原子量。
	var offset atomic.Int64
	now := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	svc := NewService(openTestStore(t), candidatesN(10), tester, Options{
		Interval: 5 * time.Minute, Jitter: time.Minute, Concurrency: 2, RequestTimeout: time.Minute,
		MinVersion: "lmuai-0.2.13.2", DefaultModels: []ModelSpec{{Model: "gpt-6-astra"}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: now, Rand: mrand.New(mrand.NewPCG(1, 1)),
	})
	off := false
	if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Schedule: &model.IQScheduleUpdate{Enabled: &off}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	if calls, _ := tester.snapshot(); len(calls) != 0 {
		t.Fatalf("关闭时不应发题")
	}
	if overview, _ := svc.Overview(ctx); overview.Schedule.NextRunAt != nil {
		t.Fatalf("关闭时不应有下次时间")
	}

	// 把时钟拨到离下一个 1 分钟锚点只剩 ~300ms，再开启 1 分钟周期、0 随机
	real := time.Now()
	offset.Store(int64(real.Truncate(time.Minute).Add(time.Minute).Add(-300 * time.Millisecond).Sub(real)))
	on, interval, jitter := true, 60, 0
	if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Schedule: &model.IQScheduleUpdate{Enabled: &on, IntervalSeconds: &interval, JitterSeconds: &jitter}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls, _ := tester.snapshot(); len(calls) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls, _ := tester.snapshot(); len(calls) != 2 {
		t.Fatalf("开启后应在锚点上跑一轮（10 个号抽 2 个），got %d", len(calls))
	}
	overview, _ := svc.Overview(ctx)
	if overview.Schedule.NextAnchorAt == nil || overview.Schedule.NextAnchorAt.Sub(*overview.Schedule.LastRunAt) < 30*time.Second {
		t.Fatalf("跑完应排到下一个锚点: %+v", overview.Schedule)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后调度循环应退出")
	}
}
