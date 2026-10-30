package iq

import (
	"context"
	"errors"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-pro-monitor/internal/model"
)

type fakeCandidates struct {
	rows      []model.IQCandidate
	err       error
	groups    []model.IQGroupInfo
	groupsErr error
}

func (f *fakeCandidates) ListIQCandidates(context.Context) ([]model.IQCandidate, error) {
	return f.rows, f.err
}

func (f *fakeCandidates) ListIQGroups(context.Context) ([]model.IQGroupInfo, error) {
	return f.groups, f.groupsErr
}

func (f *fakeCandidates) AccountGroups(_ context.Context, ids []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	for _, row := range f.rows {
		for _, id := range ids {
			if row.ID == id && len(row.GroupIDs) > 0 {
				out[id] = row.GroupIDs
			}
		}
	}
	return out, nil
}

// fakeTester 按账号 ID 决定答案：用 answers[id] 里的文本作答，errs[id] 非空时报错。
type fakeTester struct {
	mu       sync.Mutex
	version  string
	answers  map[int64]string
	upstream map[int64]string
	errs     map[int64]error
	calls    []TestRequest
	ids      []int64
	block    chan struct{}
}

func (f *fakeTester) TestAccount(ctx context.Context, id int64, request TestRequest) TestResult {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return TestResult{Err: ctx.Err()}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, request)
	f.ids = append(f.ids, id)
	if err := f.errs[id]; err != nil {
		return TestResult{Err: err, Duration: 3 * time.Second}
	}
	injected := true
	return TestResult{Text: f.answers[id], UpstreamModel: f.upstream[id], TicketInjected: &injected, Duration: 2 * time.Second}
}

func (f *fakeTester) Version(context.Context) (string, error) { return f.version, nil }

func (f *fakeTester) snapshot() ([]TestRequest, []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TestRequest(nil), f.calls...), append([]int64(nil), f.ids...)
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestIQService(t *testing.T, candidates *fakeCandidates, tester *fakeTester, specs []ModelSpec, c *clock) *Service {
	t.Helper()
	return NewService(openTestStore(t), candidates, tester, Options{
		Interval: 5 * time.Minute, Jitter: time.Minute, Concurrency: 2, RequestTimeout: time.Minute,
		MinVersion: "lmuai-0.2.13.2", DefaultModels: specs, ApplyTicket: true,
		HistoryPoints: 180, RecentWindow: 30, MinSamples: 3, HealthyPercent: 80, WarningPercent: 50,
		ClassifyModel: func(requested, observed string) string {
			if observed == requested {
				return "same"
			}
			return "degraded"
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    c.Now,
		Rand:   mrand.New(mrand.NewPCG(7, 9)),
	})
}

// runRound 按库里当前的定时配置跑一轮，等价于调度循环醒来那一下（含开关判断）。
func runRound(ctx context.Context, svc *Service) {
	config, _ := svc.scheduleConfig(ctx)
	if !config.Enabled {
		return
	}
	svc.runScheduledRound(ctx, config)
}

func candidatesN(n int) *fakeCandidates {
	rows := make([]model.IQCandidate, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, model.IQCandidate{ID: int64(i), Name: "acct-" + string(rune('a'+i-1))})
	}
	return &fakeCandidates{rows: rows}
}

func TestScheduledRoundSamplesGradesAndStores(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	tester := &fakeTester{version: "lmuai-0.2.13.2",
		answers:  map[int64]string{},
		upstream: map[int64]string{},
		errs:     map[int64]error{},
	}
	for i := int64(1); i <= 25; i++ {
		switch {
		case i%5 == 0:
			tester.errs[i] = errors.New("API returned 429: rate limited")
		case i%2 == 0:
			tester.answers[i] = `{"answer": 20, "reasoning": "漏了一个"}`
			tester.upstream[i] = "gpt-5.6-luna"
		default:
			tester.answers[i] = `{"answer": 21, "reasoning": "圆 9 + 星 12"}`
			tester.upstream[i] = "gpt-6-astra"
		}
	}
	specs := []ModelSpec{{Model: "gpt-6-astra", ReasoningEffort: "xhigh"}}
	svc := newTestIQService(t, candidatesN(25), tester, specs, c)

	runRound(ctx, svc)
	calls, ids := tester.snapshot()
	if len(calls) != 3 {
		t.Fatalf("25 个号应抽 ceil(25/10)=3 个，got %d", len(calls))
	}
	seen := map[int64]bool{}
	for i, call := range calls {
		if seen[ids[i]] {
			t.Fatalf("同一轮不应重复抽同一个号: %v", ids)
		}
		seen[ids[i]] = true
		if call.ModelID != "gpt-6-astra" || call.ReasoningEffort != "xhigh" || !call.Probe || !call.ApplyCodexTicket {
			t.Fatalf("测试请求参数错: %+v", call)
		}
		if !strings.Contains(call.Prompt, "糖") || !strings.Contains(call.Prompt, `"answer"`) {
			t.Fatalf("题目应是糖果题并要求 JSON 作答")
		}
	}
	prompts := map[string]bool{}
	for _, call := range calls {
		prompts[call.Prompt] = true
	}
	if len(prompts) != len(calls) {
		t.Fatalf("同一轮里每个号的题面都应随机改写，got %d 种 / %d 次", len(prompts), len(calls))
	}

	// 多跑几轮，攒够样本看判分与统计
	for round := 0; round < 20; round++ {
		c.Add(5 * time.Minute)
		runRound(ctx, svc)
	}
	overview, err := svc.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !overview.Gate.Open || len(overview.Board.Rows) != 1 || len(overview.Board.Columns) != 1 {
		t.Fatalf("闸应开、矩阵应是号池 1 行 × 1 个模型: %+v", overview.Board)
	}
	if !overview.Question.Randomized || len(overview.Question.Samples) != 3 || overview.Question.Prompt != OriginalCandy().Prompt {
		t.Fatalf("题目说明应带原题和随机示例: %+v", overview.Question)
	}
	poolCell := overview.Board.Rows[0].Cells[0]
	if !poolCell.Configured || poolCell.Stats.Graded == 0 || poolCell.Stats.Graded > 30 || len(poolCell.Strip) == 0 || len(poolCell.Strip) > 30 {
		t.Fatalf("号池格子应是最近 30 次可判分 + 迷你历史条: %+v", poolCell)
	}
	cardView, err := svc.Card(ctx, "pool", "gpt-6-astra@xhigh", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cardView.Probes) != 20 || cardView.Probes[0].Prompt == "" || cardView.Probes[0].Variant != QuestionScopeRandom {
		t.Fatalf("单格详情应带最近 20 次作答和当次题面: %d %+v", len(cardView.Probes), cardView.Probes[0])
	}
	card := cardView.Card
	if card.Spec.Label != "gpt-6-astra@xhigh" || len(card.Points) == 0 || len(card.Points) > 180 {
		t.Fatalf("卡片字段错: %+v", card.Spec)
	}
	for i := 1; i < len(card.Points); i++ {
		if card.Points[i].At.Before(card.Points[i-1].At) {
			t.Fatalf("历史条应按时间旧 → 新")
		}
	}
	h := card.History
	if h.Errors == 0 || h.Correct == 0 || h.Wrong == 0 || h.Graded != h.Correct+h.Wrong+h.NoAnswer {
		t.Fatalf("应同时有对、错、出错，且出错不进分母: %+v", h)
	}
	if h.Swapped != h.Wrong {
		t.Fatalf("答错的号都被换成了 luna，换模型次数应等于答错数: %+v", h)
	}
	if h.JSONFormat != h.Graded {
		t.Fatalf("全部按 JSON 作答: %+v", h)
	}
	if card.Recent.Graded > 30 || card.Level == LevelUnknown {
		t.Fatalf("最近窗口 ≤30 且样本足够应有判级: %+v level=%s", card.Recent, card.Level)
	}
	if card.Recent.Errors == 0 {
		t.Fatalf("最近窗口里的出错次数应单独数出来: %+v", card.Recent)
	}

	view, err := svc.Accounts(ctx, "24h", 0)
	if err != nil {
		t.Fatal(err)
	}
	if view.Tested == 0 || view.Available != 25 {
		t.Fatalf("账号排行汇总错: tested=%d available=%d", view.Tested, view.Available)
	}
	for i := 1; i < len(view.Accounts); i++ {
		a, b := view.Accounts[i-1], view.Accounts[i]
		if !a.Insufficient && !b.Insufficient && accuracyOf(a.IQStats) > accuracyOf(b.IQStats) {
			t.Fatalf("样本足够的号应按准确率升序: %v > %v", accuracyOf(a.IQStats), accuracyOf(b.IQStats))
		}
		if a.Insufficient && !b.Insufficient {
			t.Fatalf("样本不足的号应排在后面")
		}
	}
	for _, row := range view.Accounts {
		if len(row.Recent) > 10 {
			t.Fatalf("近 10 次红绿条超长: %d", len(row.Recent))
		}
		if row.AccountID%5 == 0 && row.Graded != 0 {
			t.Fatalf("总出错的号不应有可判分样本: %+v", row)
		}
	}

	probes, err := svc.Probes(ctx, model.IQProbeQuery{Model: "gpt-6-astra@xhigh", Limit: 5})
	if err != nil || len(probes.Probes) != 5 {
		t.Fatalf("采样详情错: %d %v", len(probes.Probes), err)
	}
	for _, p := range probes.Probes {
		if p.Outcome != OutcomeError && (p.AnswerFormat != FormatJSON || p.AnswerReasoning == "") {
			t.Fatalf("JSON 作答的 reasoning 应存下来: %+v", p)
		}
	}
}

func TestGateBlocksOldSub2API(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	tester := &fakeTester{version: "lmuai-0.2.10.3"}
	svc := newTestIQService(t, candidatesN(10), tester, []ModelSpec{{Model: "gpt-6-astra"}}, c)

	runRound(ctx, svc)
	if calls, _ := tester.snapshot(); len(calls) != 0 {
		t.Fatalf("旧版 sub2api 一次都不应调用测试接口，got %d", len(calls))
	}
	overview, _ := svc.Overview(ctx)
	if overview.Level != LevelBlocked || overview.Gate.Open || !strings.Contains(overview.Schedule.LastError, "0.2.10.3") {
		t.Fatalf("应显示等待 sub2api 支持: level=%s gate=%+v err=%q", overview.Level, overview.Gate, overview.Schedule.LastError)
	}
	if _, err := svc.RunNow(ctx, model.IQRunRequest{}); err == nil {
		t.Fatalf("立即检测也应被版本闸挡住")
	}

	// sub2api 升级后，立即检测会强制重读版本，不用等缓存过期
	tester.mu.Lock()
	tester.version = "lmuai-0.2.13.2"
	tester.answers = map[int64]string{}
	tester.mu.Unlock()
	if _, err := svc.RunNow(ctx, model.IQRunRequest{}); err != nil {
		t.Fatalf("升级后应放行: %v", err)
	}
	svc.Wait()
	if calls, _ := tester.snapshot(); len(calls) != 2 {
		t.Fatalf("10 个号应抽 2 个，got %d", len(calls))
	}
}

func TestShutdownInterruptedProbesAreNotStored(t *testing.T) {
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	tester := &fakeTester{version: "lmuai-0.2.13.2", block: make(chan struct{})}
	svc := newTestIQService(t, candidatesN(10), tester, []ModelSpec{{Model: "gpt-6-astra"}}, c)
	ctx, cancel := context.WithCancel(context.Background())
	config, _ := svc.scheduleConfig(ctx)
	done := make(chan struct{})
	go func() { defer close(done); svc.runScheduledRound(ctx, config) }()
	time.Sleep(50 * time.Millisecond) // 等两个测试都卡在 block 上
	cancel()                          // 模拟进程收到停止信号
	<-done
	probes, err := svc.store.RecentProbes(context.Background(), ProbeFilter{Limit: 10})
	if err != nil || len(probes) != 0 {
		t.Fatalf("被停机打断的测试不应入库（会被误记成号出错）: %+v err=%v", probes, err)
	}
}

func TestRecentlyProbedAccountsAreSkipped(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	tester := &fakeTester{version: "lmuai-0.2.13.2"}
	svc := newTestIQService(t, candidatesN(20), tester, []ModelSpec{{Model: "gpt-6-astra"}}, c)

	// 连跑 10 轮、每轮只隔 10 秒（远小于 5 分钟间隔）：20 个号每轮抽 2 个，应恰好不重复地覆盖全部号
	for round := 0; round < 10; round++ {
		runRound(ctx, svc)
		c.Add(10 * time.Second)
	}
	_, ids := tester.snapshot()
	seen := map[int64]int{}
	for _, id := range ids {
		seen[id]++
	}
	if len(ids) != 20 || len(seen) != 20 {
		t.Fatalf("一个间隔内同一个号不应被测两次: %d 次调用覆盖 %d 个号 %v", len(ids), len(seen), seen)
	}

	// 全部号都在冷却里时放宽：照样凑满样本数，不会空转
	runRound(ctx, svc)
	if _, ids := tester.snapshot(); len(ids) != 22 {
		t.Fatalf("可用号全在冷却里时应放宽并照常抽样，got %d", len(ids))
	}
}

func TestDisabledScheduleSkipsRoundButManualStillWorks(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	tester := &fakeTester{version: "lmuai-0.2.13.2"}
	svc := newTestIQService(t, candidatesN(10), tester, []ModelSpec{{Model: "gpt-6-astra"}}, c)
	off := false
	if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Schedule: &model.IQScheduleUpdate{Enabled: &off}}); err != nil {
		t.Fatal(err)
	}
	runRound(ctx, svc)
	if calls, _ := tester.snapshot(); len(calls) != 0 {
		t.Fatalf("定时关闭后不应发题")
	}
	overview, _ := svc.Overview(ctx)
	if overview.Level != LevelPaused || !overview.Schedule.Paused || overview.Schedule.Enabled || overview.Schedule.NextRunAt != nil {
		t.Fatalf("应显示已暂停且没有下次时间: level=%s schedule=%+v", overview.Level, overview.Schedule)
	}
	// 手动「立即跑一轮」不受定时开关影响
	if _, err := svc.RunNow(ctx, model.IQRunRequest{}); err != nil {
		t.Fatalf("定时关闭时手动检测应照常可用: %v", err)
	}
	svc.Wait()
	if calls, _ := tester.snapshot(); len(calls) != 2 {
		t.Fatalf("手动跑一轮应按配置抽 2 个，got %d", len(calls))
	}
}

func TestLegacyPausedFlagCarriesOver(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Now()}
	svc := newTestIQService(t, candidatesN(3), &fakeTester{version: "lmuai-0.2.13.2"}, []ModelSpec{{Model: "gpt-6-astra"}}, c)
	// 旧版直接写的 paused=true：没存过 schedule 时，升级后应仍是关闭
	if err := svc.store.SetSetting(ctx, settingPaused, "true"); err != nil {
		t.Fatal(err)
	}
	view, err := svc.Settings(ctx)
	if err != nil || view.Schedule.Enabled || !view.Paused || view.ScheduleSource != "default" {
		t.Fatalf("旧的暂停状态应沿用: %+v %v", view, err)
	}
	// 旧前端发 {"paused": false} 仍然有效
	resume := false
	view, err = svc.UpdateSettings(ctx, model.IQSettingsRequest{Paused: &resume})
	if err != nil || !view.Schedule.Enabled || view.ScheduleSource != "settings" {
		t.Fatalf("旧的 paused 字段应等价于 enabled 取反: %+v %v", view, err)
	}
}

func TestScheduleSampleConfigDrivesRoundSize(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	tester := &fakeTester{version: "lmuai-0.2.13.2"}
	svc := newTestIQService(t, candidatesN(40), tester, []ModelSpec{{Model: "gpt-6-astra"}}, c)

	set := func(update model.IQScheduleUpdate) {
		t.Helper()
		if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Schedule: &update}); err != nil {
			t.Fatal(err)
		}
	}
	countCalls := func() int { calls, _ := tester.snapshot(); return len(calls) }

	// 按比例：40 个号的 25% = 10 个
	mode, percent := model.IQSampleRatio, 25
	set(model.IQScheduleUpdate{SampleMode: &mode, SamplePercent: &percent})
	runRound(ctx, svc)
	if got := countCalls(); got != 10 {
		t.Fatalf("25%% 应抽 10 个，got %d", got)
	}
	// 按比例但有下限：40 个号的 5% = 2，下限 4 → 4 个
	percent, minimum := 5, 4
	set(model.IQScheduleUpdate{SamplePercent: &percent, SampleMin: &minimum})
	c.Add(10 * time.Minute)
	runRound(ctx, svc)
	if got := countCalls() - 10; got != 4 {
		t.Fatalf("比例低于下限时应按下限抽 4 个，got %d", got)
	}
	// 按个数：固定 7 个
	countMode, count := model.IQSampleCount, 7
	set(model.IQScheduleUpdate{SampleMode: &countMode, SampleCount: &count})
	c.Add(10 * time.Minute)
	runRound(ctx, svc)
	if got := countCalls() - 14; got != 7 {
		t.Fatalf("按个数应抽 7 个，got %d", got)
	}
	overview, _ := svc.Overview(ctx)
	if overview.Schedule.SampleSize != 7 || overview.Schedule.Candidates != 40 || overview.Schedule.ConfigSource != "settings" {
		t.Fatalf("看板应展示按个数的抽样: %+v", overview.Schedule)
	}
	// 个数大于可用号时不超过可用总数
	count = 100
	set(model.IQScheduleUpdate{SampleCount: &count})
	c.Add(10 * time.Minute)
	runRound(ctx, svc)
	if got := countCalls() - 21; got != 40 {
		t.Fatalf("个数超过可用号时应只抽全部 40 个，got %d", got)
	}
}

func TestScheduleUpdateValidation(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Now()}
	svc := newTestIQService(t, candidatesN(3), &fakeTester{version: "lmuai-0.2.13.2"}, []ModelSpec{{Model: "gpt-6-astra"}}, c)
	before, _ := svc.Settings(ctx)

	ptr := func(v int) *int { return &v }
	str := func(v string) *string { return &v }
	bad := []model.IQScheduleUpdate{
		{IntervalSeconds: ptr(30)},                           // 周期太短
		{IntervalSeconds: ptr(25 * 3600)},                    // 周期太长
		{JitterSeconds: ptr(-1)},                             // 负数
		{IntervalSeconds: ptr(300), JitterSeconds: ptr(151)}, // 超过周期一半
		{SampleMode: str("random")},                          // 未知方式
		{SamplePercent: ptr(0)},                              // 比例为 0
		{SamplePercent: ptr(101)},                            // 比例超 100
		{SampleMode: str(model.IQSampleCount), SampleCount: ptr(0)},
		{SampleMin: ptr(0)},
	}
	for _, update := range bad {
		u := update
		if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Schedule: &u}); err == nil {
			t.Errorf("应被拒绝: %+v", u)
		}
	}
	// 一个请求里模型合法但周期不合法：整个请求都不生效
	models := "gpt-5.6-sol@max"
	if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Models: &models, Schedule: &model.IQScheduleUpdate{IntervalSeconds: ptr(10)}}); err == nil {
		t.Fatal("非法周期应拒绝整个请求")
	}
	after, _ := svc.Settings(ctx)
	if after.Schedule != before.Schedule || after.Models[0].Label != before.Models[0].Label {
		t.Fatalf("被拒的请求不应改动任何配置: before=%+v after=%+v", before, after)
	}
	// 刚好等于周期一半是允许的
	if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Schedule: &model.IQScheduleUpdate{IntervalSeconds: ptr(600), JitterSeconds: ptr(300)}}); err != nil {
		t.Fatalf("随机范围等于周期一半应允许: %v", err)
	}
}

func TestRunNowSingleAccountAndGuards(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	tester := &fakeTester{version: "lmuai-0.2.13.2", answers: map[int64]string{3: `{"answer":21}`}, block: make(chan struct{})}
	specs := []ModelSpec{{Model: "gpt-6-astra", ReasoningEffort: "xhigh"}, {Model: "gpt-5.6-sol", ReasoningEffort: "max"}}
	svc := newTestIQService(t, candidatesN(5), tester, specs, c)

	expectStatus := func(err error, status int) {
		t.Helper()
		var iqErr *model.IQError
		if !errors.As(err, &iqErr) || iqErr.Status != status {
			t.Fatalf("want status %d, got %v", status, err)
		}
	}
	_, err := svc.RunNow(ctx, model.IQRunRequest{AccountID: 99})
	expectStatus(err, http.StatusConflict) // 不可用的号
	_, err = svc.RunNow(ctx, model.IQRunRequest{AccountID: 3, Model: "gpt-9"})
	expectStatus(err, http.StatusBadRequest) // 不在清单里的模型

	response, err := svc.RunNow(ctx, model.IQRunRequest{AccountID: 3, Model: "gpt-5.6-sol@max"})
	if err != nil || response.Kind != "account" || response.Targets != 1 {
		t.Fatalf("单号检测应受理: %+v %v", response, err)
	}
	_, err = svc.RunNow(ctx, model.IQRunRequest{})
	expectStatus(err, http.StatusConflict) // 上一轮还在跑
	close(tester.block)
	svc.Wait()

	_, err = svc.RunNow(ctx, model.IQRunRequest{})
	expectStatus(err, http.StatusTooManyRequests) // 冷却中
	c.Add(time.Minute)
	if _, err := svc.RunNow(ctx, model.IQRunRequest{}); err != nil {
		t.Fatalf("冷却过后应受理: %v", err)
	}
	svc.Wait()

	calls, ids := tester.snapshot()
	if ids[0] != 3 || calls[0].ModelID != "gpt-5.6-sol" || calls[0].ReasoningEffort != "max" {
		t.Fatalf("单号检测应按指定模型发题: id=%d %+v", ids[0], calls[0])
	}
	probes, _ := svc.Probes(ctx, model.IQProbeQuery{AccountID: 3})
	if len(probes.Probes) == 0 || probes.Probes[len(probes.Probes)-1].Trigger != "manual" {
		t.Fatalf("应记成 manual 触发: %+v", probes.Probes)
	}
}

func TestSettingsValidation(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Now()}
	svc := newTestIQService(t, candidatesN(3), &fakeTester{version: "lmuai-0.2.13.2"}, []ModelSpec{{Model: "gpt-6-astra", ReasoningEffort: "xhigh"}}, c)

	view, err := svc.Settings(ctx)
	if err != nil || view.ModelsSource != "default" || view.Models[0].Label != "gpt-6-astra@xhigh" {
		t.Fatalf("未配置时应回落默认清单: %+v %v", view, err)
	}
	for _, bad := range []string{"", "gpt-6@turbo", "a,b,c,d,e,f,g,h,i"} {
		value := bad
		if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Models: &value}); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	value := "gpt-5.6-sol@max, gpt-6-astra@high"
	view, err = svc.UpdateSettings(ctx, model.IQSettingsRequest{Models: &value})
	if err != nil || view.ModelsSource != "settings" || len(view.Models) != 2 || view.Models[1].Label != "gpt-6-astra@high" {
		t.Fatalf("保存后应读到新清单: %+v %v", view, err)
	}
}

func TestAccountsRejectsUnknownWindow(t *testing.T) {
	c := &clock{now: time.Now()}
	svc := newTestIQService(t, candidatesN(3), &fakeTester{version: "lmuai-0.2.13.2"}, []ModelSpec{{Model: "gpt-6-astra"}}, c)
	if _, err := svc.Accounts(context.Background(), "1y", 0); err == nil {
		t.Fatalf("未知窗口应报错")
	}
}
