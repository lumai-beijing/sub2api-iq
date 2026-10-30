package db

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gpt-pro-monitor/internal/model"
)

// IQRepository 只读 sub2api 的账号与分组，给智商检测出候选。
type IQRepository struct {
	pool *pgxpool.Pool

	// 分组近 24 小时请求数：扫 usage_logs，10 分钟内复用上一次的结果。
	trafficMu sync.Mutex
	traffic   map[int64]int
	trafficAt time.Time
}

func NewIQRepository(pool *pgxpool.Pool) *IQRepository {
	return &IQRepository{pool: pool}
}

// 「可用」= 网关此刻真的会调度它：active、可调度、不在限流 / 过载 / 临时不可调度的冷却期里。
// 冷却中的号不测：探测本身也会占用上游额度，而且测出来的多半是上游报错，不是智商。
// 号池整体只抽 OAuth 号；分组里的 API Key 号（官方 Key、别家中转）也会被分组检测抽到，所以两种都列出来。
// 账号表 ~110 行、account_groups 几百行，这条查询的代价可以忽略。
const iqAvailable = `a.status = 'active'
  AND a.schedulable
  AND (a.rate_limit_reset_at IS NULL OR a.rate_limit_reset_at <= now())
  AND (a.overload_until IS NULL OR a.overload_until <= now())
  AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= now())`

const iqCandidatesQuery = `
SELECT a.id, a.name, a.type,
       COALESCE(array_agg(ag.group_id ORDER BY ag.group_id) FILTER (WHERE ag.group_id IS NOT NULL), '{}')
FROM accounts a
LEFT JOIN account_groups ag ON ag.account_id = a.id
WHERE a.platform = 'openai'
  AND a.type IN ('oauth', 'apikey')
  AND a.deleted_at IS NULL
  AND a.parent_account_id IS NULL
  AND ` + iqAvailable + `
GROUP BY a.id, a.name, a.type
ORDER BY a.id`

func (r *IQRepository) ListIQCandidates(ctx context.Context) ([]model.IQCandidate, error) {
	rows, err := r.pool.Query(ctx, iqCandidatesQuery)
	if err != nil {
		return nil, fmt.Errorf("query iq candidates: %w", err)
	}
	defer rows.Close()
	out := make([]model.IQCandidate, 0, 128)
	for rows.Next() {
		var item model.IQCandidate
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.GroupIDs); err != nil {
			return nil, fmt.Errorf("scan iq candidate: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// OpenAI 分组及成员数。成员只算未删除、非影子的 OpenAI 号；可用与候选同一口径。
const iqGroupsQuery = `
SELECT g.id, g.name, g.status,
       count(a.id),
       count(a.id) FILTER (WHERE a.type = 'oauth'),
       count(a.id) FILTER (WHERE a.type = 'apikey'),
       count(a.id) FILTER (WHERE ` + iqAvailable + `)
FROM groups g
LEFT JOIN account_groups ag ON ag.group_id = g.id
LEFT JOIN accounts a ON a.id = ag.account_id
  AND a.deleted_at IS NULL AND a.platform = 'openai' AND a.parent_account_id IS NULL AND a.type IN ('oauth', 'apikey')
WHERE g.platform = 'openai' AND g.deleted_at IS NULL
GROUP BY g.id, g.name, g.status
ORDER BY g.id`

// 近 24 小时每个分组的请求数。走 usage_logs 的 created_at 索引，147 上一天约 3 万行。
const iqGroupTrafficQuery = `
SELECT group_id, count(*)
FROM usage_logs
WHERE created_at > now() - interval '24 hours' AND group_id IS NOT NULL
GROUP BY group_id`

const groupTrafficTTL = 10 * time.Minute

// ListIQGroups 列出 sub2api 的 OpenAI 分组。近 24 小时请求数读失败时记 -1，不影响分组本身。
func (r *IQRepository) ListIQGroups(ctx context.Context) ([]model.IQGroupInfo, error) {
	rows, err := r.pool.Query(ctx, iqGroupsQuery)
	if err != nil {
		return nil, fmt.Errorf("query iq groups: %w", err)
	}
	out := []model.IQGroupInfo{}
	for rows.Next() {
		var item model.IQGroupInfo
		if err := rows.Scan(&item.ID, &item.Name, &item.Status, &item.Members, &item.OAuth, &item.APIKey, &item.Available); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan iq group: %w", err)
		}
		out = append(out, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	traffic, trafficErr := r.groupTraffic(ctx)
	for i := range out {
		if trafficErr != nil {
			out[i].Requests24h = -1
		} else {
			out[i].Requests24h = traffic[out[i].ID]
		}
	}
	return out, nil
}

func (r *IQRepository) groupTraffic(ctx context.Context) (map[int64]int, error) {
	r.trafficMu.Lock()
	defer r.trafficMu.Unlock()
	if r.traffic != nil && time.Since(r.trafficAt) < groupTrafficTTL {
		return r.traffic, nil
	}
	rows, err := r.pool.Query(ctx, iqGroupTrafficQuery)
	if err != nil {
		return nil, fmt.Errorf("query iq group traffic: %w", err)
	}
	defer rows.Close()
	traffic := map[int64]int{}
	for rows.Next() {
		var (
			id int64
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan iq group traffic: %w", err)
		}
		traffic[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	r.traffic, r.trafficAt = traffic, time.Now()
	return traffic, nil
}

// AccountGroups 返回这些号当前所属的分组（账号排行里展示「这个号在哪个分组」）。
func (r *IQRepository) AccountGroups(ctx context.Context, accountIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT account_id, group_id FROM account_groups WHERE account_id = ANY($1) ORDER BY account_id, group_id`, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("query iq account groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var accountID, groupID int64
		if err := rows.Scan(&accountID, &groupID); err != nil {
			return nil, fmt.Errorf("scan iq account group: %w", err)
		}
		out[accountID] = append(out[accountID], groupID)
	}
	return out, rows.Err()
}
