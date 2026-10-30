package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-pro-monitor/internal/db"
	"gpt-pro-monitor/internal/model"
)

type fakeCodexSource struct {
	mu          sync.Mutex
	accounts    []model.CodexTicketAccountRow
	settings    map[string]string
	usage       db.UsageAggregate
	daily       []db.DailyPairBucket
	accountCall int
	usageCall   int
	dailyCall   int
	lastModels  []string
	accountErr  error
	usageErr    error
}

func (f *fakeCodexSource) ListTicketAccounts(context.Context) ([]model.CodexTicketAccountRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accountCall++
	if f.accountErr != nil {
		return nil, f.accountErr
	}
	return f.accounts, nil
}

func (f *fakeCodexSource) AggregateGatedUsage(_ context.Context, _ int, models []string) (db.UsageAggregate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usageCall++
	f.lastModels = append([]string(nil), models...)
	if f.usageErr != nil {
		return db.UsageAggregate{}, f.usageErr
	}
	out := f.usage
	out.Models = append([]string(nil), models...)
	return out, nil
}

func (f *fakeCodexSource) AggregateGatedDaily(context.Context, int, []string) ([]db.DailyPairBucket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dailyCall++
	return f.daily, nil
}

func (f *fakeCodexSource) LoadCodexSettings(context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, nil
}

func (f *fakeCodexSource) calls() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accountCall, f.usageCall, f.dailyCall
}

// ticketsJSON 拼出 extra 里 codex_turn_ticket:* 键的对象，模拟数据库返回。
func ticketsJSON(now time.Time, entries map[string][3]any) string {
	parts := []string{}
	for m, e := range entries {
		length, attempts, offsets := e[0].(int), e[1].(int), e[2].([2]time.Duration)
		parts = append(parts, fmt.Sprintf(`"codex_turn_ticket:%s":{"model":%q,"state":"gAAAAAsecret","length":%d,"attempts":%d,"captured_at":%q,"expires_at":%q}`,
			m, m, length, attempts, now.Add(offsets[0]).Format(time.RFC3339Nano), now.Add(offsets[1]).Format(time.RFC3339Nano)))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func pair(req, resp string) db.UsagePair { return db.UsagePair{Requested: req, Response: resp} }

func newTestService(source *fakeCodexSource, now time.Time) *CodexService {
	return NewCodexService(source, CodexOptions{
		PoolCacheTTL: time.Minute, UsageCacheTTL: 2 * time.Minute, TrendCacheTTL: 30 * time.Minute,
		SampleInterval: 2 * time.Minute, TrendRetention: 24 * time.Hour,
		UsageWindowHours: 24, TrendDays: 7, PoolGroupIDs: []int64{67, 69},
		DefaultModels: []string{"gpt-6-astra"}, UsageStaleAfter: time.Hour,
		Now: func() time.Time { return now },
	})
}

func TestClassifyResponseModelMatchesWatchdog(t *testing.T) {
	cases := map[[2]string]string{
		{"gpt-6-astra", "gpt-6-astra"}:      model.ResponseSame,
		{"gpt-6-astra", ""}:                 model.ResponseBlank,
		{"gpt-6-astra", "gpt-5.6-luna"}:     model.ResponseDegraded,
		{"gpt-5.6-sol", "gpt-6-sol"}:        model.ResponseUpgraded,
		{"gpt-5.6-sol", "gpt-5.5-sol"}:      model.ResponseDegraded,
		{"gpt-6-terra", "gpt-6-sol"}:        model.ResponseDegraded,
		{"gpt-6-astra", "o5-preview"}:       model.ResponseUnknown,
		{"gpt-6-astra", "GPT-6-Astra"}:      model.ResponseSame,
		{"gpt-6-astra", "gpt-6-astra-mini"}: model.ResponseDegraded,
	}
	for in, want := range cases {
		if got := classifyResponseModel(in[0], in[1]); got != want {
			t.Errorf("%s → %s: want %s got %s", in[0], in[1], want, got)
		}
	}
}

func TestGatedModelsComeFromSettingsWithDefaultFallback(t *testing.T) {
	now := time.Now()
	svc := newTestService(&fakeCodexSource{}, now)
	if got := svc.gatedModels(map[string]string{"openai_codex_ticket_models": " gpt-6-astra , gpt-5.6-sol,gpt-6-astra "}); len(got) != 2 || got[1] != "gpt-5.6-sol" {
		t.Fatalf("settings 清单解析错: %v", got)
	}
	if got := svc.gatedModels(map[string]string{"openai_codex_ticket_models": ""}); len(got) != 1 || got[0] != "gpt-6-astra" {
		t.Fatalf("空清单应回落默认: %v", got)
	}
	if got := svc.gatedModels(nil); len(got) != 1 {
		t.Fatalf("settings 缺失应回落默认: %v", got)
	}
	config := svc.buildConfig(map[string]string{"openai_codex_ticket_models": "gpt-6-astra,gpt-5.6-sol"})
	if config.GatedModelsSource != "db" {
		t.Fatalf("来源应为 db，实际 %s", config.GatedModelsSource)
	}
	config = svc.buildConfig(map[string]string{})
	if config.GatedModelsSource != "default" {
		t.Fatalf("来源应为 default，实际 %s", config.GatedModelsSource)
	}
}

func TestClassifyTicketStatesPerModel(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	config := model.CodexConfigView{TargetLength: 292, FreshMinSeconds: 300, GatedModels: []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-6-terra"}}
	rows := []model.CodexTicketAccountRow{{
		ID: 1, OptIn: true, Schedulable: true, Status: "active",
		TicketsJSON: ticketsJSON(now, map[string][3]any{
			"gpt-6-astra": {292, 1, [2]time.Duration{-10 * time.Minute, 50 * time.Minute}},
			"gpt-5.6-sol": {292, 3, [2]time.Duration{-59 * time.Minute, 2 * time.Minute}},
			"gpt-6-sol":   {292, 1, [2]time.Duration{-time.Minute, time.Hour}}, // 不在门控清单，应被忽略
		}),
	}, {ID: 2, OptIn: false}}
	accounts := newTestService(&fakeCodexSource{}, now).classify(rows, config, now)

	got := map[string]string{}
	for _, ticket := range accounts[0].Tickets {
		got[ticket.Model] = ticket.State
	}
	if got["gpt-6-astra"] != model.TicketFresh || got["gpt-5.6-sol"] != model.TicketStale || got["gpt-6-terra"] != model.TicketNone {
		t.Fatalf("按模型分档错: %v", got)
	}
	if _, leaked := got["gpt-6-sol"]; leaked {
		t.Fatal("非门控模型的票不应出现在明细里")
	}
	if len(accounts[0].Tickets) != 3 {
		t.Fatalf("应按门控清单每模型一项，实际 %d", len(accounts[0].Tickets))
	}
	for _, ticket := range accounts[1].Tickets {
		if ticket.State != model.TicketOff {
			t.Fatalf("未 opt-in 应全为 off: %+v", ticket)
		}
	}
}

func TestOverviewNeverLeaksTicketState(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	source := &fakeCodexSource{
		settings: map[string]string{"openai_codex_ticket_enabled": "true", "openai_codex_ticket_models": "gpt-6-astra,gpt-5.6-sol"},
		accounts: []model.CodexTicketAccountRow{{ID: 1, Name: "a", OptIn: true, Schedulable: true, Status: "active",
			TicketsJSON: ticketsJSON(now, map[string][3]any{"gpt-6-astra": {292, 1, [2]time.Duration{-time.Minute, time.Hour}}})}},
	}
	overview, err := newTestService(source, now).Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", overview), "gAAAAA") {
		t.Fatal("turn-state blob 泄露到了看板响应里")
	}
}

func TestPerModelPoolsFloorAndDegradation(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	rows := []model.CodexTicketAccountRow{}
	// 67 组 8 个号：astra 全部持票；sol 只有 2 个持票 → sol 地板 5 击穿、astra 满足
	for id := int64(1); id <= 8; id++ {
		tickets := map[string][3]any{"gpt-6-astra": {292, 1, [2]time.Duration{-time.Minute, time.Hour}}}
		if id <= 2 {
			tickets["gpt-5.6-sol"] = [3]any{292, 1, [2]time.Duration{-time.Minute, time.Hour}}
		}
		rows = append(rows, model.CodexTicketAccountRow{ID: id, OptIn: true, Schedulable: true, Status: "active",
			Groups: []model.CodexGroupRef{{ID: 67, Name: "gpt-pro-pool-1"}}, TicketsJSON: ticketsJSON(now, tickets)})
	}
	h := now.Add(-time.Hour)
	source := &fakeCodexSource{
		accounts: rows,
		settings: map[string]string{
			"openai_codex_ticket_enabled": "true", "openai_codex_ticket_fail_closed": "true",
			"openai_codex_ticket_fail_closed_min_holders": "5", "openai_codex_ticket_models": "gpt-6-astra,gpt-5.6-sol",
		},
		usage: db.UsageAggregate{
			Total: map[db.UsagePair]db.UsageCounters{
				pair("gpt-6-astra", "gpt-6-astra"):  {Requests: 900},
				pair("gpt-6-astra", "gpt-5.6-luna"): {Requests: 100}, // 降智
				pair("gpt-5.6-sol", "gpt-5.6-sol"):  {Requests: 980},
				pair("gpt-5.6-sol", "gpt-6-sol"):    {Requests: 20}, // 升级，不算降智
			},
			ByAccount: map[db.UsagePair]map[int64]db.UsageCounters{
				pair("gpt-6-astra", "gpt-5.6-luna"): {1: {Requests: 100}},
				pair("gpt-5.6-sol", "gpt-6-sol"):    {1: {Requests: 20}},
			},
			ByHour: map[db.UsagePair]map[time.Time]db.UsageCounters{
				pair("gpt-6-astra", "gpt-6-astra"):  {h: {Requests: 900}},
				pair("gpt-6-astra", "gpt-5.6-luna"): {h: {Requests: 100}},
				pair("gpt-5.6-sol", "gpt-5.6-sol"):  {h: {Requests: 980}},
				pair("gpt-5.6-sol", "gpt-6-sol"):    {h: {Requests: 20}},
			},
		},
	}
	overview, err := newTestService(source, now).Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Models) != 2 || overview.Models[0].Model != "gpt-6-astra" || overview.Models[1].Model != "gpt-5.6-sol" {
		t.Fatalf("模型视图应按门控清单顺序: %+v", overview.Models)
	}
	astra, sol := overview.Models[0], overview.Models[1]
	if !astra.Pools[0].FloorSatisfied || astra.Pool.Holders != 8 {
		t.Fatalf("astra 应满足地板: %+v", astra.Pools[0])
	}
	if sol.Pools[0].FloorSatisfied || sol.Pool.Holders != 2 {
		t.Fatalf("sol 2/5 应判跌破: %+v", sol.Pools[0])
	}
	// 降智按谱系：astra 10%（luna 是跨系列），sol 0%（gpt-6-sol 是升级）
	if astra.Degradation.DegradedPercent != 10 {
		t.Fatalf("astra 降智率应 10%%，实际 %.2f", astra.Degradation.DegradedPercent)
	}
	if sol.Degradation.DegradedPercent != 0 || sol.Degradation.Upgraded != 20 {
		t.Fatalf("sol 升级不应算降智: %+v", sol.Degradation)
	}
	// 结论按模型分开，sol 有跌破 critical，astra 没有
	var solBreach, astraBreach bool
	for _, a := range overview.Advisories {
		if a.Category == "票池" && a.Level == "critical" {
			if a.Model == "gpt-5.6-sol" {
				solBreach = true
			}
			if a.Model == "gpt-6-astra" {
				astraBreach = true
			}
		}
	}
	if !solBreach || astraBreach {
		t.Fatalf("结论应只对 sol 报跌破: %+v", overview.Advisories)
	}
	// 账号明细里的用量按模型拆开
	var acct1 *model.CodexTicketAccount
	for i := range overview.Accounts {
		if overview.Accounts[i].ID == 1 {
			acct1 = &overview.Accounts[i]
		}
	}
	if acct1 == nil || len(acct1.Usage) != 2 || acct1.Requests != 120 || acct1.Degraded != 100 {
		t.Fatalf("账号用量拆分错: %+v", acct1)
	}
}

func TestZeroHoldersIsStartupNotBreach(t *testing.T) {
	// 刚加进门控清单的模型一张票都没有：应给 info「尚未启动」，不能喊 critical 跌破。
	now := time.Now().UTC()
	rows := []model.CodexTicketAccountRow{}
	for id := int64(1); id <= 6; id++ {
		rows = append(rows, model.CodexTicketAccountRow{ID: id, OptIn: true, Schedulable: true, Status: "active",
			Groups: []model.CodexGroupRef{{ID: 67, Name: "p1"}}, TicketsJSON: "{}"})
	}
	source := &fakeCodexSource{accounts: rows, settings: map[string]string{
		"openai_codex_ticket_enabled": "true", "openai_codex_ticket_fail_closed": "true",
		"openai_codex_ticket_fail_closed_min_holders": "5", "openai_codex_ticket_models": "gpt-6-terra",
	}}
	overview, err := newTestService(source, now).Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range overview.Advisories {
		if a.Category == "票池" && a.Level == "critical" {
			t.Fatalf("零持票应是 info 而非 critical: %+v", a)
		}
	}
}

func TestUsageCacheInvalidatesWhenGatedModelsChange(t *testing.T) {
	current := time.Now().UTC()
	source := &fakeCodexSource{settings: map[string]string{"openai_codex_ticket_enabled": "true", "openai_codex_ticket_models": "gpt-6-astra"}}
	// 票池 TTL 短、用量 TTL 长：只有清单变化才该让用量重算。
	svc := NewCodexService(source, CodexOptions{
		PoolCacheTTL: time.Second, UsageCacheTTL: time.Hour, UsageWindowHours: 24, TrendDays: 7,
		TrendRetention: time.Hour, DefaultModels: []string{"gpt-6-astra"}, Now: func() time.Time { return current },
	})
	if _, err := svc.Overview(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Overview(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, usageCalls, _ := source.calls(); usageCalls != 1 {
		t.Fatalf("清单没变，用量缓存应命中，实际打库 %d 次", usageCalls)
	}
	source.mu.Lock()
	source.settings = map[string]string{"openai_codex_ticket_enabled": "true", "openai_codex_ticket_models": "gpt-6-astra,gpt-5.6-sol"}
	source.mu.Unlock()
	current = current.Add(2 * time.Second) // 票池缓存过期，重读 settings；用量缓存本身未过期
	if _, err := svc.Overview(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, usageCalls, _ := source.calls()
	if usageCalls != 2 || len(source.lastModels) != 2 {
		t.Fatalf("清单变了应重算用量并带上新清单，实际 calls=%d models=%v", usageCalls, source.lastModels)
	}
}

func TestCachesAreIndependentAndSingleFlight(t *testing.T) {
	now := time.Now().UTC()
	source := &fakeCodexSource{settings: map[string]string{"openai_codex_ticket_enabled": "true"}}
	svc := newTestService(source, now)
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := svc.Overview(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	accountCalls, usageCalls, dailyCalls := source.calls()
	if accountCalls != 1 || usageCalls != 1 || dailyCalls != 0 {
		t.Fatalf("并发 8 个请求只应各打一次库且不触发 7 天扫描，实际 accounts=%d usage=%d daily=%d", accountCalls, usageCalls, dailyCalls)
	}
	if _, err := svc.DailyTrend(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, dailyCalls = source.calls(); dailyCalls != 1 {
		t.Fatalf("DailyTrend 应打一次库，实际 %d", dailyCalls)
	}
}

func TestDailyTrendSplitsByModelWithLineage(t *testing.T) {
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)
	source := &fakeCodexSource{
		settings: map[string]string{"openai_codex_ticket_enabled": "true", "openai_codex_ticket_models": "gpt-6-astra,gpt-5.6-sol"},
		daily: []db.DailyPairBucket{
			{Requested: "gpt-6-astra", Response: "gpt-6-astra", Day: day, Requests: 90},
			{Requested: "gpt-6-astra", Response: "gpt-5.6-luna", Day: day, Requests: 10},
			{Requested: "gpt-5.6-sol", Response: "gpt-6-sol", Day: day, Requests: 5},
			{Requested: "gpt-5.6-sol", Response: "gpt-5.6-sol", Day: day, Requests: 95},
		},
	}
	trend, err := newTestService(source, now).DailyTrend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]model.CodexDailyBucket{}
	for _, b := range trend.Buckets {
		got[b.Model] = b
	}
	if got["gpt-6-astra"].DegradedPercent != 10 || got["gpt-5.6-sol"].DegradedPercent != 0 {
		t.Fatalf("按天趋势谱系裁决错: %+v", got)
	}
}

func TestUsageFailureFallsBackToLastGoodData(t *testing.T) {
	base := time.Now().UTC()
	current := base
	source := &fakeCodexSource{
		settings: map[string]string{"openai_codex_ticket_enabled": "true"},
		usage:    db.UsageAggregate{Total: map[db.UsagePair]db.UsageCounters{pair("gpt-6-astra", "gpt-6-astra"): {Requests: 100}}},
	}
	svc := NewCodexService(source, CodexOptions{
		PoolCacheTTL: time.Minute, UsageCacheTTL: time.Minute, UsageWindowHours: 24, TrendDays: 7,
		TrendRetention: time.Hour, DefaultModels: []string{"gpt-6-astra"}, Now: func() time.Time { return current },
	})
	if _, err := svc.Overview(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	source.usageErr = errors.New("db down")
	source.mu.Unlock()
	current = base.Add(2 * time.Minute)
	overview, err := svc.Overview(context.Background())
	if err != nil {
		t.Fatalf("刷新失败时应回落: %v", err)
	}
	if overview.Models[0].Degradation.Requests != 100 || !overview.Freshness.Degraded {
		t.Fatalf("应展示上一份数据并标记 degraded: %+v", overview.Freshness)
	}
}

func TestTrendSamplerRecordsPerModel(t *testing.T) {
	current := time.Now().UTC()
	source := &fakeCodexSource{
		settings: map[string]string{"openai_codex_ticket_enabled": "true", "openai_codex_ticket_models": "gpt-6-astra,gpt-5.6-sol"},
		accounts: []model.CodexTicketAccountRow{{ID: 1, OptIn: true, Schedulable: true, Status: "active",
			TicketsJSON: ticketsJSON(current, map[string][3]any{"gpt-6-astra": {292, 1, [2]time.Duration{-time.Minute, time.Hour}}})}},
	}
	svc := NewCodexService(source, CodexOptions{
		PoolCacheTTL: time.Nanosecond, UsageCacheTTL: time.Minute, UsageWindowHours: 24, TrendDays: 7,
		TrendRetention: 10 * time.Minute, DefaultModels: []string{"gpt-6-astra"}, Now: func() time.Time { return current },
	})
	for i := 0; i < 5; i++ {
		if err := svc.SampleTrend(context.Background()); err != nil {
			t.Fatal(err)
		}
		current = current.Add(5 * time.Minute)
	}
	samples := svc.trendSamples()
	if len(samples) != 3 {
		t.Fatalf("10 分钟保留窗口下应剩 3 个采样点，实际 %d", len(samples))
	}
	if samples[0].Holders["gpt-6-astra"] != 1 || samples[0].Holders["gpt-5.6-sol"] != 0 {
		t.Fatalf("采样应按模型分开且清单内每模型都有键: %+v", samples[0].Holders)
	}
}
