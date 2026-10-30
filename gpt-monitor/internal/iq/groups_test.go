package iq

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-pro-monitor/internal/model"
)

// groupFixture：20 个 OAuth 号。#1–#10 在分组 6，#11–#12 在分组 40，#13 同时在 6 和 25，
// 另有一个只在分组 40 里的 API Key 号 #99。
func groupFixture() *fakeCandidates {
	rows := []model.IQCandidate{}
	for i := int64(1); i <= 20; i++ {
		row := model.IQCandidate{ID: i, Name: "acct-" + itoa(i), Type: model.IQAccountOAuth}
		switch {
		case i <= 10:
			row.GroupIDs = []int64{6}
		case i <= 12:
			row.GroupIDs = []int64{40}
		case i == 13:
			row.GroupIDs = []int64{6, 25}
		}
		rows = append(rows, row)
	}
	rows = append(rows, model.IQCandidate{ID: 99, Name: "official-key", Type: model.IQAccountAPIKey, GroupIDs: []int64{40}})
	return &fakeCandidates{rows: rows, groups: []model.IQGroupInfo{
		{ID: 6, Name: "gpt-pro-pool-2-auto", Status: "active", Members: 11, OAuth: 11, Available: 11, Requests24h: 7764},
		{ID: 25, Name: "gpt-pro-3", Status: "active", Members: 1, OAuth: 1, Available: 1},
		{ID: 40, Name: "gpt-pro-pool-3", Status: "active", Members: 3, OAuth: 2, APIKey: 1, Available: 3, Requests24h: 2878},
	}}
}

func newGroupService(t *testing.T, candidates *fakeCandidates) (*Service, *fakeTester, *clock) {
	t.Helper()
	c := &clock{now: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)}
	tester := &fakeTester{version: "lmuai-0.2.13.2", answers: map[int64]string{}}
	for _, row := range candidates.rows {
		tester.answers[row.ID] = `{"answer": 21, "reasoning": "圆 9 + 星 12"}`
	}
	svc := newTestIQService(t, candidates, tester, []ModelSpec{{Model: "gpt-6-astra", ReasoningEffort: "medium"}}, c)
	ctx := context.Background()
	count, interval := model.IQSampleCount, 300
	two := 2
	if _, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Schedule: &model.IQScheduleUpdate{SampleMode: &count, SampleCount: &two, IntervalSeconds: &interval}}); err != nil {
		t.Fatal(err)
	}
	return svc, tester, c
}

func selectGroups(t *testing.T, svc *Service, perRound int, ids ...int64) {
	t.Helper()
	if _, err := svc.UpdateSettings(context.Background(), model.IQSettingsRequest{Groups: &model.IQGroupUpdate{IDs: &ids, PerRound: &perRound}}); err != nil {
		t.Fatal(err)
	}
}

func TestGroupRoundTopsUpSelectedGroups(t *testing.T) {
	ctx := context.Background()
	candidates := groupFixture()
	svc, tester, _ := newGroupService(t, candidates)
	selectGroups(t, svc, 2, 40, 6)

	runRound(ctx, svc)
	_, ids := tester.snapshot()
	inGroup := map[int64]int{}
	seen := map[int64]bool{}
	byID := map[int64]model.IQCandidate{}
	for _, row := range candidates.rows {
		byID[row.ID] = row
	}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("一轮里不应重复测同一个号: %v", ids)
		}
		seen[id] = true
		for _, g := range byID[id].GroupIDs {
			inGroup[g]++
		}
	}
	if inGroup[40] < 2 || inGroup[6] < 2 {
		t.Fatalf("每个选中的分组这一轮至少测 2 个号: %v（测了 %v）", inGroup, ids)
	}
	if len(ids) > 2+4 {
		t.Fatalf("号池抽 2 个 + 两个分组最多各补 2 个，got %d", len(ids))
	}

	// 号池整体只算按比例抽样的 OAuth 号：为分组补测的、API Key 号都不算。
	pool, _ := svc.store.RecentProbes(ctx, ProbeFilter{Source: PoolSource, Limit: 100})
	all, _ := svc.store.RecentProbes(ctx, ProbeFilter{Source: AccountsSource, Limit: 100})
	if len(pool) != 2 || len(all) != len(ids) {
		t.Fatalf("号池整体应只有 2 条抽样，全部号 %d 条: pool=%d all=%d", len(ids), len(pool), len(all))
	}
	for _, p := range pool {
		if p.SampleGroupID != 0 || p.AccountType != model.IQAccountOAuth || p.Prompt == "" || p.QuestionVariant == "" {
			t.Fatalf("号池抽样记录字段错: %+v", p)
		}
	}
	topUps := 0
	for _, p := range all {
		if p.SampleGroupID > 0 {
			topUps++
			if !containsID(byID[p.AccountID].GroupIDs, p.SampleGroupID) {
				t.Fatalf("补测的号应属于它补测的分组: %+v", p)
			}
		}
	}
	if topUps != len(ids)-2 {
		t.Fatalf("补测条数应等于总数减号池抽样: %d", topUps)
	}
	overview, _ := svc.Overview(ctx)
	if overview.Schedule.LastRoundPool != 2 || overview.Schedule.LastRoundTopUps != topUps || overview.Schedule.Groups != 2 {
		t.Fatalf("看板应展示这一轮的构成: %+v", overview.Schedule)
	}

	// 矩阵：号池、分组 40、分组 6（按选择的顺序），分组名从 sub2api 读。
	rows := overview.Board.Rows
	if len(rows) != 3 || rows[1].Key != "group:40" || rows[1].Name != "gpt-pro-pool-3" || rows[2].Key != "group:6" {
		t.Fatalf("矩阵的行错: %+v", rows)
	}
	if rows[1].Cells[0].Stats.Graded < 2 || !strings.Contains(rows[1].Detail, "API Key") {
		t.Fatalf("分组 40 应有本轮的结果，并提示组里有 API Key 号: %+v", rows[1])
	}
}

// 分组的归属按检测那一刻记：号以后换了分组，已有的记录仍算在原来的分组上。
func TestGroupAttributionSurvivesMembershipChange(t *testing.T) {
	ctx := context.Background()
	candidates := groupFixture()
	svc, _, c := newGroupService(t, candidates)
	if _, err := svc.RunNow(ctx, model.IQRunRequest{AccountID: 13}); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	candidates.rows[12].GroupIDs = []int64{40} // #13 从 6、25 挪到 40
	c.Add(time.Minute)
	if _, err := svc.RunNow(ctx, model.IQRunRequest{AccountID: 13}); err != nil {
		t.Fatal(err)
	}
	svc.Wait()

	count := func(group int64) int {
		probes, err := svc.store.RecentProbes(ctx, ProbeFilter{Source: GroupSource(group), Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		return len(probes)
	}
	if count(6) != 1 || count(25) != 1 || count(40) != 1 {
		t.Fatalf("应是 6、25 各 1 条（挪走前），40 一条（挪过去后）: 6=%d 25=%d 40=%d", count(6), count(25), count(40))
	}
	inGroup, err := svc.Accounts(ctx, "all", 6)
	if err != nil || len(inGroup.Accounts) != 1 || inGroup.Accounts[0].Graded != 1 {
		t.Fatalf("按分组看账号排行，只算检测时属于该分组的记录: %+v %v", inGroup.Accounts, err)
	}
	if groups := inGroup.Accounts[0].Groups; len(groups) != 1 || groups[0].ID != 40 || groups[0].Name != "gpt-pro-pool-3" {
		t.Fatalf("账号排行展示的是号当前所在的分组: %+v", groups)
	}
	probes, _ := svc.Probes(ctx, model.IQProbeQuery{Source: "group:25"})
	if len(probes.Probes) != 1 || len(probes.Probes[0].GroupIDs) != 2 {
		t.Fatalf("采样详情应带检测时的分组: %+v", probes.Probes)
	}
}

func TestGroupRunNowPicksFromTheGroup(t *testing.T) {
	ctx := context.Background()
	svc, tester, _ := newGroupService(t, groupFixture())
	selectGroups(t, svc, 3, 40)
	response, err := svc.RunNow(ctx, model.IQRunRequest{GroupID: 40})
	if err != nil || response.Kind != "group" || response.Targets != 3 {
		t.Fatalf("分组 40 有 3 个可用号，应全测: %+v %v", response, err)
	}
	svc.Wait()
	_, ids := tester.snapshot()
	for _, id := range ids {
		if id != 11 && id != 12 && id != 99 {
			t.Fatalf("只应测分组 40 的号（含 API Key 号）: %v", ids)
		}
	}
	all, _ := svc.store.RecentProbes(ctx, ProbeFilter{Source: AccountsSource, Limit: 10})
	for _, p := range all {
		if p.SampleGroupID != 40 || p.Trigger != "manual" {
			t.Fatalf("分组立即检测应记为该分组的补测: %+v", p)
		}
		if p.AccountID == 99 && p.AccountType != model.IQAccountAPIKey {
			t.Fatalf("API Key 号的类型要记下来: %+v", p)
		}
	}
	if _, err := svc.RunNow(ctx, model.IQRunRequest{GroupID: 77}); err == nil {
		t.Fatalf("没有可用号的分组应报错")
	}
}

func TestGroupSettingsValidation(t *testing.T) {
	ctx := context.Background()
	candidates := groupFixture()
	svc, _, _ := newGroupService(t, candidates)
	bad := func(ids []int64, perRound int, status int) {
		t.Helper()
		_, err := svc.UpdateSettings(ctx, model.IQSettingsRequest{Groups: &model.IQGroupUpdate{IDs: &ids, PerRound: &perRound}})
		var iqErr *model.IQError
		if !errors.As(err, &iqErr) || iqErr.Status != status {
			t.Fatalf("ids=%v per_round=%d 应返回 %d，got %v", ids, perRound, status, err)
		}
	}
	bad([]int64{999}, 2, http.StatusBadRequest) // sub2api 里没有
	bad([]int64{6, 6}, 2, http.StatusBadRequest)
	bad([]int64{6}, 0, http.StatusBadRequest)
	bad([]int64{6}, maxGroupPerRound+1, http.StatusBadRequest)
	selectGroups(t, svc, 2, 6, 40)

	// sub2api 里删掉了分组 40：照样能取消勾选；但读不到分组列表时不能新加。
	candidates.groups = candidates.groups[:2]
	candidates.groupsErr = errors.New("connection refused")
	selectGroups(t, svc, 2, 6)
	bad([]int64{6, 25}, 2, http.StatusServiceUnavailable)
	settings, _ := svc.Settings(ctx)
	if len(settings.Groups.IDs) != 1 || settings.Groups.IDs[0] != 6 {
		t.Fatalf("取消勾选应生效: %+v", settings.Groups)
	}

	// 选中的分组在 sub2api 里没了 → 分组列表标 missing，矩阵给一条提醒。
	candidates.groupsErr = nil
	if err := svc.store.SetSetting(ctx, settingGroups, `{"ids":[6,40],"per_round":2}`); err != nil {
		t.Fatal(err)
	}
	groups, err := svc.Groups(ctx, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if groups.Groups[0].ID != 6 || !groups.Groups[0].Selected || groups.Groups[1].ID != 40 || !groups.Groups[1].Missing {
		t.Fatalf("选中的在前，sub2api 里没了的标 missing: %+v", groups.Groups)
	}
	board, _ := svc.Board(ctx, "", "")
	found := false
	for _, a := range board.Advisories {
		if a.SourceKey == "group:40" && strings.Contains(a.Title, "已经没有") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应提醒分组已不存在: %+v", board.Advisories)
	}
}

// 题面随机化前后对比：固定题面时期全对、随机题面后大多答错 → 提醒可能是缓存。
func TestVariantAdvisoryFlagsCachedProviders(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)}
	svc, _ := newProviderService(t, c)
	input := providerInput("缓存中转", "https://cache.example.com/v1")
	input.Models = "gpt-6-astra@xhigh"
	cached, err := svc.CreateProvider(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	steadyInput := providerInput("老实中转", "https://honest.example.com/v1")
	steadyInput.Models = "gpt-6-astra@xhigh"
	steady, err := svc.CreateProvider(ctx, steadyInput)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(providerID int64, question string, correct, wrong int, at time.Time) {
		for i := 0; i < correct+wrong; i++ {
			outcome := OutcomeCorrect
			if i >= correct {
				outcome = OutcomeWrong
			}
			if _, err := svc.store.InsertProbe(ctx, Probe{ProviderID: providerID, RunID: "r", AccountName: "x", Model: "gpt-6-astra",
				ReasoningEffort: "xhigh", QuestionID: question, Outcome: outcome, Trigger: "schedule", CreatedAt: at.Add(time.Duration(i) * time.Second)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, after := c.Now().Add(-48*time.Hour), c.Now().Add(-time.Hour)
	insert(cached.ID, fixedCandyQuestionID, 30, 0, before)
	insert(cached.ID, CandyQuestion.ID, 9, 21, after)
	insert(steady.ID, fixedCandyQuestionID, 27, 3, before)
	insert(steady.ID, CandyQuestion.ID, 26, 4, after)

	board, err := svc.Board(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var drop, ok *model.IQAdvisory
	for i, a := range board.Advisories {
		switch {
		case a.Title == "题面随机化后答对率明显下降":
			drop = &board.Advisories[i]
		case a.Title == "题面随机化前后答对率没有明显变化":
			ok = &board.Advisories[i]
		}
	}
	if drop == nil || drop.SourceKey != "provider:"+itoa(cached.ID) || !strings.Contains(drop.Evidence, "100%") || !strings.Contains(drop.Evidence, "30%") {
		t.Fatalf("缓存中转应被点名: %+v", board.Advisories)
	}
	if ok == nil || !strings.Contains(ok.Evidence, "老实中转") || strings.Contains(ok.Evidence, "缓存中转") {
		t.Fatalf("老实中转应归入「没有明显变化」: %+v", ok)
	}

	// 只看随机题面：缓存中转的格子按随机题面算（30%），固定题面口径则是 100%。
	random, _ := svc.Board(ctx, "all", QuestionScopeRandom)
	fixed, _ := svc.Board(ctx, "all", QuestionScopeFixed)
	if *random.Rows[1].Cells[0].Stats.AccuracyPercent != 30 || *fixed.Rows[1].Cells[0].Stats.AccuracyPercent != 100 {
		t.Fatalf("题面口径应分开统计: random=%+v fixed=%+v", random.Rows[1].Cells[0].Stats, fixed.Rows[1].Cells[0].Stats)
	}
	if _, err := svc.Board(ctx, "1y", ""); err == nil {
		t.Fatalf("非法窗口应报错")
	}
	if _, err := svc.Board(ctx, "", "both"); err == nil {
		t.Fatalf("非法题面口径应报错")
	}
}

func TestStorePruneCascadesGroupMemberships(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	old, err := store.InsertProbe(ctx, Probe{RunID: "r", AccountID: 1, AccountName: "a", Model: "m", QuestionID: CandyQuestion.ID,
		Outcome: OutcomeCorrect, Trigger: "schedule", GroupIDs: []int64{6, 6, 25}, CreatedAt: base.Add(-100 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertProbe(ctx, Probe{RunID: "r", AccountID: 2, AccountName: "b", Model: "m", QuestionID: CandyQuestion.ID,
		Outcome: OutcomeCorrect, Trigger: "schedule", GroupIDs: []int64{6}, CreatedAt: base}); err != nil {
		t.Fatal(err)
	}
	probes := []Probe{{ID: old}}
	if err := store.LoadGroups(ctx, probes); err != nil || len(probes[0].GroupIDs) != 2 {
		t.Fatalf("重复的分组只存一次: %+v %v", probes[0].GroupIDs, err)
	}
	if removed, err := store.Prune(ctx, base.Add(-90*24*time.Hour)); err != nil || removed != 1 {
		t.Fatalf("应清掉 1 条过期记录: %d %v", removed, err)
	}
	var left int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM iq_probe_groups`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("过期记录的分组归属应级联删掉，剩 %d 条 %v", left, err)
	}
}

func TestParseSource(t *testing.T) {
	for key, want := range map[string]Source{
		"": AccountsSource, "accounts": AccountsSource, "pool": PoolSource, "group:6": GroupSource(6), "provider:12": ProviderSource(12),
	} {
		got, err := ParseSource(key)
		if err != nil || got != want {
			t.Errorf("ParseSource(%q) = %+v %v", key, got, err)
		}
		if key != "" && got.Key() != key {
			t.Errorf("Key() 应能原样还原 %q，got %q", key, got.Key())
		}
	}
	for _, bad := range []string{"group:", "group:0", "provider:-1", "team:3", "group:abc"} {
		if _, err := ParseSource(bad); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}
