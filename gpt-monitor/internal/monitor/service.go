package monitor

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"gpt-pro-monitor/internal/model"
)

type AccountSource interface {
	ListProAccounts(ctx context.Context) ([]model.AccountRow, error)
}

type Options struct {
	CacheTTL              time.Duration
	UsageStaleAfter       time.Duration
	UsageWarningThreshold float64
	CriticalExpiryDays    int
	WarningExpiryDays     int
	AttentionExpiryDays   int
	UpcomingExpiryDays    int
	AdminURL              string
	Now                   func() time.Time
}

type Service struct {
	source  AccountSource
	options Options

	mu         sync.RWMutex
	refreshMu  sync.Mutex
	cached     model.Snapshot
	cacheUntil time.Time
}

func NewService(source AccountSource, options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{source: source, options: options}
}

func (s *Service) Snapshot(ctx context.Context) (model.Snapshot, error) {
	now := s.options.Now().UTC()

	s.mu.RLock()
	if !s.cacheUntil.IsZero() && now.Before(s.cacheUntil) {
		snapshot := cloneSnapshot(s.cached)
		snapshot.CachedAt = now
		s.mu.RUnlock()
		return snapshot, nil
	}
	s.mu.RUnlock()

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	now = s.options.Now().UTC()
	s.mu.RLock()
	if !s.cacheUntil.IsZero() && now.Before(s.cacheUntil) {
		snapshot := cloneSnapshot(s.cached)
		snapshot.CachedAt = now
		s.mu.RUnlock()
		return snapshot, nil
	}
	s.mu.RUnlock()

	rows, err := s.source.ListProAccounts(ctx)
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("load pro accounts: %w", err)
	}

	snapshot := s.BuildSnapshot(rows, now)
	s.mu.Lock()
	s.cached = cloneSnapshot(snapshot)
	s.cacheUntil = now.Add(s.options.CacheTTL)
	s.mu.Unlock()

	return snapshot, nil
}

func (s *Service) BuildSnapshot(rows []model.AccountRow, now time.Time) model.Snapshot {
	now = now.UTC()
	accounts := make([]model.Account, 0, len(rows))
	summary := model.Summary{Total: len(rows)}

	for _, row := range rows {
		account := s.buildAccount(row, now)
		accounts = append(accounts, account)

		switch account.ExpiryLevel {
		case "expired":
			summary.Expired++
		case "unknown":
			summary.UnknownExpiry++
		}

		if account.SubscriptionExpiresAt != nil && account.SubscriptionExpiresAt.After(now) {
			remaining := account.SubscriptionExpiresAt.Sub(now)
			if remaining <= days(s.options.CriticalExpiryDays) {
				summary.Due3Days++
			}
			if remaining <= days(s.options.WarningExpiryDays) {
				summary.Due7Days++
			}
			if remaining <= days(s.options.AttentionExpiryDays) {
				summary.Due14Days++
			}
			if remaining <= days(s.options.UpcomingExpiryDays) {
				summary.Due30Days++
			} else {
				summary.Later++
			}
		}

		if account.Status == "error" {
			summary.StatusError++
		}
		if !account.Schedulable {
			summary.Unschedulable++
		}
		if account.ExpiryConflict {
			summary.ExpiryConflict++
		}
		if account.UsageWarning {
			summary.UsageWarning++
		}
		if account.UsageSnapshotAt == nil {
			summary.UsageSnapshotGap++
		}
	}

	sort.SliceStable(accounts, func(i, j int) bool {
		left, right := accounts[i], accounts[j]
		if levelRank(left.ExpiryLevel) != levelRank(right.ExpiryLevel) {
			return levelRank(left.ExpiryLevel) < levelRank(right.ExpiryLevel)
		}
		if left.ExpiryConflict != right.ExpiryConflict {
			return left.ExpiryConflict
		}
		if left.Status != right.Status {
			return left.Status == "error"
		}
		if left.UsageWarning != right.UsageWarning {
			return left.UsageWarning
		}
		if left.SubscriptionExpiresAt == nil {
			return false
		}
		if right.SubscriptionExpiresAt == nil {
			return true
		}
		if !left.SubscriptionExpiresAt.Equal(*right.SubscriptionExpiresAt) {
			return left.SubscriptionExpiresAt.Before(*right.SubscriptionExpiresAt)
		}
		return left.ID < right.ID
	})

	return model.Snapshot{
		GeneratedAt: now,
		CachedAt:    now,
		Summary:     summary,
		Accounts:    accounts,
	}
}

func (s *Service) buildAccount(row model.AccountRow, now time.Time) model.Account {
	account := model.Account{
		ID:                    row.ID,
		Name:                  row.Name,
		Email:                 row.Email,
		Status:                row.Status,
		Schedulable:           row.Schedulable,
		SubscriptionExpiresAt: row.SubscriptionExpiresAt,
		AccountExpiresAt:      row.AccountExpiresAt,
		ExpiryLevel:           s.expiryLevel(row.SubscriptionExpiresAt, now),
		FiveHour: model.UsageWindow{
			Available:     row.Codex5HWindowMinutes > 0,
			UsedPercent:   row.Codex5HUsedPercent,
			ResetsAt:      row.Codex5HResetAt,
			WindowMinutes: row.Codex5HWindowMinutes,
		},
		SevenDay: model.UsageWindow{
			Available:     row.Codex7DWindowMinutes > 0,
			UsedPercent:   row.Codex7DUsedPercent,
			ResetsAt:      row.Codex7DResetAt,
			WindowMinutes: row.Codex7DWindowMinutes,
		},
		UsageSnapshotAt:  row.UsageSnapshotAt,
		ErrorMessage:     row.ErrorMessage,
		RateLimitResetAt: row.RateLimitResetAt,
		LastUsedAt:       row.LastUsedAt,
		AdminURL:         s.options.AdminURL,
	}

	if row.SubscriptionExpiresAt != nil {
		account.SecondsRemaining = int64(row.SubscriptionExpiresAt.Sub(now).Seconds())
		account.DaysRemaining = int(row.SubscriptionExpiresAt.Sub(now).Hours() / 24)
	}
	account.ExpiryConflict = row.SubscriptionExpiresAt != nil && row.AccountExpiresAt != nil && row.AccountExpiresAt.Before(*row.SubscriptionExpiresAt)
	account.UsageStale = row.UsageSnapshotAt == nil || now.Sub(*row.UsageSnapshotAt) > s.options.UsageStaleAfter
	account.UsageWarning = !account.UsageStale && highUsage(row.Codex5HUsedPercent, row.Codex5HWindowMinutes, s.options.UsageWarningThreshold) ||
		!account.UsageStale && highUsage(row.Codex7DUsedPercent, row.Codex7DWindowMinutes, s.options.UsageWarningThreshold)
	return account
}

func (s *Service) expiryLevel(expiry *time.Time, now time.Time) string {
	if expiry == nil {
		return "unknown"
	}
	if !expiry.After(now) {
		return "expired"
	}
	switch {
	case !expiry.After(now.Add(days(s.options.CriticalExpiryDays))):
		return "critical"
	case !expiry.After(now.Add(days(s.options.WarningExpiryDays))):
		return "warning"
	case !expiry.After(now.Add(days(s.options.AttentionExpiryDays))):
		return "attention"
	case !expiry.After(now.Add(days(s.options.UpcomingExpiryDays))):
		return "upcoming"
	default:
		return "normal"
	}
}

func highUsage(used *float64, windowMinutes int, threshold float64) bool {
	return windowMinutes > 0 && used != nil && *used >= threshold
}

func days(value int) time.Duration {
	return time.Duration(value) * 24 * time.Hour
}

func levelRank(level string) int {
	switch level {
	case "expired":
		return 0
	case "critical":
		return 1
	case "warning":
		return 2
	case "attention":
		return 3
	case "upcoming":
		return 4
	case "normal":
		return 5
	default:
		return 6
	}
}

func cloneSnapshot(snapshot model.Snapshot) model.Snapshot {
	snapshot.Accounts = append([]model.Account(nil), snapshot.Accounts...)
	return snapshot
}
