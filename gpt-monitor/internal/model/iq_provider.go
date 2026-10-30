package model

import "time"

// 第三方供应商：任意 OpenAI 兼容接口（Base URL + API Key），用和号池同一道题、同一套判分测智商，
// 结果和号池放在一起对比。每个供应商各自一套检测周期与测试模型。

const (
	IQProviderChat      = "chat_completions"
	IQProviderResponses = "responses"
)

// IQProviderSchedule 是供应商的定时配置：锚定周期 ± 随机范围，每轮把每个测试模型各测 SamplesPerModel 次。
type IQProviderSchedule struct {
	Enabled         bool `json:"enabled"`
	IntervalSeconds int  `json:"interval_seconds"`
	JitterSeconds   int  `json:"jitter_seconds"`
	SamplesPerModel int  `json:"samples_per_model"`
}

// IQProviderInput 是新增 / 编辑供应商的请求体（整体替换）。编辑时 APIKey 留空表示不改。
type IQProviderInput struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIFormat string `json:"api_format"`
	APIKey    string `json:"api_key"`
	Models    string `json:"models"`
	IQProviderSchedule
}

type IQProviderStatus struct {
	Running          bool       `json:"running"`
	RunningKind      string     `json:"running_kind,omitempty"` // schedule | manual
	RunningTargets   int        `json:"running_targets"`
	LastRunAt        *time.Time `json:"last_run_at"`
	LastFinishedAt   *time.Time `json:"last_finished_at"`
	LastRoundSeconds *int       `json:"last_round_seconds"`
	NextRunAt        *time.Time `json:"next_run_at"` // 停用时为空
	NextAnchorAt     *time.Time `json:"next_anchor_at"`
	SkippedSlots     int        `json:"skipped_slots"`
	LastError        string     `json:"last_error,omitempty"`
}

// IQProviderView 是看板上的一张供应商卡。API Key 只给末 4 位。
type IQProviderView struct {
	ID         int64              `json:"id"`
	Name       string             `json:"name"`
	BaseURL    string             `json:"base_url"`
	Endpoint   string             `json:"endpoint"` // 实际发题的完整地址
	Host       string             `json:"host"`
	APIFormat  string             `json:"api_format"`
	APIKeyHint string             `json:"api_key_hint"`
	Insecure   bool               `json:"insecure"` // http 明文传 Key
	Models     []IQModelSpec      `json:"models"`
	Schedule   IQProviderSchedule `json:"schedule"`
	Level      string             `json:"level"`
	LevelLabel string             `json:"level_label"`
	Cards      []IQModelCard      `json:"cards"` // 每个测试模型一份，结构与号池的模型卡相同
	Status     IQProviderStatus   `json:"status"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

type IQProviderLimits struct {
	MaxProviders int `json:"max_providers"`
	MaxModels    int `json:"max_models"`
	MaxSamples   int `json:"max_samples_per_model"`
}

type IQProvidersView struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Providers   []IQProviderView `json:"providers"`
	Limits      IQProviderLimits `json:"limits"`
	// 号池当前的测试模型：新增供应商时预填，模型对得上才能逐行对比。
	DefaultModels string `json:"default_models"`
}

// IQProviderTestRequest 是「测试连接」：调一次 GET /models 验证地址和 Key。
// 编辑已有供应商时可以不重填 Key（带 provider_id 用库里的），但只能发往原来的主机。
type IQProviderTestRequest struct {
	ProviderID int64  `json:"provider_id"`
	BaseURL    string `json:"base_url"`
	APIFormat  string `json:"api_format"`
	APIKey     string `json:"api_key"`
	Models     string `json:"models"`
}

type IQProviderTestResult struct {
	OK         bool     `json:"ok"`
	Endpoint   string   `json:"endpoint"`
	Message    string   `json:"message"`
	ModelCount int      `json:"model_count"`
	Missing    []string `json:"missing"` // 配置了、但 /models 里没有的测试模型
	Sample     []string `json:"sample"`
	LatencyMs  int64    `json:"latency_ms"`
}
