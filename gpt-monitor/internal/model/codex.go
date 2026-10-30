package model

import "time"

// 票状态分档，与 sub2api 的判据对齐：
//
//	fresh   —— 长度合格且剩余 > fresh_min_seconds（调度时排在前面）
//	stale   —— 长度合格但快到期（上游对临期 turn-state 可能不认）
//	expired —— 有票但已过期（看门狗会清）
//	none    —— opt-in 了但从没打到票
//	off     —— 没 opt-in，压根不参与打票
const (
	TicketFresh   = "fresh"
	TicketStale   = "stale"
	TicketExpired = "expired"
	TicketNone    = "none"
	TicketOff     = "off"
)

// 上游终态模型相对请求模型的裁决，与 sub2api 看门狗（#718）同一套规则。
const (
	ResponseSame     = "same"     // 与请求模型相同
	ResponseUpgraded = "upgraded" // 同系列升版（gpt-5.6-sol → gpt-6-sol），不算降智
	ResponseDegraded = "degraded" // 跨系列或同系列降版（gpt-6-astra → gpt-5.6-luna）
	ResponseUnknown  = "unknown"  // 解析不了的名字，看门狗放行，看板单列
	ResponseBlank    = "blank"    // 上游没回模型名
)

type CodexSettingValue struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Source    string `json:"source"` // db | default
	HotReload bool   `json:"hot_reload"`
}

type CodexConfigView struct {
	Enabled           bool                `json:"enabled"`
	FailClosed        bool                `json:"fail_closed"`
	FloorMinHolders   int                 `json:"floor_min_holders"`
	FreshMinSeconds   int                 `json:"fresh_min_seconds"`
	TargetLength      int                 `json:"target_length"`
	TTLSeconds        int                 `json:"ttl_seconds"`
	ProbeInterval     int                 `json:"harvest_probe_interval_seconds"`
	BackoffMaxSeconds int                 `json:"harvest_backoff_max_seconds"`
	Concurrency       int                 `json:"harvest_concurrency"`
	MaxProbesPerCycle int                 `json:"harvest_max_probes_per_cycle"`
	PreferHolders     bool                `json:"prefer_holders"`
	GatedModels       []string            `json:"gated_models"`
	GatedModelsSource string              `json:"gated_models_source"` // db | default
	Values            []CodexSettingValue `json:"values"`
}

type CodexGroupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// CodexTicketAccountRow 是数据库直出的一行。TicketsJSON 是 extra 里全部
// codex_turn_ticket:* 键的 JSON 对象，按模型拆分在服务层做，查询不依赖门控清单。
type CodexTicketAccountRow struct {
	ID                    int64
	Name                  string
	Email                 string
	Status                string
	Schedulable           bool
	OptIn                 bool
	TicketsJSON           string
	Groups                []CodexGroupRef
	Quota5HUsedPercent    *float64
	Quota7DUsedPercent    *float64
	UsageSnapshotAt       *time.Time
	SubscriptionExpiresAt *time.Time
	LastUsedAt            *time.Time
}

// CodexAccountTicket 是一个账号在一个门控模型上的票。
type CodexAccountTicket struct {
	Model            string     `json:"model"`
	State            string     `json:"state"`
	Length           int        `json:"length"`
	Attempts         int        `json:"attempts"`
	CapturedAt       *time.Time `json:"captured_at"`
	ExpiresAt        *time.Time `json:"expires_at"`
	RemainingSeconds int64      `json:"remaining_seconds"`
	AgeSeconds       int64      `json:"age_seconds"`
}

// CodexAccountUsage 是一个账号在一个门控模型上近 N 小时的用量。
type CodexAccountUsage struct {
	Model           string   `json:"model"`
	Requests        int      `json:"requests"`
	Degraded        int      `json:"degraded"`
	Upgraded        int      `json:"upgraded"`
	DegradedPercent *float64 `json:"degraded_percent"`
	RPM             float64  `json:"rpm"`
	Concurrency     float64  `json:"concurrency"`
}

type CodexTicketAccount struct {
	ID                    int64                `json:"id"`
	Name                  string               `json:"name"`
	Email                 string               `json:"email"`
	Status                string               `json:"status"`
	Schedulable           bool                 `json:"schedulable"`
	OptIn                 bool                 `json:"opt_in"`
	Eligible              bool                 `json:"eligible"` // opt-in 且 active 且可调度
	Groups                []CodexGroupRef      `json:"groups"`
	Tickets               []CodexAccountTicket `json:"tickets"` // 按门控清单顺序，每个模型一项
	Usage                 []CodexAccountUsage  `json:"usage"`   // 只含有流量的模型
	Requests              int                  `json:"requests"`
	Degraded              int                  `json:"degraded"`
	Quota5HUsedPercent    *float64             `json:"quota_5h_used_percent"`
	Quota7DUsedPercent    *float64             `json:"quota_7d_used_percent"`
	Quota7DRemaining      *float64             `json:"quota_7d_remaining_percent"`
	UsageSnapshotAt       *time.Time           `json:"usage_snapshot_at"`
	SubscriptionExpiresAt *time.Time           `json:"subscription_expires_at"`
	LastUsedAt            *time.Time           `json:"last_used_at"`
}

type CodexPoolSummary struct {
	Eligible         int     `json:"eligible"` // opt-in 且可调度：打票机制真正覆盖的分母
	Holders          int     `json:"holders"`
	Fresh            int     `json:"fresh"`
	Stale            int     `json:"stale"`
	Expired          int     `json:"expired"`
	Never            int     `json:"never"`
	CoveragePercent  float64 `json:"coverage_percent"`
	FreshPercent     float64 `json:"fresh_percent"`
	AvgAttempts      float64 `json:"avg_attempts"`
	AvgRemainingSecs int64   `json:"avg_remaining_seconds"`
}

type CodexPoolBreakdown struct {
	GroupID         int64   `json:"group_id"`
	Name            string  `json:"name"`
	Members         int     `json:"members"`
	Schedulable     int     `json:"schedulable"`
	Eligible        int     `json:"eligible"`
	Holders         int     `json:"holders"`
	Fresh           int     `json:"fresh"`
	Stale           int     `json:"stale"`
	Expired         int     `json:"expired"`
	Never           int     `json:"never"`
	Floor           int     `json:"floor"`
	FloorSatisfied  bool    `json:"floor_satisfied"`
	FloorMargin     float64 `json:"floor_margin"` // holders / floor，<1 表示 fail_closed 已放行
	CoveragePercent float64 `json:"coverage_percent"`
	Requests        int     `json:"requests"`
	Degraded        int     `json:"degraded"`
	DegradedPercent float64 `json:"degraded_percent"`
}

type CodexHarvestBucket struct {
	Minute   time.Time `json:"minute"`
	Captured int       `json:"captured"`
}

type CodexHarvestSummary struct {
	WindowMinutes    int                  `json:"window_minutes"`
	Captured         int                  `json:"captured"`
	AvgAttempts      float64              `json:"avg_attempts"`
	OldestCapturedAt *time.Time           `json:"oldest_captured_at"`
	NewestCapturedAt *time.Time           `json:"newest_captured_at"`
	CapturePerMinute float64              `json:"capture_per_minute"`
	CycleCeiling     float64              `json:"cycle_ceiling_per_minute"`
	Buckets          []CodexHarvestBucket `json:"buckets"`
	Note             string               `json:"note"`
}

type CodexUsageBucket struct {
	Hour            time.Time `json:"hour"`
	Requests        int       `json:"requests"`
	Degraded        int       `json:"degraded"`
	Upgraded        int       `json:"upgraded"`
	Blank           int       `json:"blank"`
	DegradedPercent float64   `json:"degraded_percent"`
	RPM             float64   `json:"rpm"`
	Concurrency     float64   `json:"concurrency"`
}

// CodexResponsePair 是「请求模型 → 上游返回模型」的一对及其裁决，用于解释降智率的构成。
type CodexResponsePair struct {
	Response string `json:"response"`
	Verdict  string `json:"verdict"`
	Requests int    `json:"requests"`
}

type CodexDegradationSummary struct {
	WindowHours           int                 `json:"window_hours"`
	Requests              int                 `json:"requests"`
	Degraded              int                 `json:"degraded"`
	Upgraded              int                 `json:"upgraded"`
	Unknown               int                 `json:"unknown"`
	Blank                 int                 `json:"blank"`
	DegradedPercent       float64             `json:"degraded_percent"`
	BlankPercent          float64             `json:"blank_percent"`
	RecentHours           int                 `json:"recent_hours"`
	RecentRequests        int                 `json:"recent_requests"`
	RecentDegraded        int                 `json:"recent_degraded"`
	RecentDegradedPercent float64             `json:"recent_degraded_percent"`
	Pairs                 []CodexResponsePair `json:"pairs"`
	Buckets               []CodexUsageBucket  `json:"buckets"`
}

type CodexLoadSummary struct {
	WindowHours     int     `json:"window_hours"`
	Requests        int     `json:"requests"`
	RPM             float64 `json:"rpm"`
	AvgConcurrency  float64 `json:"avg_concurrency"`
	PeakRPM         float64 `json:"peak_rpm"`
	PeakConcurrency float64 `json:"peak_concurrency"`
	AvgDurationMs   int64   `json:"avg_duration_ms"`
	ActiveAccounts  int     `json:"active_accounts"`
	PerEligibleRPM  float64 `json:"rpm_per_eligible_account"`
}

type CodexQuotaBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type CodexQuotaSummary struct {
	Avg5HUsedPercent  *float64           `json:"avg_5h_used_percent"`
	Avg7DUsedPercent  *float64           `json:"avg_7d_used_percent"`
	Avg7DRemaining    *float64           `json:"avg_7d_remaining_percent"`
	Accounts7DAbove80 int                `json:"accounts_7d_above_80"`
	Accounts5HAbove80 int                `json:"accounts_5h_above_80"`
	SnapshotStale     int                `json:"snapshot_stale"`
	Histogram7D       []CodexQuotaBucket `json:"histogram_7d"`
	ExpiringIn7Days   int                `json:"expiring_in_7_days"`
	ExpiringIn30Days  int                `json:"expiring_in_30_days"`
	Expired           int                `json:"expired"`
}

// CodexTrendSample 由进程内采样器写入，不落库。重启即清空。
type CodexTrendSample struct {
	At       time.Time      `json:"at"`
	Eligible int            `json:"eligible"`
	Holders  map[string]int `json:"holders"` // model → 持票数
	Fresh    map[string]int `json:"fresh"`   // model → 新鲜票数
}

type CodexDailyBucket struct {
	Model           string    `json:"model"`
	Day             time.Time `json:"day"`
	Requests        int       `json:"requests"`
	Degraded        int       `json:"degraded"`
	DegradedPercent float64   `json:"degraded_percent"`
}

type CodexAdvisory struct {
	Level    string `json:"level"` // critical | warning | info | ok
	Category string `json:"category"`
	Model    string `json:"model,omitempty"` // 空 = 与模型无关
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence"`
}

type CodexFreshness struct {
	PoolAt   time.Time `json:"pool_at"`
	UsageAt  time.Time `json:"usage_at"`
	PoolTTL  int       `json:"pool_ttl_seconds"`
	UsageTTL int       `json:"usage_ttl_seconds"`
	Degraded bool      `json:"degraded"`
	Notes    []string  `json:"notes,omitempty"`
}

// CodexModelView 是一个门控模型的完整视图：票池 / 分池 / 采集 / 降智。
type CodexModelView struct {
	Model       string                  `json:"model"`
	Pool        CodexPoolSummary        `json:"pool"`
	Pools       []CodexPoolBreakdown    `json:"pools"`
	Harvest     CodexHarvestSummary     `json:"harvest"`
	Degradation CodexDegradationSummary `json:"degradation"`
}

type CodexOverview struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Config      CodexConfigView      `json:"config"`
	Population  CodexPopulation      `json:"population"`
	Models      []CodexModelView     `json:"models"`
	Load        CodexLoadSummary     `json:"load"`
	Quota       CodexQuotaSummary    `json:"quota"`
	Trend       []CodexTrendSample   `json:"trend"`
	Accounts    []CodexTicketAccount `json:"accounts"`
	Advisories  []CodexAdvisory      `json:"advisories"`
	Freshness   CodexFreshness       `json:"freshness"`
}

// CodexPopulation 是与模型无关的账号总体。
type CodexPopulation struct {
	OAuthTotal  int `json:"oauth_total"`
	OptIn       int `json:"opt_in"`
	NotOptIn    int `json:"not_opt_in"`
	Schedulable int `json:"schedulable"`
	Eligible    int `json:"eligible"`
}

type CodexDailyTrend struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Days        int                `json:"days"`
	Models      []string           `json:"models"`
	Buckets     []CodexDailyBucket `json:"buckets"`
	Note        string             `json:"note"`
}
