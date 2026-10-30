package model

import "time"

// 智商检测：定时抽号答一道有标准答案的题，按答对率衡量每个号的答题水平。
// 结果存在监控自己的 SQLite 里；sub2api 的库仍然只读。

// IQCandidate 是此刻可被抽中的号（active、可调度、不在任何冷却期）。
type IQCandidate struct {
	ID       int64
	Name     string
	Type     string  // oauth | apikey；号池整体只抽 OAuth 号，分组里有什么号就测什么号
	GroupIDs []int64 // 此刻所属的 sub2api 分组
}

// 账号类型：与 sub2api accounts.type 一致。
const (
	IQAccountOAuth  = "oauth"
	IQAccountAPIKey = "apikey"
)

// IQError 是可以直接展示给用户的错误，带 HTTP 状态码。
type IQError struct {
	Status  int
	Message string
}

func (e *IQError) Error() string { return e.Message }

type IQModelSpec struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	Label           string `json:"label"` // gpt-6-astra@xhigh
}

// IQGate 是版本闸：sub2api 低于 Required 时一律不发题（旧接口会把题目换成 "hi"）。
type IQGate struct {
	Open      bool       `json:"open"`
	Required  string     `json:"required"`
	Current   string     `json:"current"`
	CheckedAt *time.Time `json:"checked_at"`
	Error     string     `json:"error,omitempty"`
}

// 抽样方式：按可用号的百分比（带下限），或每轮固定个数。
const (
	IQSampleRatio = "ratio"
	IQSampleCount = "count"
)

// IQScheduleConfig 是定时检测的可调参数：界面上改，存在监控的 SQLite（iq_settings.schedule），即改即生效。
// 周期是「锚定」的：锚点每隔 IntervalSeconds 固定推进，每轮在锚点 ± JitterSeconds 内随机开跑，
// 不受上一轮耗时影响。
type IQScheduleConfig struct {
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
	JitterSeconds   int    `json:"jitter_seconds"`
	SampleMode      string `json:"sample_mode"`    // ratio | count
	SamplePercent   int    `json:"sample_percent"` // ratio：可用号的百分比，向上取整
	SampleMin       int    `json:"sample_min"`     // ratio：每轮至少几个
	SampleCount     int    `json:"sample_count"`   // count：每轮固定几个
}

// IQScheduleUpdate 是对 IQScheduleConfig 的局部修改，nil 字段保持原值。
type IQScheduleUpdate struct {
	Enabled         *bool   `json:"enabled"`
	IntervalSeconds *int    `json:"interval_seconds"`
	JitterSeconds   *int    `json:"jitter_seconds"`
	SampleMode      *string `json:"sample_mode"`
	SamplePercent   *int    `json:"sample_percent"`
	SampleMin       *int    `json:"sample_min"`
	SampleCount     *int    `json:"sample_count"`
}

type IQSchedule struct {
	IQScheduleConfig
	ConfigSource     string     `json:"config_source"` // settings | default
	Paused           bool       `json:"paused"`        // = !enabled，兼容旧前端
	Concurrency      int        `json:"concurrency"`
	Running          bool       `json:"running"`
	RunningKind      string     `json:"running_kind,omitempty"` // schedule | manual
	RunningTargets   int        `json:"running_targets"`
	LastRunAt        *time.Time `json:"last_run_at"`
	LastFinishedAt   *time.Time `json:"last_finished_at"`
	LastRoundSeconds *int       `json:"last_round_seconds"` // 上一轮从开跑到最后一个号答完
	NextRunAt        *time.Time `json:"next_run_at"`        // 定时关闭时为空
	NextAnchorAt     *time.Time `json:"next_anchor_at"`
	SkippedSlots     int        `json:"skipped_slots"` // 上一轮超时、错过整个随机窗口而跳过的周期数（进程启动以来）
	LastError        string     `json:"last_error,omitempty"`
	Candidates       int        `json:"candidates"`  // 此刻可用的 OAuth 号（号池整体的抽样基数）
	SampleSize       int        `json:"sample_size"` // 号池整体每轮抽几个
	// 分组补测：号池抽完后，给每个选中的分组补到至少 GroupPerRound 个号。
	Groups          int `json:"groups"`
	GroupPerRound   int `json:"group_per_round"`
	LastRoundPool   int `json:"last_round_pool"`    // 上一轮号池抽样的号数
	LastRoundTopUps int `json:"last_round_top_ups"` // 上一轮为分组补测的号数
}

// IQStats 是一组探测的计数。Graded = 答对 + 答错 + 无答案；出错（上游失败、超时）不进准确率分母。
type IQStats struct {
	Graded          int      `json:"graded"`
	Correct         int      `json:"correct"`
	Wrong           int      `json:"wrong"`
	NoAnswer        int      `json:"no_answer"`
	Errors          int      `json:"errors"`
	Swapped         int      `json:"swapped"`     // 上游换成了别的系列或更低版本
	JSONFormat      int      `json:"json_format"` // 按要求只输出了 JSON
	AccuracyPercent *float64 `json:"accuracy_percent"`
	JSONPercent     *float64 `json:"json_percent"`
	AvgDurationMs   *int64   `json:"avg_duration_ms"`
}

type IQHistoryPoint struct {
	At            time.Time `json:"at"`
	Outcome       string    `json:"outcome"`
	AccountID     int64     `json:"account_id"`
	AccountName   string    `json:"account_name"`
	Answer        string    `json:"answer"`
	UpstreamModel string    `json:"upstream_model"`
	Swapped       bool      `json:"swapped"`
	DurationMs    int64     `json:"duration_ms"`
	Variant       string    `json:"variant"` // fixed（随机化之前的固定题面）| random
}

type IQModelCard struct {
	Spec       IQModelSpec      `json:"spec"`
	Level      string           `json:"level"` // healthy | warning | danger | unknown
	LevelLabel string           `json:"level_label"`
	Recent     IQStats          `json:"recent"`     // 最近 RecentWindow 次，判级用
	History    IQStats          `json:"history"`    // 历史条覆盖的全部点
	LastRound  IQStats          `json:"last_round"` // 最近一轮里这个模型的结果
	LastAt     *time.Time       `json:"last_at"`
	Points     []IQHistoryPoint `json:"points"` // 旧 → 新
}

type IQThresholds struct {
	RecentWindow   int     `json:"recent_window"`
	MinSamples     int     `json:"min_samples"`
	HealthyPercent float64 `json:"healthy_percent"`
	WarningPercent float64 `json:"warning_percent"`
	HistoryPoints  int     `json:"history_points"`
}

type IQQuestionView struct {
	ID         string   `json:"id"`
	Prompt     string   `json:"prompt"` // 原题
	Answer     int      `json:"answer"`
	Randomized bool     `json:"randomized"`
	Space      float64  `json:"space"`   // 随机题面的组合数
	Samples    []string `json:"samples"` // 几个随机题面示例
}

type IQOverview struct {
	GeneratedAt  time.Time      `json:"generated_at"`
	Level        string         `json:"level"` // 号池与分组（自己的号）最差的判级：healthy | warning | danger | unknown | blocked | paused
	LevelLabel   string         `json:"level_label"`
	Gate         IQGate         `json:"gate"`
	Schedule     IQSchedule     `json:"schedule"`
	Question     IQQuestionView `json:"question"`
	Models       []IQModelSpec  `json:"models"`
	ModelsSource string         `json:"models_source"` // settings | default
	Groups       IQGroupConfig  `json:"groups"`
	Thresholds   IQThresholds   `json:"thresholds"`
	Board        IQBoard        `json:"board"` // 默认口径（最近 N 次、全部题面）的总览矩阵，含结论
}

type IQAccountModelStat struct {
	Model           string     `json:"model"`
	ReasoningEffort string     `json:"reasoning_effort"`
	LastAt          *time.Time `json:"last_at"`
	IQStats
}

type IQAccountRow struct {
	AccountID         int64                `json:"account_id"`
	AccountName       string               `json:"account_name"`
	AccountType       string               `json:"account_type"` // oauth | apikey（老记录为空，都是 OAuth）
	Groups            []IQGroupRef         `json:"groups"`       // 当前所属分组
	Available         bool                 `json:"available"`
	Insufficient      bool                 `json:"insufficient"`
	LastAt            *time.Time           `json:"last_at"`
	Recent            []string             `json:"recent"` // 最近 10 次的结果，旧 → 新
	LastAnswer        string               `json:"last_answer"`
	LastUpstreamModel string               `json:"last_upstream_model"`
	Models            []IQAccountModelStat `json:"models"`
	IQStats
}

type IQAccountsView struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Window      string         `json:"window"`
	GroupID     int64          `json:"group_id"` // 0 = 全部账号
	Since       *time.Time     `json:"since"`
	MinSamples  int            `json:"min_samples"`
	Tested      int            `json:"tested"`    // 窗口内被测过的号
	Available   int            `json:"available"` // 此刻可用的号
	Overall     IQStats        `json:"overall"`
	Accounts    []IQAccountRow `json:"accounts"`
}

type IQProbeView struct {
	ID              int64     `json:"id"`
	ProviderID      int64     `json:"provider_id"` // 0 = 号池
	RunID           string    `json:"run_id"`
	AccountID       int64     `json:"account_id"`
	AccountName     string    `json:"account_name"`
	AccountType     string    `json:"account_type"`
	SampleGroupID   int64     `json:"sample_group_id"` // >0：为这个分组补测的
	GroupIDs        []int64   `json:"group_ids"`       // 检测时所属分组
	Variant         string    `json:"variant"`         // fixed | random
	Prompt          string    `json:"prompt"`          // 这次发出的题面（随机化之前的记录为空，即原题）
	Model           string    `json:"model"`
	ReasoningEffort string    `json:"reasoning_effort"`
	Outcome         string    `json:"outcome"`
	AnswerValue     string    `json:"answer_value"`
	AnswerFormat    string    `json:"answer_format"` // json | text | none
	AnswerReasoning string    `json:"answer_reasoning"`
	AnswerExcerpt   string    `json:"answer_excerpt"`
	UpstreamModel   string    `json:"upstream_model"`
	ModelVerdict    string    `json:"model_verdict"`
	TicketInjected  *bool     `json:"ticket_injected"`
	ErrorMessage    string    `json:"error_message"`
	DurationMs      int64     `json:"duration_ms"`
	Trigger         string    `json:"trigger"`
	CreatedAt       time.Time `json:"created_at"`
}

type IQProbeQuery struct {
	Source    string // pool | accounts | group:<id> | provider:<id>；空 = accounts
	AccountID int64
	Model     string // 模型名或 gpt-6-astra@xhigh
	Outcome   string // correct | wrong | no_answer | error；空 = 全部
	Scope     string // all | fixed | random
	Limit     int
}

type IQProbesView struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Probes      []IQProbeView `json:"probes"`
}

type IQRunRequest struct {
	AccountID int64  `json:"account_id"`
	GroupID   int64  `json:"group_id"` // >0：只测这个分组（抽 GroupPerRound 个号）
	Model     string `json:"model"`
}

type IQRunResponse struct {
	RunID   string `json:"run_id"`
	Kind    string `json:"kind"` // round | account
	Targets int    `json:"targets"`
}

type IQSettingsRequest struct {
	Models   *string           `json:"models"`
	Schedule *IQScheduleUpdate `json:"schedule"`
	Groups   *IQGroupUpdate    `json:"groups"`
	// Paused 是旧前端的「暂停自动检测」，等价于 schedule.enabled = !paused。
	Paused *bool `json:"paused"`
}

type IQSettingsView struct {
	Models         []IQModelSpec    `json:"models"`
	ModelsSource   string           `json:"models_source"`
	Schedule       IQScheduleConfig `json:"schedule"`
	ScheduleSource string           `json:"schedule_source"`
	Groups         IQGroupConfig    `json:"groups"`
	Paused         bool             `json:"paused"` // = !schedule.enabled，兼容旧前端
}
