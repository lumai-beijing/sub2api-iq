package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains runtime configuration loaded from environment variables.
type Config struct {
	ServerAddr             string
	DatabaseURL            string
	AdminUsername          string
	AdminPasswordHash      string
	SessionSecret          string
	SessionTTL             time.Duration
	CookieSecure           bool
	CacheTTL               time.Duration
	UsageStaleAfter        time.Duration
	UsageWarningThreshold  float64
	Sub2APIAdminURL        string
	CriticalExpiryDays     int
	WarningExpiryDays      int
	AttentionExpiryDays    int
	UpcomingExpiryDays     int
	ShutdownTimeout        time.Duration
	LoginRateLimitAttempts int
	LoginRateLimitWindow   time.Duration

	// Codex 292 打票看板。所有 DB 访问都走缓存，TTL 就是对 46 的性能预算旋钮。
	CodexEnabled          bool
	CodexGatedModels      []string
	CodexDisplayTimezone  string
	CodexPoolGroupIDs     []int64
	CodexPoolCacheTTL     time.Duration
	CodexUsageCacheTTL    time.Duration
	CodexTrendCacheTTL    time.Duration
	CodexSampleInterval   time.Duration
	CodexTrendRetention   time.Duration
	CodexUsageWindowHours int
	CodexTrendDays        int
	DBStatementTimeout    time.Duration
	DBMaxConns            int32

	// 智商检测。唯一会调用上游、也是唯一会写数据的模块：经 sub2api 管理员测试接口发题，
	// 结果写在监控自己的 SQLite 里（sub2api 的库仍然只读）。
	IQEnabled        bool
	IQDBPath         string
	Sub2APIBaseURL   string
	Sub2APIAdminKey  string
	IQMinVersion     string
	IQInterval       time.Duration
	IQJitter         time.Duration
	IQConcurrency    int
	IQRequestTimeout time.Duration
	IQRetention      time.Duration
	IQDefaultModels  string
	IQApplyTicket    bool
	IQHealthyPercent float64
	IQWarningPercent float64
	IQRecentWindow   int
	IQMinSamples     int

	// 第三方供应商：界面上添加的 OpenAI 兼容接口。API Key 用 IQSecretKey 加密后存进 SQLite。
	IQProvidersEnabled     bool
	IQSecretKey            string
	IQProviderAllowPrivate bool // 允许 Base URL 指向内网地址（默认不允许，防止被当成探测内网的跳板）
}

func Load() (Config, error) {
	cfg := Config{
		ServerAddr:             envString("SERVER_ADDR", ":8080"),
		DatabaseURL:            strings.TrimSpace(os.Getenv("DATABASE_URL")),
		AdminUsername:          strings.TrimSpace(os.Getenv("MONITOR_ADMIN_USERNAME")),
		AdminPasswordHash:      strings.TrimSpace(os.Getenv("MONITOR_ADMIN_PASSWORD_HASH")),
		SessionSecret:          strings.TrimSpace(os.Getenv("SESSION_SECRET")),
		Sub2APIAdminURL:        envString("SUB2API_ADMIN_URL", "https://clawapi.lmuai.com/admin/accounts"),
		LoginRateLimitAttempts: envInt("LOGIN_RATE_LIMIT_ATTEMPTS", 5),
	}

	var err error
	if cfg.SessionTTL, err = envDuration("SESSION_TTL", 12*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.CacheTTL, err = envDuration("CACHE_TTL", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.UsageStaleAfter, err = envDuration("USAGE_STALE_AFTER", 60*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.LoginRateLimitWindow, err = envDuration("LOGIN_RATE_LIMIT_WINDOW", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}

	if cfg.CodexPoolCacheTTL, err = envDuration("CODEX_POOL_CACHE_TTL", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.CodexUsageCacheTTL, err = envDuration("CODEX_USAGE_CACHE_TTL", 120*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.CodexTrendCacheTTL, err = envDuration("CODEX_TREND_CACHE_TTL", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.CodexSampleInterval, err = envDuration("CODEX_SAMPLE_INTERVAL", 2*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.CodexTrendRetention, err = envDuration("CODEX_TREND_RETENTION", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.DBStatementTimeout, err = envDuration("DB_STATEMENT_TIMEOUT", 8*time.Second); err != nil {
		return Config{}, err
	}

	cfg.CodexEnabled = envBool("CODEX_DASHBOARD_ENABLED", true)
	// 门控模型的兜底清单：settings 表读不到 openai_codex_ticket_models 时用它。
	// 兼容旧的单数键 CODEX_GATED_MODEL。
	cfg.CodexGatedModels = envStringList("CODEX_GATED_MODELS", envStringList("CODEX_GATED_MODEL", []string{"gpt-6-astra"}))
	cfg.CodexDisplayTimezone = envString("CODEX_DISPLAY_TIMEZONE", "Asia/Shanghai")
	cfg.CodexPoolGroupIDs = envInt64List("CODEX_POOL_GROUP_IDS", []int64{67, 69})
	cfg.CodexUsageWindowHours = envInt("CODEX_USAGE_WINDOW_HOURS", 24)
	cfg.CodexTrendDays = envInt("CODEX_TREND_DAYS", 7)
	cfg.DBMaxConns = int32(envInt("DB_MAX_CONNS", 4))

	cfg.CookieSecure = envBool("COOKIE_SECURE", true)
	cfg.UsageWarningThreshold = envFloat("USAGE_WARNING_THRESHOLD", 90)
	cfg.CriticalExpiryDays = envInt("EXPIRY_CRITICAL_DAYS", 3)
	cfg.WarningExpiryDays = envInt("EXPIRY_WARNING_DAYS", 7)
	cfg.AttentionExpiryDays = envInt("EXPIRY_ATTENTION_DAYS", 14)
	cfg.UpcomingExpiryDays = envInt("EXPIRY_UPCOMING_DAYS", 30)

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.AdminUsername == "" {
		return Config{}, fmt.Errorf("MONITOR_ADMIN_USERNAME is required")
	}
	if cfg.AdminPasswordHash == "" {
		return Config{}, fmt.Errorf("MONITOR_ADMIN_PASSWORD_HASH is required")
	}
	if len(cfg.SessionSecret) < 32 {
		return Config{}, fmt.Errorf("SESSION_SECRET must contain at least 32 characters")
	}
	if cfg.CriticalExpiryDays < 0 || cfg.WarningExpiryDays < cfg.CriticalExpiryDays ||
		cfg.AttentionExpiryDays < cfg.WarningExpiryDays || cfg.UpcomingExpiryDays < cfg.AttentionExpiryDays {
		return Config{}, fmt.Errorf("expiry day thresholds must be non-negative and ordered")
	}
	if cfg.UsageWarningThreshold < 0 || cfg.UsageWarningThreshold > 100 {
		return Config{}, fmt.Errorf("USAGE_WARNING_THRESHOLD must be between 0 and 100")
	}
	if cfg.LoginRateLimitAttempts <= 0 {
		return Config{}, fmt.Errorf("LOGIN_RATE_LIMIT_ATTEMPTS must be positive")
	}
	// 性能护栏：窗口上限写死 24 小时。usage_logs 在 46 上是 5.6GB / 410 万行，
	// 24h 走 idx_usage_logs_effective_requested_model_created 只读 ~5MB，
	// 而 7 天要扫 ~278MB 堆页、会把 sub2api 的热缓存挤掉，所以只能走单独的按需面板。
	if cfg.CodexUsageWindowHours <= 0 || cfg.CodexUsageWindowHours > 24 {
		return Config{}, fmt.Errorf("CODEX_USAGE_WINDOW_HOURS must be between 1 and 24")
	}
	if cfg.CodexTrendDays <= 0 || cfg.CodexTrendDays > 14 {
		return Config{}, fmt.Errorf("CODEX_TREND_DAYS must be between 1 and 14")
	}
	if cfg.CodexUsageCacheTTL < 30*time.Second {
		return Config{}, fmt.Errorf("CODEX_USAGE_CACHE_TTL must be at least 30s")
	}
	if cfg.CodexTrendCacheTTL < 5*time.Minute {
		return Config{}, fmt.Errorf("CODEX_TREND_CACHE_TTL must be at least 5m")
	}
	if cfg.CodexSampleInterval < 30*time.Second {
		return Config{}, fmt.Errorf("CODEX_SAMPLE_INTERVAL must be at least 30s")
	}
	if cfg.DBStatementTimeout <= 0 {
		return Config{}, fmt.Errorf("DB_STATEMENT_TIMEOUT must be positive")
	}
	if cfg.DBMaxConns <= 0 || cfg.DBMaxConns > 16 {
		return Config{}, fmt.Errorf("DB_MAX_CONNS must be between 1 and 16")
	}
	if len(cfg.CodexGatedModels) == 0 {
		return Config{}, fmt.Errorf("CODEX_GATED_MODELS is required")
	}
	if _, err := time.LoadLocation(cfg.CodexDisplayTimezone); err != nil {
		return Config{}, fmt.Errorf("CODEX_DISPLAY_TIMEZONE: %w", err)
	}
	if err := loadIQ(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadIQ(cfg *Config) error {
	cfg.IQEnabled = envBool("IQ_ENABLED", false)
	cfg.IQDBPath = envString("IQ_DB_PATH", "/data/iq.db")
	cfg.Sub2APIBaseURL = strings.TrimRight(envString("SUB2API_BASE_URL", ""), "/")
	cfg.Sub2APIAdminKey = strings.TrimSpace(os.Getenv("SUB2API_ADMIN_API_KEY"))
	// 低于这个版本的 sub2api 测试接口会把题目换成 "hi"（lumai-beijing/sub2api#816 在 0.2.13.2 上线）。
	cfg.IQMinVersion = envString("IQ_MIN_SUB2API_VERSION", "lmuai-0.2.13.2")
	cfg.IQConcurrency = envInt("IQ_CONCURRENCY", 2)
	cfg.IQDefaultModels = envString("IQ_DEFAULT_MODELS", "gpt-6-astra@xhigh")
	cfg.IQApplyTicket = envBool("IQ_APPLY_CODEX_TICKET", true)
	cfg.IQHealthyPercent = envFloat("IQ_HEALTHY_PERCENT", 80)
	cfg.IQWarningPercent = envFloat("IQ_WARNING_PERCENT", 50)
	cfg.IQRecentWindow = envInt("IQ_RECENT_WINDOW", 30)
	cfg.IQMinSamples = envInt("IQ_MIN_SAMPLES", 5)
	cfg.IQProvidersEnabled = envBool("IQ_PROVIDERS_ENABLED", true)
	// 没单独配密钥时用 SESSION_SECRET 派生；注意轮换 SESSION_SECRET 后已存的 Key 需要在界面上重填。
	cfg.IQSecretKey = envString("IQ_SECRET_KEY", cfg.SessionSecret)
	cfg.IQProviderAllowPrivate = envBool("IQ_PROVIDER_ALLOW_PRIVATE", false)

	var err error
	if cfg.IQInterval, err = envDuration("IQ_INTERVAL", 5*time.Minute); err != nil {
		return err
	}
	if cfg.IQJitter, err = envDuration("IQ_JITTER", 60*time.Second); err != nil {
		return err
	}
	if cfg.IQRequestTimeout, err = envDuration("IQ_REQUEST_TIMEOUT", 4*time.Minute); err != nil {
		return err
	}
	if cfg.IQRetention, err = envDuration("IQ_RETENTION", 90*24*time.Hour); err != nil {
		return err
	}
	if !cfg.IQEnabled {
		return nil
	}

	if cfg.Sub2APIBaseURL == "" {
		return fmt.Errorf("IQ_ENABLED=true requires SUB2API_BASE_URL")
	}
	if !strings.HasPrefix(cfg.Sub2APIBaseURL, "http://") && !strings.HasPrefix(cfg.Sub2APIBaseURL, "https://") {
		return fmt.Errorf("SUB2API_BASE_URL must start with http:// or https://")
	}
	if len(cfg.Sub2APIAdminKey) < 16 {
		return fmt.Errorf("IQ_ENABLED=true requires SUB2API_ADMIN_API_KEY")
	}
	// 这两个只是界面上没改过时的默认值；范围与看板「设置」里的校验一致。
	if cfg.IQInterval < time.Minute || cfg.IQInterval > 24*time.Hour {
		return fmt.Errorf("IQ_INTERVAL must be between 1m and 24h")
	}
	if cfg.IQJitter < 0 || 2*cfg.IQJitter > cfg.IQInterval {
		return fmt.Errorf("IQ_JITTER must be between 0 and half of IQ_INTERVAL")
	}
	if cfg.IQConcurrency <= 0 || cfg.IQConcurrency > 8 {
		return fmt.Errorf("IQ_CONCURRENCY must be between 1 and 8")
	}
	if cfg.IQRequestTimeout < 10*time.Second || cfg.IQRequestTimeout > 15*time.Minute {
		return fmt.Errorf("IQ_REQUEST_TIMEOUT must be between 10s and 15m")
	}
	if cfg.IQRetention < 24*time.Hour {
		return fmt.Errorf("IQ_RETENTION must be at least 24h")
	}
	if cfg.IQWarningPercent < 0 || cfg.IQHealthyPercent > 100 || cfg.IQWarningPercent > cfg.IQHealthyPercent {
		return fmt.Errorf("IQ thresholds must satisfy 0 <= IQ_WARNING_PERCENT <= IQ_HEALTHY_PERCENT <= 100")
	}
	if cfg.IQRecentWindow <= 0 || cfg.IQRecentWindow > 500 || cfg.IQMinSamples <= 0 || cfg.IQMinSamples > cfg.IQRecentWindow {
		return fmt.Errorf("IQ_RECENT_WINDOW must be 1..500 and IQ_MIN_SAMPLES must be 1..IQ_RECENT_WINDOW")
	}
	return nil
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func envInt64List(key string, fallback []int64) []int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	values := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return fallback
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return fallback
	}
	return values
}

func envStringList(key string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	values := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		values = append(values, part)
	}
	if len(values) == 0 {
		return fallback
	}
	return values
}
