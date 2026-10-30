package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"gpt-pro-monitor/internal/model"
)

// CodexRepository 只读 sub2api 的账号 / 用量 / 打票配置。
//
// 性能约定（46 上 usage_logs 是 5.6GB / 410 万行）：
//   - 只用 idx_usage_logs_effective_requested_model_created 这条索引；
//     谓词必须写成 coalesce(nullif(btrim(requested_model),”), model) = ANY($models)
//     才命中，改写任何一处都会退化成全表扫。实测 2 个模型 24h ≈ 96ms / 4.3MB。
//   - 用量侧一次请求只扫一遍表，靠 GROUPING SETS 同时出逐账号 / 逐小时 / 总计，
//     且按「请求模型 → 上游返回模型」成对聚合，降智裁决在服务层用谱系规则做，
//     不在 SQL 里写死模型名。
//   - 窗口上限 24 小时；7 天按天的那份要读 ~280MB 堆页，只给按需面板用。
type CodexRepository struct {
	pool     *pgxpool.Pool
	timezone string
}

func NewCodexRepository(pool *pgxpool.Pool, timezone string) *CodexRepository {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	return &CodexRepository{pool: pool, timezone: timezone}
}

// ---------- 票池 / 账号 ----------

// 票全部取出来（extra 里所有 codex_turn_ticket:* 键），按模型拆分在服务层做。
// 这样门控清单变化不用改查询，也不会漏掉刚加进清单的模型已经采到的票。
const ticketAccountsQuery = `
SELECT
	a.id,
	a.name,
	COALESCE(a.credentials->>'email', ''),
	a.status,
	a.schedulable,
	lower(COALESCE(a.extra->>'codex_ticket_enabled', '')) IN ('true','1','t','yes'),
	COALESCE((
		SELECT jsonb_object_agg(e.key, e.value)::text
		FROM jsonb_each(COALESCE(a.extra, '{}'::jsonb)) e
		WHERE e.key LIKE 'codex_turn_ticket:%'
	), '{}'),
	COALESCE(a.extra->>'codex_5h_used_percent', ''),
	COALESCE(a.extra->>'codex_7d_used_percent', ''),
	COALESCE(a.extra->>'codex_usage_updated_at', ''),
	COALESCE(a.credentials->>'subscription_expires_at', ''),
	a.last_used_at,
	COALESCE(grp.items::text, '[]')
FROM accounts a
LEFT JOIN LATERAL (
	SELECT jsonb_agg(jsonb_build_object('id', g.id, 'name', g.name) ORDER BY g.id) AS items
	FROM account_groups ag
	JOIN groups g ON g.id = ag.group_id
	WHERE ag.account_id = a.id AND g.deleted_at IS NULL AND g.platform = 'openai'
) grp ON TRUE
WHERE a.platform = 'openai'
  AND a.type = 'oauth'
  AND a.deleted_at IS NULL
  AND a.parent_account_id IS NULL
ORDER BY a.id`

func (r *CodexRepository) ListTicketAccounts(ctx context.Context) ([]model.CodexTicketAccountRow, error) {
	rows, err := r.pool.Query(ctx, ticketAccountsQuery)
	if err != nil {
		return nil, fmt.Errorf("query ticket accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]model.CodexTicketAccountRow, 0, 128)
	for rows.Next() {
		var (
			row             model.CodexTicketAccountRow
			lastUsedAt      pgtype.Timestamptz
			quota5HRaw      string
			quota7DRaw      string
			usageSnapshot   string
			subscriptionRaw string
			groupsRaw       string
		)
		if err := rows.Scan(
			&row.ID, &row.Name, &row.Email, &row.Status, &row.Schedulable,
			&row.OptIn, &row.TicketsJSON,
			&quota5HRaw, &quota7DRaw, &usageSnapshot, &subscriptionRaw,
			&lastUsedAt, &groupsRaw,
		); err != nil {
			return nil, fmt.Errorf("scan ticket account: %w", err)
		}
		row.Quota5HUsedPercent = parseFloat(quota5HRaw)
		row.Quota7DUsedPercent = parseFloat(quota7DRaw)
		row.UsageSnapshotAt = parseTime(usageSnapshot)
		row.SubscriptionExpiresAt = parseTime(subscriptionRaw)
		row.LastUsedAt = pgTime(lastUsedAt)
		row.Groups = parseGroupRefs(groupsRaw)
		accounts = append(accounts, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ticket accounts: %w", err)
	}
	return accounts, nil
}

func parseGroupRefs(raw string) []model.CodexGroupRef {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var refs []model.CodexGroupRef
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	return refs
}

// ---------- 用量聚合（单遍扫描，按 请求模型→返回模型 成对） ----------

type UsagePair struct {
	Requested string
	Response  string // 已 trim；空串表示上游没回模型名
}

type UsageCounters struct {
	Requests int
	BusyMs   int64
}

// UsageAggregate 是一次扫描得到的三层聚合结果，每层都按 (请求模型, 返回模型) 成对。
type UsageAggregate struct {
	WindowHours int
	Models      []string
	Total       map[UsagePair]UsageCounters
	ByAccount   map[UsagePair]map[int64]UsageCounters
	ByHour      map[UsagePair]map[time.Time]UsageCounters
}

const gatedUsageQuery = `
SELECT
	COALESCE(NULLIF(btrim(u.requested_model), ''), u.model) AS req,
	COALESCE(btrim(u.upstream_response_model), '') AS resp,
	grouping(u.account_id) AS g_account,
	grouping(date_trunc('hour', u.created_at)) AS g_hour,
	u.account_id,
	date_trunc('hour', u.created_at) AS bucket,
	count(*) AS requests,
	COALESCE(sum(COALESCE(u.duration_ms, 0)), 0)::bigint AS busy_ms
FROM usage_logs u
WHERE u.created_at >= now() - make_interval(hours => $1::int)
  AND COALESCE(NULLIF(btrim(u.requested_model), ''), u.model) = ANY($2::text[])
GROUP BY GROUPING SETS ((1, 2, u.account_id), (1, 2, date_trunc('hour', u.created_at)), (1, 2))`

func (r *CodexRepository) AggregateGatedUsage(ctx context.Context, windowHours int, models []string) (UsageAggregate, error) {
	aggregate := UsageAggregate{
		WindowHours: windowHours,
		Models:      append([]string(nil), models...),
		Total:       map[UsagePair]UsageCounters{},
		ByAccount:   map[UsagePair]map[int64]UsageCounters{},
		ByHour:      map[UsagePair]map[time.Time]UsageCounters{},
	}
	if len(models) == 0 {
		return aggregate, nil
	}
	rows, err := r.pool.Query(ctx, gatedUsageQuery, windowHours, models)
	if err != nil {
		return UsageAggregate{}, fmt.Errorf("aggregate gated usage: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			pair            UsagePair
			groupingAccount int
			groupingHour    int
			accountID       pgtype.Int8
			bucket          pgtype.Timestamptz
			counters        UsageCounters
		)
		if err := rows.Scan(&pair.Requested, &pair.Response, &groupingAccount, &groupingHour,
			&accountID, &bucket, &counters.Requests, &counters.BusyMs); err != nil {
			return UsageAggregate{}, fmt.Errorf("scan usage aggregate: %w", err)
		}
		switch {
		case groupingAccount == 0 && accountID.Valid:
			if aggregate.ByAccount[pair] == nil {
				aggregate.ByAccount[pair] = map[int64]UsageCounters{}
			}
			aggregate.ByAccount[pair][accountID.Int64] = counters
		case groupingHour == 0 && bucket.Valid:
			if aggregate.ByHour[pair] == nil {
				aggregate.ByHour[pair] = map[time.Time]UsageCounters{}
			}
			aggregate.ByHour[pair][bucket.Time.UTC()] = counters
		case groupingAccount == 1 && groupingHour == 1:
			aggregate.Total[pair] = counters
		}
	}
	if err := rows.Err(); err != nil {
		return UsageAggregate{}, fmt.Errorf("iterate usage aggregate: %w", err)
	}
	return aggregate, nil
}

// ---------- 按天趋势（按需面板，代价高） ----------

type DailyPairBucket struct {
	Requested string
	Response  string
	Day       time.Time
	Requests  int
}

const gatedDailyQuery = `
SELECT COALESCE(NULLIF(btrim(u.requested_model), ''), u.model) AS req,
	COALESCE(btrim(u.upstream_response_model), '') AS resp,
	date_trunc('day', u.created_at, $3::text) AS bucket,
	count(*) AS requests
FROM usage_logs u
WHERE u.created_at >= now() - make_interval(days => $1::int)
  AND COALESCE(NULLIF(btrim(u.requested_model), ''), u.model) = ANY($2::text[])
GROUP BY 1, 2, 3 ORDER BY 3, 1, 2`

func (r *CodexRepository) AggregateGatedDaily(ctx context.Context, days int, models []string) ([]DailyPairBucket, error) {
	if len(models) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, gatedDailyQuery, days, models, r.timezone)
	if err != nil {
		return nil, fmt.Errorf("aggregate gated daily: %w", err)
	}
	defer rows.Close()

	buckets := make([]DailyPairBucket, 0, days*4)
	for rows.Next() {
		var (
			item   DailyPairBucket
			bucket pgtype.Timestamptz
		)
		if err := rows.Scan(&item.Requested, &item.Response, &bucket, &item.Requests); err != nil {
			return nil, fmt.Errorf("scan daily bucket: %w", err)
		}
		if bucket.Valid {
			item.Day = bucket.Time.UTC()
		}
		buckets = append(buckets, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily buckets: %w", err)
	}
	return buckets, nil
}

// ---------- 打票配置 ----------

// 读受限视图而不是 settings 本表：settings 里有明文 admin_api_key。
const codexSettingsQuery = `SELECT key, COALESCE(value, '') FROM gpt_monitor_codex_settings`

func (r *CodexRepository) LoadCodexSettings(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx, codexSettingsQuery)
	if err != nil {
		return nil, fmt.Errorf("load codex settings: %w", err)
	}
	defer rows.Close()

	values := make(map[string]string, 16)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan codex setting: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate codex settings: %w", err)
	}
	return values, nil
}
