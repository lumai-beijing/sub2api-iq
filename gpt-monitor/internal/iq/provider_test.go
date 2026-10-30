package iq

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-pro-monitor/internal/model"
)

// fakeProviderTester 按 BaseURL 决定答案，记录每次请求。
type fakeProviderTester struct {
	mu       sync.Mutex
	answers  map[string]string // base_url → 答案文本
	upstream string
	err      error
	asks     []struct {
		target ProviderTarget
		spec   ModelSpec
		prompt string
	}
	models []string
}

func (f *fakeProviderTester) Ask(_ context.Context, target ProviderTarget, spec ModelSpec, prompt string) TestResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asks = append(f.asks, struct {
		target ProviderTarget
		spec   ModelSpec
		prompt string
	}{target, spec, prompt})
	if f.err != nil {
		return TestResult{Err: f.err, Duration: time.Second}
	}
	return TestResult{Text: f.answers[target.BaseURL], UpstreamModel: firstNonEmpty(f.upstream, spec.Model), Duration: 3 * time.Second}
}

func (f *fakeProviderTester) ListModels(_ context.Context, target ProviderTarget) ([]string, error) {
	if target.APIKey == "bad-key-0000" {
		return nil, errors.New("API returned 401: invalid key")
	}
	return f.models, nil
}

func (f *fakeProviderTester) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asks)
}

func newProviderService(t *testing.T, c *clock) (*Service, *fakeProviderTester) {
	t.Helper()
	svc := newTestIQService(t, candidatesN(10), &fakeTester{version: "lmuai-0.2.13.2", answers: map[int64]string{}}, []ModelSpec{{Model: "gpt-6-astra", ReasoningEffort: "xhigh"}}, c)
	sealer, err := NewSealer("test-secret-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	tester := &fakeProviderTester{answers: map[string]string{}, models: []string{"gpt-6-astra", "gpt-5.6-sol"}}
	svc.EnableProviders(tester, sealer)
	return svc, tester
}

func providerInput(name, baseURL string) model.IQProviderInput {
	return model.IQProviderInput{
		Name: name, BaseURL: baseURL, APIFormat: model.IQProviderChat, APIKey: "sk-third-party-abcdef123456",
		Models:             "gpt-6-astra@xhigh, gpt-5.6-sol@max",
		IQProviderSchedule: model.IQProviderSchedule{Enabled: true, IntervalSeconds: 900, JitterSeconds: 60, SamplesPerModel: 2},
	}
}

func TestProviderCRUDAndKeyHandling(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	svc, _ := newProviderService(t, c)

	view, err := svc.CreateProvider(ctx, providerInput("中转 A", "https://relay-a.example.com/v1/chat/completions"))
	if err != nil {
		t.Fatal(err)
	}
	if view.BaseURL != "https://relay-a.example.com/v1" || view.Endpoint != "https://relay-a.example.com/v1/chat/completions" ||
		view.APIKeyHint != "••••3456" || len(view.Models) != 2 || len(view.Cards) != 2 || view.Schedule.SamplesPerModel != 2 {
		t.Fatalf("新建结果不对: %+v", view)
	}
	record, _ := svc.store.GetProvider(ctx, view.ID)
	if strings.Contains(record.APIKeySealed, "sk-third-party") {
		t.Fatalf("库里不应存明文 Key: %s", record.APIKeySealed)
	}

	// 同名拒绝
	if _, err := svc.CreateProvider(ctx, providerInput("中转 A", "https://other.example.com/v1")); err == nil {
		t.Fatal("同名供应商应被拒绝")
	}

	// 编辑：不填 Key 时保留原来的；同主机改路径允许
	edit := providerInput("中转 A", "https://relay-a.example.com/openai/v1")
	edit.APIKey = ""
	edit.Models = "gpt-6-astra@high"
	edit.IntervalSeconds = 1800
	updated, err := svc.UpdateProvider(ctx, view.ID, edit)
	if err != nil || updated.APIKeyHint != "••••3456" || updated.Schedule.IntervalSeconds != 1800 || len(updated.Models) != 1 {
		t.Fatalf("编辑后应保留原 Key、应用新配置: %+v %v", updated, err)
	}
	// 编辑：不填 Key 却换了主机 → 拒绝（防止把存着的 Key 发到别处）
	edit.BaseURL = "https://evil.example.com/v1"
	if _, err := svc.UpdateProvider(ctx, view.ID, edit); err == nil || !strings.Contains(err.Error(), "重新填写 API Key") {
		t.Fatalf("换主机不填 Key 应拒绝: %v", err)
	}
	// 换主机同时填了新 Key → 允许
	edit.APIKey = "sk-new-key-for-new-host-9999"
	if updated, err := svc.UpdateProvider(ctx, view.ID, edit); err != nil || updated.APIKeyHint != "••••9999" {
		t.Fatalf("换主机并填新 Key 应允许: %+v %v", updated, err)
	}

	// 列表里不出现 Key 本身
	list, err := svc.Providers(ctx)
	if err != nil || len(list.Providers) != 1 || list.DefaultModels != "gpt-6-astra@xhigh" {
		t.Fatalf("列表错: %+v %v", list, err)
	}

	if err := svc.DeleteProvider(ctx, view.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProvider(ctx, view.ID, false); err == nil {
		t.Fatal("删除不存在的供应商应报错")
	}
}

func TestProviderValidation(t *testing.T) {
	ctx := context.Background()
	svc, _ := newProviderService(t, &clock{now: time.Now()})
	mutate := []func(*model.IQProviderInput){
		func(in *model.IQProviderInput) { in.Name = "" },
		func(in *model.IQProviderInput) { in.BaseURL = "not a url" },
		func(in *model.IQProviderInput) { in.BaseURL = "ftp://x.example.com" },
		func(in *model.IQProviderInput) { in.APIFormat = "grpc" },
		func(in *model.IQProviderInput) { in.APIKey = "" },
		func(in *model.IQProviderInput) { in.APIKey = "has space" },
		func(in *model.IQProviderInput) { in.Models = "" },
		func(in *model.IQProviderInput) { in.Models = "gpt-6@turbo" },
		func(in *model.IQProviderInput) { in.Models = "a,b,c,d,e,f,g,h,i" },
		func(in *model.IQProviderInput) { in.IntervalSeconds = 30 },
		func(in *model.IQProviderInput) { in.JitterSeconds = 600 },
		func(in *model.IQProviderInput) { in.SamplesPerModel = 0 },
		func(in *model.IQProviderInput) { in.SamplesPerModel = 6 },
	}
	for i, m := range mutate {
		input := providerInput("p", "https://x.example.com/v1")
		m(&input)
		_, err := svc.CreateProvider(ctx, input)
		var iqErr *model.IQError
		if !errors.As(err, &iqErr) || iqErr.Status != http.StatusBadRequest {
			t.Errorf("case %d 应 400，got %v", i, err)
		}
	}
	if count, _ := svc.store.CountProviders(ctx); count != 0 {
		t.Fatalf("非法输入不应入库: %d", count)
	}
}

func TestProviderRoundsAreIsolatedFromPool(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	svc, tester := newProviderService(t, c)
	tester.answers["https://good.example.com/v1"] = `{"answer": 21, "reasoning": "对"}`
	tester.answers["https://bad.example.com/v1"] = `{"answer": 29, "reasoning": "错"}`

	good, err := svc.CreateProvider(ctx, providerInput("好中转", "https://good.example.com/v1"))
	if err != nil {
		t.Fatal(err)
	}
	bad, err := svc.CreateProvider(ctx, providerInput("坏中转", "https://bad.example.com/v1"))
	if err != nil {
		t.Fatal(err)
	}
	// 每个供应商跑 3 轮：2 个模型 × 每轮 2 次 = 每轮 4 题
	for _, id := range []int64{good.ID, bad.ID} {
		record, _ := svc.store.GetProvider(ctx, id)
		runtime := &providerRuntime{wake: make(chan struct{}, 1), done: make(chan struct{})}
		for round := 0; round < 3; round++ {
			svc.runProviderRound(ctx, record, runtime, "schedule", nil)
			c.Add(15 * time.Minute)
		}
	}
	if got := tester.count(); got != 24 {
		t.Fatalf("应发 2 个供应商 × 3 轮 × 4 题 = 24 次，got %d", got)
	}
	prompts := map[string]bool{}
	for _, ask := range tester.asks {
		if ask.target.APIKey != "sk-third-party-abcdef123456" {
			t.Fatalf("发题时应用解密后的 Key")
		}
		prompts[ask.prompt] = true
	}
	if len(prompts) != len(tester.asks) {
		t.Fatalf("第三方每次检测的题面也应随机改写: %d 种 / %d 次", len(prompts), len(tester.asks))
	}
	// 号池也跑一轮
	runRound(ctx, svc)

	// 号池的统计不应混进供应商的记录
	pool, err := svc.Card(ctx, "pool", "gpt-6-astra@xhigh", "")
	if err != nil {
		t.Fatal(err)
	}
	if pool.Card.History.Graded+pool.Card.History.Errors != 2 {
		t.Fatalf("号池卡片只应有号池自己的 2 条，got %+v", pool.Card.History)
	}
	accounts, _ := svc.Accounts(ctx, "all", 0)
	for _, row := range accounts.Accounts {
		if row.AccountID == 0 {
			t.Fatalf("账号排行里不应出现供应商的记录: %+v", row)
		}
	}

	providers, err := svc.Providers(ctx)
	if err != nil || len(providers.Providers) != 2 {
		t.Fatalf("providers: %v", err)
	}
	for _, p := range providers.Providers {
		for _, card := range p.Cards {
			if card.History.Graded != 6 {
				t.Fatalf("%s %s 应有 6 个样本（3 轮 × 2 次），got %d", p.Name, card.Spec.Label, card.History.Graded)
			}
		}
	}
	byName := map[string]model.IQProviderView{}
	for _, p := range providers.Providers {
		byName[p.Name] = p
	}
	if byName["好中转"].Level != LevelHealthy || byName["坏中转"].Level != LevelDanger {
		t.Fatalf("判级错: 好=%s 坏=%s", byName["好中转"].Level, byName["坏中转"].Level)
	}

	// 矩阵（时间窗口口径）：astra@xhigh 这一列，号池 + 两个供应商都有数据
	board, err := svc.Board(ctx, "all", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Rows) != 3 || board.Rows[0].Kind != "pool" || board.Rows[1].Name != "好中转" || board.Rows[2].Name != "坏中转" {
		t.Fatalf("矩阵应是号池 + 2 个供应商: %+v", board.Rows)
	}
	if len(board.Columns) != 2 || board.Columns[0].Label != "gpt-6-astra@xhigh" || board.Columns[1].Label != "gpt-5.6-sol@max" {
		t.Fatalf("列应是号池的模型在前、供应商多出来的在后: %+v", board.Columns)
	}
	goodCell, badCell := board.Rows[1].Cells[0], board.Rows[2].Cells[0]
	if *goodCell.Stats.AccuracyPercent != 100 || *badCell.Stats.AccuracyPercent != 0 || !goodCell.Best || badCell.Best {
		t.Fatalf("矩阵格子错: good=%+v bad=%+v", goodCell, badCell)
	}
	if !board.Rows[0].Cells[0].Insufficient || board.Rows[0].Cells[0].Best || goodCell.GapVsPool != nil {
		t.Fatalf("号池只有 2 个样本，应标样本不足、不参与比较，也不算差值: %+v", board.Rows[0].Cells[0])
	}
	if board.Rows[0].Cells[1].Configured || !board.Rows[1].Cells[1].Configured {
		t.Fatalf("号池没在测 sol@max，那一格应标未配置")
	}
	if board.Rows[1].Total.Stats.Graded != 12 || board.Rows[2].Total.Stats.Graded != 12 {
		t.Fatalf("合计错: %+v / %+v", board.Rows[1].Total.Stats, board.Rows[2].Total.Stats)
	}
	if len(board.Advisories) != 0 {
		t.Fatalf("时间窗口口径不出结论（结论讲的是「现在」）")
	}
	recent, _ := svc.Board(ctx, "", "")
	foundBad := false
	for _, a := range recent.Advisories {
		if a.SourceKey == "provider:"+itoa(bad.ID) && a.Level == "critical" {
			foundBad = true
			if !strings.Contains(a.Detail, "29") || !strings.Contains(a.Detail, "形状") {
				t.Fatalf("降智结论应带最常见的错答及其含义: %+v", a)
			}
		}
	}
	if !foundBad {
		t.Fatalf("坏中转应有一条 critical 结论: %+v", recent.Advisories)
	}

	// 删除时保留记录：矩阵里不再显示，但库里还在
	if err := svc.DeleteProvider(ctx, bad.ID, true); err != nil {
		t.Fatal(err)
	}
	if probes, _ := svc.store.RecentProbes(ctx, ProbeFilter{Source: ProviderSource(bad.ID), Limit: 100}); len(probes) != 12 {
		t.Fatalf("保留记录时不应删检测记录: %d", len(probes))
	}
	board, _ = svc.Board(ctx, "all", "")
	if len(board.Rows) != 2 {
		t.Fatalf("已删除的供应商不应出现在矩阵里: %+v", board.Rows)
	}
	// 删除时不保留：连记录一起删
	if err := svc.DeleteProvider(ctx, good.ID, false); err != nil {
		t.Fatal(err)
	}
	if probes, _ := svc.store.RecentProbes(ctx, ProbeFilter{Source: ProviderSource(good.ID), Limit: 100}); len(probes) != 0 {
		t.Fatalf("不保留时应删掉检测记录: %d", len(probes))
	}
}

func TestProviderErrorsAreRecordedNotCountedAsWrong(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	svc, tester := newProviderService(t, c)
	tester.err = errors.New("API returned 429: rate limited sk-third-party-abcdef123456")
	view, _ := svc.CreateProvider(ctx, providerInput("限流中转", "https://slow.example.com/v1"))
	record, _ := svc.store.GetProvider(ctx, view.ID)
	svc.runProviderRound(ctx, record, &providerRuntime{wake: make(chan struct{}, 1), done: make(chan struct{})}, "schedule", nil)
	probes, _ := svc.Probes(ctx, model.IQProbeQuery{Source: "provider:" + itoa(view.ID)})
	if len(probes.Probes) != 4 {
		t.Fatalf("应记 4 条出错: %d", len(probes.Probes))
	}
	for _, p := range probes.Probes {
		if p.Outcome != OutcomeError || strings.Contains(p.ErrorMessage, "sk-third-party") || p.ProviderID != view.ID {
			t.Fatalf("出错记录应抹掉 Key: %+v", p)
		}
	}
	list, _ := svc.Providers(ctx)
	if card := list.Providers[0].Cards[0]; card.Recent.Graded != 0 || card.Recent.Errors != 2 || card.Level != LevelUnknown {
		t.Fatalf("出错不应计入准确率: %+v", card.Recent)
	}
}

func TestTestProviderConnection(t *testing.T) {
	ctx := context.Background()
	svc, _ := newProviderService(t, &clock{now: time.Now()})
	result, err := svc.TestProvider(ctx, model.IQProviderTestRequest{BaseURL: "https://x.example.com/v1", APIKey: "good-key-0000", Models: "gpt-6-astra@xhigh, gpt-7-mystery"})
	if err != nil || !result.OK || result.ModelCount != 2 || len(result.Missing) != 1 || result.Missing[0] != "gpt-7-mystery" {
		t.Fatalf("测试连接结果错: %+v %v", result, err)
	}
	result, err = svc.TestProvider(ctx, model.IQProviderTestRequest{BaseURL: "https://x.example.com/v1", APIKey: "bad-key-0000"})
	if err != nil || result.OK || !strings.Contains(result.Message, "401") {
		t.Fatalf("Key 不对应返回失败原因: %+v %v", result, err)
	}
	// 编辑已有供应商时可以不填 Key，用库里的——但只能测原来的主机
	view, _ := svc.CreateProvider(ctx, providerInput("p", "https://x.example.com/v1"))
	if result, err := svc.TestProvider(ctx, model.IQProviderTestRequest{ProviderID: view.ID, BaseURL: "https://x.example.com/v2"}); err != nil || !result.OK {
		t.Fatalf("同主机用库里的 Key 应能测: %+v %v", result, err)
	}
	if _, err := svc.TestProvider(ctx, model.IQProviderTestRequest{ProviderID: view.ID, BaseURL: "https://evil.example.com/v1"}); err == nil {
		t.Fatal("换主机用库里的 Key 应拒绝")
	}
}

func TestProviderLoopRunsOnItsOwnSchedule(t *testing.T) {
	if testing.Short() {
		t.Skip("用真实时钟")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &clock{now: time.Now()}
	svc, tester := newProviderService(t, c)
	tester.answers["https://fast.example.com/v1"] = `{"answer": 21}`
	// 号池关掉，确认供应商的循环独立于号池
	off := false
	if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Schedule: &model.IQScheduleUpdate{Enabled: &off}}); err != nil {
		t.Fatal(err)
	}
	// 时钟快进到离下一个 1 分钟锚点 300ms
	real := time.Now()
	c.mu.Lock()
	c.now = real.Truncate(time.Minute).Add(time.Minute).Add(-300 * time.Millisecond)
	c.mu.Unlock()
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.Add(10 * time.Millisecond)
			}
		}
	}()
	input := providerInput("快中转", "https://fast.example.com/v1")
	input.IntervalSeconds, input.JitterSeconds, input.SamplesPerModel = 60, 0, 1
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	view, err := svc.CreateProvider(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for tester.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := tester.count(); got != 2 {
		t.Fatalf("供应商应在自己的锚点上跑一轮（2 个模型各 1 次），got %d", got)
	}
	list, _ := svc.Providers(ctx)
	if status := list.Providers[0].Status; status.NextAnchorAt == nil || status.LastRunAt == nil {
		t.Fatalf("应排好下一轮: %+v", status)
	}
	// 删除后循环退出，不再发题
	if err := svc.DeleteProvider(ctx, view.ID, false); err != nil {
		t.Fatal(err)
	}
	if svc.providerRuntime(view.ID) != nil {
		t.Fatal("删除后不应还有调度循环")
	}
	cancel()
	<-done
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
