package model

import "time"

// ---------- 分组检测 ----------

// IQGroupConfig 是分组检测的配置：测哪些 sub2api 分组、每轮每个分组至少测几个号。
// 分组的智商 = 检测时属于这个分组的号的答题结果；号池每轮按比例抽完号后，给每个选中的分组补测到至少 PerRound 个。
type IQGroupConfig struct {
	IDs      []int64 `json:"ids"`
	PerRound int     `json:"per_round"`
}

// IQGroupUpdate 是对 IQGroupConfig 的局部修改，nil 字段保持原值。
type IQGroupUpdate struct {
	IDs      *[]int64 `json:"ids"`
	PerRound *int     `json:"per_round"`
}

// IQGroupInfo 是 sub2api 里的一个 OpenAI 分组（只读）。
type IQGroupInfo struct {
	ID          int64
	Name        string
	Status      string
	Members     int // 未删除的 OpenAI 号（OAuth + API Key）
	OAuth       int
	APIKey      int
	Available   int // 此刻可调度，与抽样候选同一口径
	Requests24h int // 近 24 小时经网关的请求数；读不到为 -1
}

type IQGroupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type IQGroupRow struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Status       string     `json:"status"`
	Selected     bool       `json:"selected"`
	Missing      bool       `json:"missing"` // 选中了，但 sub2api 里已经没有这个 OpenAI 分组
	Members      int        `json:"members"`
	OAuth        int        `json:"oauth"`
	APIKey       int        `json:"apikey"`
	Available    int        `json:"available"`
	Requests24h  int        `json:"requests_24h"` // -1 = 读不到
	Stats        IQStats    `json:"stats"`        // 窗口内全部模型合计
	Insufficient bool       `json:"insufficient"`
	LastAt       *time.Time `json:"last_at"`
}

type IQGroupLimits struct {
	MaxGroups   int `json:"max_groups"`
	MaxPerRound int `json:"max_per_round"`
}

type IQGroupsView struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Window      string        `json:"window"`
	Since       *time.Time    `json:"since"`
	MinSamples  int           `json:"min_samples"`
	Config      IQGroupConfig `json:"config"`
	Limits      IQGroupLimits `json:"limits"`
	Pool        IQStats       `json:"pool"` // 号池整体同一窗口，作参照
	Groups      []IQGroupRow  `json:"groups"`
	Error       string        `json:"error,omitempty"` // 读 sub2api 分组失败时
}

// ---------- 总览矩阵：来源 × 测试模型 ----------

// 来源：号池整体、某个分组、某个第三方供应商。Key 形如 pool / group:6 / provider:2。
const (
	IQSourcePool     = "pool"
	IQSourceAccounts = "accounts"
	IQSourceGroup    = "group"
	IQSourceProvider = "provider"
)

type IQBoardColumn struct {
	Label           string `json:"label"` // gpt-6-astra@xhigh
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

// IQStripPoint 是历史条上的一格。
type IQStripPoint struct {
	At      time.Time `json:"at"`
	Outcome string    `json:"outcome"`
	Answer  string    `json:"answer,omitempty"`
	Who     string    `json:"who,omitempty"` // 号池 / 分组：#id 名称
	Variant string    `json:"variant"`       // fixed | random
}

type IQBoardCell struct {
	Configured   bool           `json:"configured"` // 这个来源当前在测这个模型
	Level        string         `json:"level"`
	LevelLabel   string         `json:"level_label"`
	Stats        IQStats        `json:"stats"` // 按所选口径：最近 N 次可判分，或时间窗口内
	Insufficient bool           `json:"insufficient"`
	Best         bool           `json:"best"`        // 这一列里最高（至少两个来源样本足够时才标）
	GapVsPool    *float64       `json:"gap_vs_pool"` // 与号池整体同一列的差（百分点）；号池自己、样本不足时为空
	Strip        []IQStripPoint `json:"strip"`       // 最近若干次（旧 → 新），不随口径变
	LastAt       *time.Time     `json:"last_at"`
}

type IQBoardRow struct {
	Key        string        `json:"key"`
	Kind       string        `json:"kind"` // pool | group | provider
	ID         int64         `json:"id"`
	Name       string        `json:"name"`
	Detail     string        `json:"detail"` // 「45 个可用 OAuth 号」/「11/39 个号可用」/ 供应商主机
	Enabled    bool          `json:"enabled"`
	Level      string        `json:"level"` // 这一行最差的判级
	LevelLabel string        `json:"level_label"`
	Running    bool          `json:"running"`
	NextRunAt  *time.Time    `json:"next_run_at"`
	LastRunAt  *time.Time    `json:"last_run_at"`
	LastError  string        `json:"last_error,omitempty"`
	Cells      []IQBoardCell `json:"cells"` // 与 Columns 同序
	Total      IQBoardCell   `json:"total"` // 全部模型合计
}

// IQAdvisory 是给运营看的一条结论，与打票看板同一套等级。
type IQAdvisory struct {
	Level     string `json:"level"` // critical | warning | info | ok
	SourceKey string `json:"source_key,omitempty"`
	Source    string `json:"source,omitempty"` // 来源名，例如「分组 gpt-pro-pool-1」
	Model     string `json:"model,omitempty"`  // 模型@推理强度；结论涉及多个模型时为空，模型名都在证据里
	// FocusModel 是点开这条结论时定位到的那一格（答得最差的模型）。
	FocusModel string `json:"focus_model,omitempty"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Evidence   string `json:"evidence"`
}

type IQBoard struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Window      string          `json:"window"` // recent | 24h | 7d | 30d | all
	Scope       string          `json:"scope"`  // all | fixed | random
	Since       *time.Time      `json:"since"`
	MinSamples  int             `json:"min_samples"`
	Columns     []IQBoardColumn `json:"columns"`
	Rows        []IQBoardRow    `json:"rows"`
	Advisories  []IQAdvisory    `json:"advisories"` // 只在 recent 口径下给（结论讲的是「现在」）
}

// IQCardView 是点开矩阵里一格看到的详情：完整历史条 + 最近几次的作答。
type IQCardView struct {
	GeneratedAt time.Time     `json:"generated_at"`
	SourceKey   string        `json:"source_key"`
	SourceName  string        `json:"source_name"`
	Kind        string        `json:"kind"`
	Scope       string        `json:"scope"`
	Card        IQModelCard   `json:"card"`
	Probes      []IQProbeView `json:"probes"` // 最近 20 次，新 → 旧
}
