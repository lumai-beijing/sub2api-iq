import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import DashboardView from './DashboardView.vue'
import type { Snapshot } from '../types'

const snapshot: Snapshot = {
  generated_at: '2026-09-11T06:45:00Z',
  cached_at: '2026-09-11T06:45:00Z',
  summary: {
    total: 66,
    expired: 27,
    due_3_days: 0,
    due_7_days: 7,
    due_14_days: 9,
    due_30_days: 39,
    later: 0,
    unknown_expiry: 0,
    status_error: 13,
    unschedulable: 30,
    expiry_conflict: 15,
    usage_warning: 0,
    usage_snapshot_gap: 0
  },
  accounts: [
    {
      id: 471,
      name: 'GPT-好运来-0909- #2',
      email: 'example@example.com',
      status: 'active',
      schedulable: true,
      subscription_expires_at: '2026-10-09T18:23:11Z',
      account_expires_at: '2026-10-09T13:08:00Z',
      seconds_remaining: 1000,
      days_remaining: 0,
      expiry_level: 'upcoming',
      expiry_conflict: true,
      usage_warning: false,
      five_hour: { available: false, used_percent: 0, resets_at: null, window_minutes: 0 },
      seven_day: { available: true, used_percent: 21, resets_at: '2026-09-16T13:51:27Z', window_minutes: 10080 },
      usage_snapshot_at: '2026-09-11T06:44:23Z',
      usage_stale: false,
      rate_limit_reset_at: null,
      last_used_at: null,
      admin_url: 'https://clawapi.lmuai.com/admin/accounts'
    }
  ]
}

describe('DashboardView', () => {
  it('renders the summary, conflict warning and missing 5h window', () => {
    const wrapper = mount(DashboardView, {
      props: {
        snapshot,
        loading: false,
        error: ''
      }
    })
    expect(wrapper.text()).toContain('全部 Pro')
    expect(wrapper.text()).toContain('66')
    expect(wrapper.text()).toContain('GPT-好运来-0909- #2')
    expect(wrapper.text()).toContain('账号先到期')
    expect(wrapper.text()).toContain('当前窗口未提供')
  })
})
