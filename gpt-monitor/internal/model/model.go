package model

import "time"

type AccountRow struct {
	ID                    int64
	Name                  string
	Email                 string
	Status                string
	Schedulable           bool
	AccountExpiresAt      *time.Time
	SubscriptionExpiresAt *time.Time
	Codex5HUsedPercent    *float64
	Codex5HResetAt        *time.Time
	Codex5HWindowMinutes  int
	Codex7DUsedPercent    *float64
	Codex7DResetAt        *time.Time
	Codex7DWindowMinutes  int
	UsageSnapshotAt       *time.Time
	ErrorMessage          string
	RateLimitResetAt      *time.Time
	LastUsedAt            *time.Time
}

type UsageWindow struct {
	Available     bool       `json:"available"`
	UsedPercent   *float64   `json:"used_percent"`
	ResetsAt      *time.Time `json:"resets_at"`
	WindowMinutes int        `json:"window_minutes"`
}

type Account struct {
	ID                    int64       `json:"id"`
	Name                  string      `json:"name"`
	Email                 string      `json:"email"`
	Status                string      `json:"status"`
	Schedulable           bool        `json:"schedulable"`
	SubscriptionExpiresAt *time.Time  `json:"subscription_expires_at"`
	AccountExpiresAt      *time.Time  `json:"account_expires_at"`
	SecondsRemaining      int64       `json:"seconds_remaining"`
	DaysRemaining         int         `json:"days_remaining"`
	ExpiryLevel           string      `json:"expiry_level"`
	ExpiryConflict        bool        `json:"expiry_conflict"`
	UsageWarning          bool        `json:"usage_warning"`
	FiveHour              UsageWindow `json:"five_hour"`
	SevenDay              UsageWindow `json:"seven_day"`
	UsageSnapshotAt       *time.Time  `json:"usage_snapshot_at"`
	UsageStale            bool        `json:"usage_stale"`
	ErrorMessage          string      `json:"error_message,omitempty"`
	RateLimitResetAt      *time.Time  `json:"rate_limit_reset_at"`
	LastUsedAt            *time.Time  `json:"last_used_at"`
	AdminURL              string      `json:"admin_url"`
}

type Summary struct {
	Total            int `json:"total"`
	Expired          int `json:"expired"`
	Due3Days         int `json:"due_3_days"`
	Due7Days         int `json:"due_7_days"`
	Due14Days        int `json:"due_14_days"`
	Due30Days        int `json:"due_30_days"`
	Later            int `json:"later"`
	UnknownExpiry    int `json:"unknown_expiry"`
	StatusError      int `json:"status_error"`
	Unschedulable    int `json:"unschedulable"`
	ExpiryConflict   int `json:"expiry_conflict"`
	UsageWarning     int `json:"usage_warning"`
	UsageSnapshotGap int `json:"usage_snapshot_gap"`
}

type Snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	CachedAt    time.Time `json:"cached_at"`
	Summary     Summary   `json:"summary"`
	Accounts    []Account `json:"accounts"`
}
