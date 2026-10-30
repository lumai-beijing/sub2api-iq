package iq

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "iq.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestStoreTallyCountsErrorsSeparately(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	injected := true
	rows := []Probe{
		{AccountID: 1, AccountName: "old-name", Model: "gpt-6-astra", ReasoningEffort: "xhigh", Outcome: OutcomeCorrect, AnswerFormat: FormatJSON, DurationMs: 1000, CreatedAt: base},
		{AccountID: 1, AccountName: "new-name", Model: "gpt-6-astra", ReasoningEffort: "xhigh", Outcome: OutcomeWrong, AnswerFormat: FormatText, DurationMs: 3000, ModelVerdict: "degraded", CreatedAt: base.Add(time.Minute), TicketInjected: &injected},
		{AccountID: 1, AccountName: "new-name", Model: "gpt-6-astra", ReasoningEffort: "xhigh", Outcome: OutcomeError, DurationMs: 90000, CreatedAt: base.Add(2 * time.Minute)},
		{AccountID: 1, AccountName: "new-name", Model: "gpt-6-astra", ReasoningEffort: "high", Outcome: OutcomeCorrect, AnswerFormat: FormatJSON, DurationMs: 800, CreatedAt: base.Add(150 * time.Second)},
		{AccountID: 1, AccountName: "new-name", Model: "gpt-5.6-sol", ReasoningEffort: "max", Outcome: OutcomeCorrect, AnswerFormat: FormatJSON, DurationMs: 500, CreatedAt: base.Add(3 * time.Minute)},
		{AccountID: 2, AccountName: "b", Model: "gpt-6-astra", ReasoningEffort: "xhigh", Outcome: OutcomeNoAnswer, AnswerFormat: FormatNone, DurationMs: 200, CreatedAt: base.Add(-48 * time.Hour)},
	}
	for _, p := range rows {
		p.RunID, p.QuestionID, p.Trigger = "r", CandyQuestion.ID, "schedule"
		if _, err := store.InsertProbe(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	// 另一道题的记录不应混进统计
	if _, err := store.InsertProbe(ctx, Probe{RunID: "r", AccountID: 1, AccountName: "new-name", Model: "gpt-6-astra", ReasoningEffort: "xhigh",
		QuestionID: "candy-v1", Outcome: OutcomeWrong, Trigger: "schedule", CreatedAt: base.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	tallies, err := store.TallyByAccount(ctx, base.Add(-time.Hour), []string{CandyQuestion.ID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tallies) != 3 {
		t.Fatalf("窗口内 1 号应有 3 组（astra@xhigh、astra@high、sol@max），got %+v", tallies)
	}
	var astra AccountTally
	for _, item := range tallies {
		if item.Model == "gpt-6-astra" && item.ReasoningEffort == "xhigh" {
			astra = item
		}
	}
	if astra.AccountName != "new-name" {
		t.Errorf("账号名应取最新一次的，got %q", astra.AccountName)
	}
	if astra.Correct != 1 || astra.Wrong != 1 || astra.Errors != 1 || astra.Graded() != 2 || astra.Swapped != 1 || astra.JSONFormat != 1 {
		t.Errorf("计数错: %+v", astra.Tally)
	}
	if astra.DurationN != 2 || astra.DurationSum != 4000 {
		t.Errorf("耗时不应计入 error 那次: %+v", astra.Tally)
	}

	all, err := store.TallyByAccount(ctx, time.Time{}, []string{CandyQuestion.ID}, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("全部历史应有 4 组，got %d err=%v", len(all), err)
	}

	xhigh := "xhigh"
	recent, err := store.RecentProbes(ctx, ProbeFilter{AccountID: 1, Model: "gpt-6-astra", Effort: &xhigh, QuestionIDs: []string{CandyQuestion.ID}, Limit: 10})
	if err != nil || len(recent) != 3 || recent[0].Outcome != OutcomeError {
		t.Fatalf("最近记录应按时间倒序、按推理强度过滤: %+v err=%v", recent, err)
	}
	if recent[1].TicketInjected == nil || !*recent[1].TicketInjected || recent[0].TicketInjected != nil {
		t.Errorf("ticket_injected 三态没保住: %+v / %+v", recent[1].TicketInjected, recent[0].TicketInjected)
	}
	if recent[2].AnswerFormat != FormatJSON {
		t.Errorf("answer_format 没存住: %+v", recent[2])
	}

	last, err := store.LastProbedAt(ctx)
	if err != nil || !last[1].Equal(base.Add(3*time.Minute)) || !last[2].Equal(base.Add(-48*time.Hour)) {
		t.Fatalf("LastProbedAt 错: %v err=%v", last, err)
	}

	pruned, err := store.Prune(ctx, base.Add(-24*time.Hour))
	if err != nil || pruned != 1 {
		t.Fatalf("应删掉 48h 前那条，pruned=%d err=%v", pruned, err)
	}
}

func TestStoreMigratesOldSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// 没有 answer_format / answer_reasoning 两列的老表
	if _, err := db.Exec(`CREATE TABLE iq_probes (
		id INTEGER PRIMARY KEY, run_id TEXT NOT NULL, account_id INTEGER NOT NULL, account_name TEXT NOT NULL,
		model TEXT NOT NULL, reasoning_effort TEXT NOT NULL, question_id TEXT NOT NULL, outcome TEXT NOT NULL,
		answer_value TEXT NOT NULL DEFAULT '', answer_excerpt TEXT NOT NULL DEFAULT '', upstream_model TEXT NOT NULL DEFAULT '',
		model_verdict TEXT NOT NULL DEFAULT '', ticket_injected INTEGER, error_message TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER NOT NULL DEFAULT 0, trigger TEXT NOT NULL, created_at INTEGER NOT NULL);
		INSERT INTO iq_probes (run_id, account_id, account_name, model, reasoning_effort, question_id, outcome, trigger, created_at)
		VALUES ('r', 1, 'a', 'm', '', 'q', 'correct', 'schedule', 1);`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("老库应能自动补列: %v", err)
	}
	defer store.Close()
	probes, err := store.RecentProbes(context.Background(), ProbeFilter{Limit: 5})
	if err != nil || len(probes) != 1 || probes[0].AnswerFormat != "" {
		t.Fatalf("补列后老数据应可读: %+v err=%v", probes, err)
	}
}

func TestStoreSettingsUpsert(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if _, ok, err := store.GetSetting(ctx, "models"); ok || err != nil {
		t.Fatalf("空库不应有设置: ok=%v err=%v", ok, err)
	}
	if err := store.SetSetting(ctx, "models", "a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, "models", "b"); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := store.GetSetting(ctx, "models"); !ok || err != nil || value != "b" {
		t.Fatalf("upsert 失败: %q %v %v", value, ok, err)
	}
}
