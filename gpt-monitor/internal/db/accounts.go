package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"gpt-pro-monitor/internal/model"
)

// AccountRepository reads the small set of OpenAI Pro OAuth accounts needed by
// the monitor. It deliberately exposes no write methods.
type AccountRepository struct {
	pool *pgxpool.Pool
}

func NewAccountRepository(pool *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{pool: pool}
}

func (r *AccountRepository) ListProAccounts(ctx context.Context) ([]model.AccountRow, error) {
	const query = `
SELECT
	id,
	name,
	COALESCE(credentials->>'email', ''),
	status,
	schedulable,
	expires_at,
	COALESCE(credentials->>'subscription_expires_at', ''),
	COALESCE(extra->>'codex_5h_used_percent', ''),
	COALESCE(extra->>'codex_5h_reset_at', ''),
	COALESCE(extra->>'codex_5h_window_minutes', ''),
	COALESCE(extra->>'codex_7d_used_percent', ''),
	COALESCE(extra->>'codex_7d_reset_at', ''),
	COALESCE(extra->>'codex_7d_window_minutes', ''),
	COALESCE(extra->>'codex_usage_updated_at', ''),
	COALESCE(error_message, ''),
	rate_limit_reset_at,
	last_used_at
FROM accounts
WHERE platform = 'openai'
  AND type = 'oauth'
  AND deleted_at IS NULL
  AND parent_account_id IS NULL
  AND lower(credentials->>'plan_type') = 'pro'
ORDER BY id`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query pro accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]model.AccountRow, 0, 80)
	for rows.Next() {
		var (
			row                    model.AccountRow
			accountExpiresAt       pgtype.Timestamptz
			rateLimitResetAt       pgtype.Timestamptz
			lastUsedAt             pgtype.Timestamptz
			subscriptionExpiresRaw string
			codex5HUsedRaw         string
			codex5HResetRaw        string
			codex5HWindowRaw       string
			codex7DUsedRaw         string
			codex7DResetRaw        string
			codex7DWindowRaw       string
			usageSnapshotRaw       string
		)

		if err := rows.Scan(
			&row.ID,
			&row.Name,
			&row.Email,
			&row.Status,
			&row.Schedulable,
			&accountExpiresAt,
			&subscriptionExpiresRaw,
			&codex5HUsedRaw,
			&codex5HResetRaw,
			&codex5HWindowRaw,
			&codex7DUsedRaw,
			&codex7DResetRaw,
			&codex7DWindowRaw,
			&usageSnapshotRaw,
			&row.ErrorMessage,
			&rateLimitResetAt,
			&lastUsedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pro account: %w", err)
		}

		row.AccountExpiresAt = pgTime(accountExpiresAt)
		row.RateLimitResetAt = pgTime(rateLimitResetAt)
		row.LastUsedAt = pgTime(lastUsedAt)
		row.SubscriptionExpiresAt = parseTime(subscriptionExpiresRaw)
		row.Codex5HUsedPercent = parseFloat(codex5HUsedRaw)
		row.Codex5HResetAt = parseTime(codex5HResetRaw)
		row.Codex5HWindowMinutes = parseInt(codex5HWindowRaw)
		row.Codex7DUsedPercent = parseFloat(codex7DUsedRaw)
		row.Codex7DResetAt = parseTime(codex7DResetRaw)
		row.Codex7DWindowMinutes = parseInt(codex7DWindowRaw)
		row.UsageSnapshotAt = parseTime(usageSnapshotRaw)

		accounts = append(accounts, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro accounts: %w", err)
	}
	return accounts, nil
}

func pgTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func parseFloat(raw string) *float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &value
}

func parseInt(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func parseTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if value, err := time.Parse(layout, raw); err == nil {
			return &value
		}
	}
	return nil
}
