import type { Account, ExpiryLevel, FilterState, SummaryMetric } from './types'

const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false
})

const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit'
})

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return dateTimeFormatter.format(date).replaceAll('/', '-')
}

export function formatDate(value: string | null | undefined): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return dateFormatter.format(date).replaceAll('/', '-')
}

export function formatCountdown(seconds: number): string {
  const normalized = Math.trunc(seconds)
  const absolute = Math.abs(normalized)
  const days = Math.floor(absolute / 86400)
  const hours = Math.floor((absolute % 86400) / 3600)
  const minutes = Math.floor((absolute % 3600) / 60)
  let value = ''
  if (days > 0) value += `${days}天`
  if (hours > 0 || days > 0) value += `${hours}小时`
  value += `${minutes}分钟`
  return normalized < 0 ? `已过期 ${value}` : `剩余 ${value}`
}

export function formatRelativeTime(value: string | null | undefined, now = Date.now()): string {
  if (!value) return '无'
  const timestamp = new Date(value).getTime()
  if (Number.isNaN(timestamp)) return '-'
  const deltaSeconds = Math.trunc((now - timestamp) / 1000)
  if (deltaSeconds < 0) return formatCountdown(Math.abs(deltaSeconds)).replace('剩余 ', '')
  if (deltaSeconds < 60) return `${deltaSeconds}秒前`
  if (deltaSeconds < 3600) return `${Math.floor(deltaSeconds / 60)}分钟前`
  if (deltaSeconds < 86400) return `${Math.floor(deltaSeconds / 3600)}小时前`
  return `${Math.floor(deltaSeconds / 86400)}天前`
}

export function expiryLevelLabel(level: ExpiryLevel): string {
  return {
    expired: '已过期',
    critical: '3天内',
    warning: '7天内',
    attention: '14天内',
    upcoming: '30天内',
    normal: '正常',
    unknown: '到期未知'
  }[level]
}

export function statusLabel(status: string): string {
  return {
    active: '正常',
    inactive: '停用',
    error: '异常'
  }[status] ?? status
}

export function usageText(available: boolean, usedPercent: number | null): string {
  if (!available || usedPercent === null) return '-'
  return `${usedPercent.toFixed(1)}%`
}

function matchesSummaryMetric(account: Account, metric: SummaryMetric): boolean {
  switch (metric) {
    case 'all':
      return true
    case 'expired':
      return account.expiry_level === 'expired'
    case 'due_3_days':
      return account.seconds_remaining > 0 && account.seconds_remaining <= 3 * 86400
    case 'due_7_days':
      return account.seconds_remaining > 0 && account.seconds_remaining <= 7 * 86400
    case 'due_14_days':
      return account.seconds_remaining > 0 && account.seconds_remaining <= 14 * 86400
    case 'due_30_days':
      return account.seconds_remaining > 0 && account.seconds_remaining <= 30 * 86400
    case 'unknown_expiry':
      return account.subscription_expires_at === null
    case 'status_error':
      return account.status === 'error'
    case 'unschedulable':
      return !account.schedulable
    case 'expiry_conflict':
      return account.expiry_conflict
    case 'usage_warning':
      return account.usage_warning
    case 'usage_snapshot_gap':
      return account.usage_snapshot_at === null
  }
}

export function filterAccounts(accounts: Account[], filters: FilterState): Account[] {
  const search = filters.search.trim().toLowerCase()
  return accounts.filter((account) => {
    if (search) {
      const haystack = `${account.id} ${account.name} ${account.email}`.toLowerCase()
      if (!haystack.includes(search)) return false
    }
    if (!matchesSummaryMetric(account, filters.summaryMetric)) return false
    if (filters.expiryLevel !== 'all' && account.expiry_level !== filters.expiryLevel) return false
    if (filters.status !== 'all' && account.status !== filters.status) return false
    if (filters.schedulable !== 'all') {
      const expected = filters.schedulable === 'yes'
      if (account.schedulable !== expected) return false
    }
    return true
  })
}

// ---------- Codex 292 打票看板 ----------

export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || Number.isNaN(seconds)) return '-'
  const absolute = Math.abs(Math.trunc(seconds))
  if (absolute < 60) return `${absolute}秒`
  const minutes = Math.floor(absolute / 60)
  if (minutes < 60) return `${minutes}分${absolute % 60 ? `${absolute % 60}秒` : ''}`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}小时${minutes % 60 ? `${minutes % 60}分` : ''}`
  return `${Math.floor(hours / 24)}天${hours % 24 ? `${hours % 24}小时` : ''}`
}

export function formatPercent(value: number | null | undefined, digits = 2): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '-'
  return `${value.toFixed(digits)}%`
}

export function formatNumber(value: number | null | undefined, digits = 2): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '-'
  return Number.isInteger(value) ? String(value) : value.toFixed(digits)
}

export function ticketStateLabel(state: string): string {
  return {
    fresh: '新鲜票',
    stale: '临期票',
    expired: '票已过期',
    none: '从未拿到票',
    off: '未开启打票'
  }[state] ?? state
}

export function advisoryLevelLabel(level: string): string {
  return { critical: '需处理', warning: '注意', info: '提示', ok: '正常' }[level] ?? level
}

export function formatHourLabel(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    hour12: false
  }).format(date).replaceAll('/', '-')
}

export function formatDayLabel(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    month: '2-digit',
    day: '2-digit'
  }).format(date).replaceAll('/', '-')
}

export function formatMinuteLabel(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  }).format(date)
}

// ---------- 智商检测 ----------

export function iqOutcomeLabel(outcome: string): string {
  return ({ correct: '答对', wrong: '答错', no_answer: '没给答案', error: '出错' } as Record<string, string>)[outcome] ?? outcome
}

// 倒计时：「4 分 51 秒后」。已过期返回「即将开始」。
export function formatTimeUntil(target: string | null | undefined, now = Date.now()): string {
  if (!target) return '-'
  const ms = new Date(target).getTime() - now
  if (Number.isNaN(ms)) return '-'
  if (ms <= 0) return '即将开始'
  const total = Math.ceil(ms / 1000)
  const minutes = Math.floor(total / 60)
  const seconds = total % 60
  return minutes > 0 ? `${minutes} 分 ${seconds} 秒后` : `${seconds} 秒后`
}

export function formatMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || Number.isNaN(ms)) return '-'
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  const minutes = Math.floor(ms / 60_000)
  return `${minutes}m${Math.round((ms % 60_000) / 1000)}s`
}

export function formatAccuracy(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '-'
  return `${Number.isInteger(value) ? value : value.toFixed(1)}%`
}
