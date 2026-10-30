import { describe, expect, it } from 'vitest'
import type { Account, FilterState } from './types'
import { expiryLevelLabel, filterAccounts, formatCountdown, formatDate } from './utils'

function account(overrides: Partial<Account> = {}): Account {
  return {
    id: 1,
    name: 'GPT-Pro',
    email: 'pro@example.com',
    status: 'active',
    schedulable: true,
    subscription_expires_at: '2026-10-09T18:23:11Z',
    account_expires_at: null,
    seconds_remaining: 100,
    days_remaining: 0,
    expiry_level: 'critical',
    expiry_conflict: false,
    usage_warning: false,
    five_hour: { available: false, used_percent: null, resets_at: null, window_minutes: 0 },
    seven_day: { available: true, used_percent: 21, resets_at: null, window_minutes: 10080 },
    usage_snapshot_at: '2026-09-11T06:44:00Z',
    usage_stale: false,
    rate_limit_reset_at: null,
    last_used_at: null,
    admin_url: 'https://example.test/admin/accounts',
    ...overrides
  }
}

const filters: FilterState = {
  search: '',
  summaryMetric: 'all',
  expiryLevel: 'all',
  status: 'all',
  schedulable: 'all'
}

describe('monitor utilities', () => {
  it('formats subscription dates in Asia/Shanghai', () => {
    expect(formatDate('2026-10-09T18:23:11Z')).toBe('2026-10-10')
  })

  it('formats expired countdown', () => {
    expect(formatCountdown(-3600)).toContain('已过期')
  })

  it('filters by search, summary metric and schedulable status', () => {
    const accounts = [
      account({ id: 1, name: '好运来 #1' }),
      account({ id: 2, name: 'Other', schedulable: false, expiry_level: 'expired', seconds_remaining: -10 })
    ]
    expect(filterAccounts(accounts, { ...filters, search: '好运来' })).toHaveLength(1)
    expect(filterAccounts(accounts, { ...filters, summaryMetric: 'expired' })[0].id).toBe(2)
    expect(filterAccounts(accounts, { ...filters, schedulable: 'no' })[0].id).toBe(2)
  })

  it('uses Chinese expiry labels', () => {
    expect(expiryLevelLabel('critical')).toBe('3天内')
  })
})
