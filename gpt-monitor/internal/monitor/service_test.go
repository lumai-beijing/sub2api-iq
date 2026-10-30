package monitor

import (
	"context"
	"testing"
	"time"

	"gpt-pro-monitor/internal/model"
)

type sourceStub struct {
	rows []model.AccountRow
	err  error
}

func (s sourceStub) ListProAccounts(context.Context) ([]model.AccountRow, error) {
	return s.rows, s.err
}

func TestBuildSnapshotClassificationAndSummary(t *testing.T) {
	now := time.Date(2026, 9, 11, 6, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Hour)
	critical := now.Add(3 * 24 * time.Hour)
	warning := now.Add(7 * 24 * time.Hour)
	attention := now.Add(14 * 24 * time.Hour)
	upcoming := now.Add(30 * 24 * time.Hour)
	fresh := now.Add(-10 * time.Minute)
	stale := now.Add(-61 * time.Minute)
	used90 := 90.0
	used99 := 99.0

	service := NewService(sourceStub{}, Options{
		UsageStaleAfter:       60 * time.Minute,
		UsageWarningThreshold: 90,
		CriticalExpiryDays:    3,
		WarningExpiryDays:     7,
		AttentionExpiryDays:   14,
		UpcomingExpiryDays:    30,
		AdminURL:              "https://example.test/admin/accounts",
		Now:                   func() time.Time { return now },
	})

	snapshot := service.BuildSnapshot([]model.AccountRow{
		{ID: 1, Name: "expired", SubscriptionExpiresAt: &expired, UsageSnapshotAt: &fresh},
		{ID: 2, Name: "critical", SubscriptionExpiresAt: &critical, UsageSnapshotAt: &fresh},
		{ID: 3, Name: "warning", SubscriptionExpiresAt: &warning, UsageSnapshotAt: &fresh},
		{ID: 4, Name: "attention", SubscriptionExpiresAt: &attention, UsageSnapshotAt: &fresh},
		{ID: 5, Name: "upcoming", SubscriptionExpiresAt: &upcoming, UsageSnapshotAt: &fresh},
		{ID: 6, Name: "usage", SubscriptionExpiresAt: &critical, AccountExpiresAt: &expired, Codex7DUsedPercent: &used90, Codex7DWindowMinutes: 10080, UsageSnapshotAt: &fresh},
		{ID: 7, Name: "stale", SubscriptionExpiresAt: &critical, Codex7DUsedPercent: &used99, Codex7DWindowMinutes: 10080, UsageSnapshotAt: &stale},
	}, now)

	levels := map[string]string{}
	for _, account := range snapshot.Accounts {
		levels[account.Name] = account.ExpiryLevel
	}
	expected := map[string]string{
		"expired":   "expired",
		"critical":  "critical",
		"warning":   "warning",
		"attention": "attention",
		"upcoming":  "upcoming",
	}
	for name, level := range expected {
		if levels[name] != level {
			t.Fatalf("%s level = %q, want %q", name, levels[name], level)
		}
	}

	if snapshot.Summary.Expired != 1 || snapshot.Summary.Due3Days != 3 || snapshot.Summary.Due7Days != 4 || snapshot.Summary.Due14Days != 5 || snapshot.Summary.Due30Days != 6 {
		t.Fatalf("unexpected summary: %+v", snapshot.Summary)
	}
	if snapshot.Summary.ExpiryConflict != 1 {
		t.Fatalf("expiry conflict = %d, want 1", snapshot.Summary.ExpiryConflict)
	}
	if snapshot.Summary.UsageWarning != 1 {
		t.Fatalf("usage warning = %d, want 1", snapshot.Summary.UsageWarning)
	}

	var staleAccount, usageAccount model.Account
	for _, account := range snapshot.Accounts {
		switch account.ID {
		case 6:
			usageAccount = account
		case 7:
			staleAccount = account
		}
	}
	if !usageAccount.UsageWarning {
		t.Fatal("fresh 90% usage should warn")
	}
	if staleAccount.UsageWarning || !staleAccount.UsageStale {
		t.Fatalf("stale usage must not warn: %+v", staleAccount)
	}
}

func TestMissingAndZeroUsageWindow(t *testing.T) {
	now := time.Date(2026, 9, 11, 6, 0, 0, 0, time.UTC)
	expiry := now.Add(24 * time.Hour)
	zero := 0.0
	service := NewService(sourceStub{}, Options{
		UsageStaleAfter:       60 * time.Minute,
		UsageWarningThreshold: 90,
		CriticalExpiryDays:    3,
		WarningExpiryDays:     7,
		AttentionExpiryDays:   14,
		UpcomingExpiryDays:    30,
	})
	snapshot := service.BuildSnapshot([]model.AccountRow{{
		ID:                    1,
		SubscriptionExpiresAt: &expiry,
		Codex5HUsedPercent:    &zero,
		Codex5HWindowMinutes:  0,
	}}, now)
	window := snapshot.Accounts[0].FiveHour
	if window.Available || window.WindowMinutes != 0 {
		t.Fatalf("zero-minute window must be unavailable: %+v", window)
	}
}
