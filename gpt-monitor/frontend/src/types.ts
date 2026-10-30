export type ExpiryLevel = 'expired' | 'critical' | 'warning' | 'attention' | 'upcoming' | 'normal' | 'unknown'

export interface Session {
  authenticated: boolean
  username?: string
  expires_at?: string
}

export interface UsageWindow {
  available: boolean
  used_percent: number | null
  resets_at: string | null
  window_minutes: number
}

export interface Account {
  id: number
  name: string
  email: string
  status: 'active' | 'inactive' | 'error' | string
  schedulable: boolean
  subscription_expires_at: string | null
  account_expires_at: string | null
  seconds_remaining: number
  days_remaining: number
  expiry_level: ExpiryLevel
  expiry_conflict: boolean
  usage_warning: boolean
  five_hour: UsageWindow
  seven_day: UsageWindow
  usage_snapshot_at: string | null
  usage_stale: boolean
  error_message?: string
  rate_limit_reset_at: string | null
  last_used_at: string | null
  admin_url: string
}

export interface Summary {
  total: number
  expired: number
  due_3_days: number
  due_7_days: number
  due_14_days: number
  due_30_days: number
  later: number
  unknown_expiry: number
  status_error: number
  unschedulable: number
  expiry_conflict: number
  usage_warning: number
  usage_snapshot_gap: number
}

export interface Snapshot {
  generated_at: string
  cached_at: string
  summary: Summary
  accounts: Account[]
}

export interface FilterState {
  search: string
  summaryMetric: SummaryMetric
  expiryLevel: ExpiryLevel | 'all'
  status: 'all' | 'active' | 'inactive' | 'error'
  schedulable: 'all' | 'yes' | 'no'
}

export type SummaryMetric =
  | 'all'
  | 'expired'
  | 'due_3_days'
  | 'due_7_days'
  | 'due_14_days'
  | 'due_30_days'
  | 'unknown_expiry'
  | 'status_error'
  | 'unschedulable'
  | 'expiry_conflict'
  | 'usage_warning'
  | 'usage_snapshot_gap'

// ---------- Codex 292 打票看板 ----------

export type TicketState = 'fresh' | 'stale' | 'expired' | 'none' | 'off'
export type AdvisoryLevel = 'critical' | 'warning' | 'info' | 'ok'
export type ResponseVerdict = 'same' | 'upgraded' | 'degraded' | 'unknown' | 'blank'

export interface CodexSettingValue {
  key: string
  value: string
  source: 'db' | 'default'
  hot_reload: boolean
}

export interface CodexConfig {
  enabled: boolean
  fail_closed: boolean
  floor_min_holders: number
  fresh_min_seconds: number
  target_length: number
  ttl_seconds: number
  harvest_probe_interval_seconds: number
  harvest_backoff_max_seconds: number
  harvest_concurrency: number
  harvest_max_probes_per_cycle: number
  prefer_holders: boolean
  gated_models: string[]
  gated_models_source: 'db' | 'default'
  values: CodexSettingValue[]
}

export interface CodexGroupRef {
  id: number
  name: string
}

export interface CodexAccountTicket {
  model: string
  state: TicketState
  length: number
  attempts: number
  captured_at: string | null
  expires_at: string | null
  remaining_seconds: number
  age_seconds: number
}

export interface CodexAccountUsage {
  model: string
  requests: number
  degraded: number
  upgraded: number
  degraded_percent: number | null
  rpm: number
  concurrency: number
}

export interface CodexTicketAccount {
  id: number
  name: string
  email: string
  status: string
  schedulable: boolean
  opt_in: boolean
  eligible: boolean
  groups: CodexGroupRef[] | null
  tickets: CodexAccountTicket[] | null
  usage: CodexAccountUsage[] | null
  requests: number
  degraded: number
  quota_5h_used_percent: number | null
  quota_7d_used_percent: number | null
  quota_7d_remaining_percent: number | null
  usage_snapshot_at: string | null
  subscription_expires_at: string | null
  last_used_at: string | null
}

export interface CodexPoolSummary {
  eligible: number
  holders: number
  fresh: number
  stale: number
  expired: number
  never: number
  coverage_percent: number
  fresh_percent: number
  avg_attempts: number
  avg_remaining_seconds: number
}

export interface CodexPoolBreakdown {
  group_id: number
  name: string
  members: number
  schedulable: number
  eligible: number
  holders: number
  fresh: number
  stale: number
  expired: number
  never: number
  floor: number
  floor_satisfied: boolean
  floor_margin: number
  coverage_percent: number
  requests: number
  degraded: number
  degraded_percent: number
}

export interface CodexHarvestSummary {
  window_minutes: number
  captured: number
  avg_attempts: number
  oldest_captured_at: string | null
  newest_captured_at: string | null
  capture_per_minute: number
  cycle_ceiling_per_minute: number
  buckets: { minute: string; captured: number }[] | null
  note: string
}

export interface CodexUsageBucket {
  hour: string
  requests: number
  degraded: number
  upgraded: number
  blank: number
  degraded_percent: number
  rpm: number
  concurrency: number
}

export interface CodexResponsePair {
  response: string
  verdict: ResponseVerdict
  requests: number
}

export interface CodexDegradationSummary {
  window_hours: number
  requests: number
  degraded: number
  upgraded: number
  unknown: number
  blank: number
  degraded_percent: number
  blank_percent: number
  recent_hours: number
  recent_requests: number
  recent_degraded: number
  recent_degraded_percent: number
  pairs: CodexResponsePair[] | null
  buckets: CodexUsageBucket[] | null
}

export interface CodexLoadSummary {
  window_hours: number
  requests: number
  rpm: number
  avg_concurrency: number
  peak_rpm: number
  peak_concurrency: number
  avg_duration_ms: number
  active_accounts: number
  rpm_per_eligible_account: number
}

export interface CodexQuotaSummary {
  avg_5h_used_percent: number | null
  avg_7d_used_percent: number | null
  avg_7d_remaining_percent: number | null
  accounts_7d_above_80: number
  accounts_5h_above_80: number
  snapshot_stale: number
  histogram_7d: { label: string; count: number }[] | null
  expiring_in_7_days: number
  expiring_in_30_days: number
  expired: number
}

export interface CodexTrendSample {
  at: string
  eligible: number
  holders: Record<string, number> | null
  fresh: Record<string, number> | null
}

export interface CodexAdvisory {
  level: AdvisoryLevel
  category: string
  model?: string
  title: string
  detail: string
  evidence: string
}

export interface CodexFreshness {
  pool_at: string
  usage_at: string
  pool_ttl_seconds: number
  usage_ttl_seconds: number
  degraded: boolean
  notes?: string[]
}

export interface CodexModelView {
  model: string
  pool: CodexPoolSummary
  pools: CodexPoolBreakdown[] | null
  harvest: CodexHarvestSummary
  degradation: CodexDegradationSummary
}

export interface CodexPopulation {
  oauth_total: number
  opt_in: number
  not_opt_in: number
  schedulable: number
  eligible: number
}

export interface CodexOverview {
  generated_at: string
  config: CodexConfig
  population: CodexPopulation
  models: CodexModelView[] | null
  load: CodexLoadSummary
  quota: CodexQuotaSummary
  trend: CodexTrendSample[] | null
  accounts: CodexTicketAccount[] | null
  advisories: CodexAdvisory[] | null
  freshness: CodexFreshness
}

export interface CodexDailyTrend {
  generated_at: string
  days: number
  models: string[] | null
  buckets: { model: string; day: string; requests: number; degraded: number; degraded_percent: number }[] | null
  note: string
}

export interface Features {
  codex_ticket: boolean
  iq?: boolean
}

// ---------- 智商检测 ----------

export type IQOutcome = 'correct' | 'wrong' | 'no_answer' | 'error'
export type IQLevel = 'healthy' | 'warning' | 'danger' | 'unknown' | 'blocked' | 'paused'

export interface IQModelSpec {
  model: string
  reasoning_effort: string
  label: string
}

export interface IQStats {
  graded: number
  correct: number
  wrong: number
  no_answer: number
  errors: number
  swapped: number
  json_format: number
  accuracy_percent: number | null
  json_percent: number | null
  avg_duration_ms: number | null
}

// 题面口径：fixed = 随机化之前的固定题面；random = 每次随机改写叙述方式（条件和答案不变）
export type IQVariant = 'fixed' | 'random'
export type IQScope = 'all' | IQVariant

export interface IQHistoryPoint {
  at: string
  outcome: IQOutcome
  account_id: number
  account_name: string
  answer: string
  upstream_model: string
  swapped: boolean
  duration_ms: number
  variant?: IQVariant
}

export interface IQModelCard {
  spec: IQModelSpec
  level: IQLevel
  level_label: string
  recent: IQStats
  history: IQStats
  last_round: IQStats
  last_at: string | null
  points: IQHistoryPoint[] | null
}

export interface IQGate {
  open: boolean
  required: string
  current: string
  checked_at: string | null
  error?: string
}

export type IQSampleMode = 'ratio' | 'count'

export interface IQScheduleConfig {
  enabled: boolean
  interval_seconds: number
  jitter_seconds: number
  sample_mode: IQSampleMode
  sample_percent: number
  sample_min: number
  sample_count: number
}

export interface IQSchedule extends IQScheduleConfig {
  config_source: 'settings' | 'default'
  paused: boolean
  concurrency: number
  running: boolean
  running_kind?: string
  running_targets: number
  last_run_at: string | null
  last_finished_at: string | null
  last_round_seconds: number | null
  next_run_at: string | null
  next_anchor_at: string | null
  skipped_slots: number
  last_error?: string
  candidates: number
  sample_size: number
  groups: number
  group_per_round: number
  last_round_pool: number
  last_round_top_ups: number
}

export interface IQThresholds {
  recent_window: number
  min_samples: number
  healthy_percent: number
  warning_percent: number
  history_points: number
}

export interface IQQuestion {
  id: string
  prompt: string
  answer: number
  randomized: boolean
  space: number
  samples: string[] | null
}

export interface IQOverview {
  generated_at: string
  level: IQLevel
  level_label: string
  gate: IQGate
  schedule: IQSchedule
  question: IQQuestion
  models: IQModelSpec[] | null
  models_source: 'settings' | 'default'
  groups: IQGroupConfig
  thresholds: IQThresholds
  board: IQBoard
}

// ---------- 总览矩阵：来源 × 测试模型 ----------

export type IQSourceKind = 'pool' | 'group' | 'provider'
export type IQBoardWindow = 'recent' | IQWindow

export interface IQBoardColumn {
  label: string
  model: string
  reasoning_effort: string
}

export interface IQStripPoint {
  at: string
  outcome: IQOutcome
  answer?: string
  who?: string
  variant: IQVariant
}

export interface IQBoardCell {
  configured: boolean
  level: IQLevel
  level_label: string
  stats: IQStats
  insufficient: boolean
  best: boolean
  gap_vs_pool: number | null
  strip: IQStripPoint[] | null
  last_at: string | null
}

export interface IQBoardRow {
  key: string
  kind: IQSourceKind
  id: number
  name: string
  detail: string
  enabled: boolean
  level: IQLevel
  level_label: string
  running: boolean
  next_run_at: string | null
  last_run_at: string | null
  last_error?: string
  cells: IQBoardCell[]
  total: IQBoardCell
}

export type IQAdvisoryLevel = 'critical' | 'warning' | 'info' | 'ok'

export interface IQAdvisory {
  level: IQAdvisoryLevel
  source_key?: string
  source?: string
  model?: string
  focus_model?: string
  title: string
  detail: string
  evidence: string
}

export interface IQBoard {
  generated_at: string
  window: IQBoardWindow
  scope: IQScope
  since: string | null
  min_samples: number
  columns: IQBoardColumn[] | null
  rows: IQBoardRow[] | null
  advisories: IQAdvisory[] | null
}

export interface IQCardView {
  generated_at: string
  source_key: string
  source_name: string
  kind: IQSourceKind
  scope: IQScope
  card: IQModelCard
  probes: IQProbe[] | null
}

// ---------- 分组 ----------

export interface IQGroupConfig {
  ids: number[] | null
  per_round: number
}

export interface IQGroupRef {
  id: number
  name: string
}

export interface IQGroupRow {
  id: number
  name: string
  status: string
  selected: boolean
  missing: boolean
  members: number
  oauth: number
  apikey: number
  available: number
  requests_24h: number
  stats: IQStats
  insufficient: boolean
  last_at: string | null
}

export interface IQGroupsView {
  generated_at: string
  window: IQWindow
  since: string | null
  min_samples: number
  config: IQGroupConfig
  limits: { max_groups: number; max_per_round: number }
  pool: IQStats
  groups: IQGroupRow[] | null
  error?: string
}

export interface IQAccountModelStat extends IQStats {
  model: string
  reasoning_effort: string
  last_at: string | null
}

export interface IQAccountRow extends IQStats {
  account_id: number
  account_name: string
  account_type: string
  groups: IQGroupRef[] | null
  available: boolean
  insufficient: boolean
  last_at: string | null
  recent: IQOutcome[] | null
  last_answer: string
  last_upstream_model: string
  models: IQAccountModelStat[] | null
}

export type IQWindow = '24h' | '7d' | '30d' | 'all'

export interface IQAccountsView {
  generated_at: string
  window: IQWindow
  group_id: number
  since: string | null
  min_samples: number
  tested: number
  available: number
  overall: IQStats
  accounts: IQAccountRow[] | null
}

export interface IQProbe {
  id: number
  provider_id: number
  run_id: string
  account_id: number
  account_name: string
  account_type: string
  sample_group_id: number
  group_ids: number[] | null
  variant: IQVariant
  prompt: string
  model: string
  reasoning_effort: string
  outcome: IQOutcome
  answer_value: string
  answer_format: 'json' | 'text' | 'none' | ''
  answer_reasoning: string
  answer_excerpt: string
  upstream_model: string
  model_verdict: string
  ticket_injected: boolean | null
  error_message: string
  duration_ms: number
  trigger: 'schedule' | 'manual'
  created_at: string
}

export interface IQProbesView {
  generated_at: string
  probes: IQProbe[] | null
}

export interface IQSettingsView {
  models: IQModelSpec[] | null
  models_source: 'settings' | 'default'
  schedule: IQScheduleConfig
  schedule_source: 'settings' | 'default'
  groups: IQGroupConfig
  paused: boolean
}

export type TicketFilter = 'all' | TicketState | 'holders'

// ---------- 第三方供应商 ----------

export type IQProviderFormat = 'chat_completions' | 'responses'

export interface IQProviderSchedule {
  enabled: boolean
  interval_seconds: number
  jitter_seconds: number
  samples_per_model: number
}

export interface IQProviderInput extends IQProviderSchedule {
  name: string
  base_url: string
  api_format: IQProviderFormat
  api_key: string
  models: string
}

export interface IQProviderStatus {
  running: boolean
  running_kind?: string
  running_targets: number
  last_run_at: string | null
  last_finished_at: string | null
  last_round_seconds: number | null
  next_run_at: string | null
  next_anchor_at: string | null
  skipped_slots: number
  last_error?: string
}

export interface IQProvider {
  id: number
  name: string
  base_url: string
  endpoint: string
  host: string
  api_format: IQProviderFormat
  api_key_hint: string
  insecure: boolean
  models: IQModelSpec[] | null
  schedule: IQProviderSchedule
  level: IQLevel
  level_label: string
  cards: IQModelCard[] | null
  status: IQProviderStatus
  created_at: string
  updated_at: string
}

export interface IQProvidersView {
  generated_at: string
  providers: IQProvider[] | null
  limits: { max_providers: number; max_models: number; max_samples_per_model: number }
  default_models: string
}

export interface IQProviderTestResult {
  ok: boolean
  endpoint: string
  message: string
  model_count: number
  missing: string[] | null
  sample: string[] | null
  latency_ms: number
}
