import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CodexTicketView from './CodexTicketView.vue'
import type { CodexModelView, CodexOverview } from '../types'

function modelView(name: string, overrides: Partial<CodexModelView> = {}): CodexModelView {
  return {
    model: name,
    pool: { eligible: 70, holders: 27, fresh: 24, stale: 3, expired: 40, never: 3, coverage_percent: 38.57, fresh_percent: 34.3, avg_attempts: 1, avg_remaining_seconds: 1800 },
    pools: [
      { group_id: 67, name: 'gpt-pro-pool-1', members: 38, schedulable: 37, eligible: 37, holders: 14, fresh: 14, stale: 0, expired: 22, never: 1, floor: 5, floor_satisfied: true, floor_margin: 2.8, coverage_percent: 37.8, requests: 900, degraded: 2, degraded_percent: 0.22 },
      { group_id: 69, name: 'gpt-pro-pool-2-auto', members: 39, schedulable: 33, eligible: 33, holders: 13, fresh: 10, stale: 3, expired: 18, never: 2, floor: 5, floor_satisfied: true, floor_margin: 2.6, coverage_percent: 39.4, requests: 500, degraded: 1, degraded_percent: 0.2 }
    ],
    harvest: { window_minutes: 60, captured: 27, avg_attempts: 1, oldest_captured_at: '2026-09-20T01:05:00Z', newest_captured_at: '2026-09-20T01:58:00Z', capture_per_minute: 0.45, cycle_ceiling_per_minute: 160, buckets: [{ minute: '2026-09-20T01:05:00Z', captured: 2 }], note: '票是覆盖写的。' },
    degradation: {
      window_hours: 24, requests: 15067, degraded: 550, upgraded: 0, unknown: 0, blank: 14, degraded_percent: 3.65, blank_percent: 0.09,
      recent_hours: 3, recent_requests: 1564, recent_degraded: 8, recent_degraded_percent: 0.51,
      pairs: [{ response: name, verdict: 'same', requests: 14503 }, { response: 'gpt-5.6-luna', verdict: 'degraded', requests: 550 }],
      buckets: [
        { hour: '2026-09-20T00:00:00Z', requests: 784, degraded: 0, upgraded: 0, blank: 0, degraded_percent: 0, rpm: 13.07, concurrency: 4.2 },
        { hour: '2026-09-20T01:00:00Z', requests: 780, degraded: 8, upgraded: 0, blank: 0, degraded_percent: 1.03, rpm: 13, concurrency: 4.1 }
      ]
    },
    ...overrides
  }
}

function overview(overrides: Partial<CodexOverview> = {}): CodexOverview {
  const sol = modelView('gpt-5.6-sol', {
    pool: { eligible: 70, holders: 7, fresh: 7, stale: 0, expired: 0, never: 63, coverage_percent: 10, fresh_percent: 10, avg_attempts: 1, avg_remaining_seconds: 3000 },
    pools: [
      { group_id: 67, name: 'gpt-pro-pool-1', members: 38, schedulable: 37, eligible: 37, holders: 6, fresh: 6, stale: 0, expired: 0, never: 31, floor: 5, floor_satisfied: true, floor_margin: 1.2, coverage_percent: 16.2, requests: 1200, degraded: 0, degraded_percent: 0 },
      { group_id: 69, name: 'gpt-pro-pool-2-auto', members: 39, schedulable: 33, eligible: 33, holders: 1, fresh: 1, stale: 0, expired: 0, never: 32, floor: 5, floor_satisfied: false, floor_margin: 0.2, coverage_percent: 3, requests: 1100, degraded: 0, degraded_percent: 0 }
    ],
    degradation: {
      window_hours: 24, requests: 21390, degraded: 0, upgraded: 551, unknown: 0, blank: 81, degraded_percent: 0, blank_percent: 0.38,
      recent_hours: 3, recent_requests: 2400, recent_degraded: 0, recent_degraded_percent: 0,
      pairs: [{ response: 'gpt-5.6-sol', verdict: 'same', requests: 20758 }, { response: 'gpt-6-sol', verdict: 'upgraded', requests: 551 }, { response: '', verdict: 'blank', requests: 81 }],
      buckets: [{ hour: '2026-09-20T01:00:00Z', requests: 800, degraded: 0, upgraded: 12, blank: 2, degraded_percent: 0, rpm: 13.3, concurrency: 4 }]
    }
  })
  return {
    generated_at: '2026-09-20T02:00:00Z',
    config: {
      enabled: true, fail_closed: true, floor_min_holders: 5, fresh_min_seconds: 300, target_length: 292, ttl_seconds: 3600,
      harvest_probe_interval_seconds: 6, harvest_backoff_max_seconds: 300, harvest_concurrency: 4, harvest_max_probes_per_cycle: 16,
      prefer_holders: true, gated_models: ['gpt-6-astra', 'gpt-5.6-sol'], gated_models_source: 'db',
      values: [
        { key: 'openai_codex_ticket_models', value: 'gpt-6-astra,gpt-5.6-sol', source: 'db', hot_reload: true },
        { key: 'openai_codex_ticket_target_length', value: '292', source: 'default', hot_reload: false }
      ]
    },
    population: { oauth_total: 107, opt_in: 107, not_opt_in: 0, schedulable: 71, eligible: 70 },
    models: [modelView('gpt-6-astra'), sol],
    load: { window_hours: 24, requests: 36457, rpm: 25.3, avg_concurrency: 8.7, peak_rpm: 40, peak_concurrency: 14, avg_duration_ms: 21290, active_accounts: 69, rpm_per_eligible_account: 0.36 },
    quota: { avg_5h_used_percent: 12.5, avg_7d_used_percent: 44, avg_7d_remaining_percent: 56, accounts_7d_above_80: 4, accounts_5h_above_80: 1, snapshot_stale: 2, histogram_7d: [{ label: '0-20%', count: 3 }, { label: '80-100%', count: 20 }], expiring_in_7_days: 2, expiring_in_30_days: 9, expired: 1 },
    trend: [],
    accounts: [
      {
        id: 564, name: 'GPT-zhuyawen', email: 'a@b.com', status: 'active', schedulable: true, opt_in: true, eligible: true,
        groups: [{ id: 67, name: 'gpt-pro-pool-1' }],
        tickets: [
          { model: 'gpt-6-astra', state: 'fresh', length: 292, attempts: 1, captured_at: '2026-09-20T01:33:00Z', expires_at: '2026-09-20T02:33:00Z', remaining_seconds: 1980, age_seconds: 1620 },
          { model: 'gpt-5.6-sol', state: 'none', length: 0, attempts: 0, captured_at: null, expires_at: null, remaining_seconds: 0, age_seconds: 0 }
        ],
        usage: [{ model: 'gpt-6-astra', requests: 120, degraded: 0, upgraded: 0, degraded_percent: 0, rpm: 0.083, concurrency: 0.4 }, { model: 'gpt-5.6-sol', requests: 40, degraded: 0, upgraded: 2, degraded_percent: 0, rpm: 0.03, concurrency: 0.1 }],
        requests: 160, degraded: 0,
        quota_5h_used_percent: 10, quota_7d_used_percent: 30, quota_7d_remaining_percent: 70,
        usage_snapshot_at: '2026-09-20T01:55:00Z', subscription_expires_at: null, last_used_at: null
      }
    ],
    advisories: [
      { level: 'critical', category: '票池', model: 'gpt-5.6-sol', title: '持票账号已跌破地板，无票号正在照常接流量', detail: 'fail_closed 的地板是保命阀。', evidence: '跌破：gpt-pro-pool-2-auto 1/5' },
      { level: 'ok', category: '降智', model: 'gpt-6-astra', title: '降智率在预期内', detail: '', evidence: '近 3h 0.51%' }
    ],
    freshness: { pool_at: '2026-09-20T01:59:00Z', usage_at: '2026-09-20T01:58:00Z', pool_ttl_seconds: 60, usage_ttl_seconds: 120, degraded: false },
    ...overrides
  }
}

function mountView(data: CodexOverview | null) {
  return mount(CodexTicketView, { props: { overview: data, dailyTrend: null, dailyLoading: false, error: '' } })
}
const activeTab = (w: ReturnType<typeof mountView>) => w.findAll('.section-tab').find((t) => t.classes().includes('active'))?.text()
const activeModel = (w: ReturnType<typeof mountView>) => w.findAll('.model-card').find((c) => c.classes().includes('active'))?.find('.model-name').text()

describe('CodexTicketView 多门控模型', () => {
  it('每个门控模型一张卡，各自显示覆盖 / 地板 / 降智', () => {
    const wrapper = mountView(overview())
    const cards = wrapper.findAll('.model-card')
    expect(cards).toHaveLength(2)
    expect(cards[0].text()).toContain('gpt-6-astra')
    expect(cards[0].text()).toContain('27/70')
    expect(cards[0].text()).toContain('2.6×') // 最薄池 69 组
    expect(cards[1].text()).toContain('gpt-5.6-sol')
    expect(cards[1].text()).toContain('7/70')
    expect(cards[1].text()).toContain('0.2×')
    expect(cards[1].classes()).toContain('tone-danger')
  })

  it('首屏落在最严重结论所属的模型与维度（sol 的票池）', () => {
    const wrapper = mountView(overview())
    expect(activeModel(wrapper)).toBe('gpt-5.6-sol')
    expect(activeTab(wrapper)).toBe('票池')
    expect(wrapper.text()).toContain('gpt-pro-pool-2-auto')
    const breached = wrapper.findAll('tbody tr').find((r) => r.text().includes('gpt-pro-pool-2-auto'))
    expect(breached?.classes()).toContain('row-expired')
  })

  it('点模型卡切换到该模型，详情随之变化', async () => {
    const wrapper = mountView(overview())
    await wrapper.findAll('.model-card')[0].trigger('click')
    expect(activeModel(wrapper)).toBe('gpt-6-astra')
    // astra 的 69 组 2.6× 满足地板，不再标红
    const row = wrapper.findAll('tbody tr').find((r) => r.text().includes('gpt-pro-pool-2-auto'))
    expect(row?.classes()).not.toContain('row-expired')
    expect(row?.text()).toContain('2.6×')
  })

  it('详情区的模型切换只在与模型相关的维度出现', async () => {
    const wrapper = mountView(overview())
    expect(wrapper.find('.model-switch').exists()).toBe(true)
    const capacity = wrapper.findAll('.section-tab').find((t) => t.text() === '容量')
    await capacity?.trigger('click')
    expect(wrapper.find('.model-switch').exists()).toBe(false)
    expect(wrapper.text()).toContain('全部门控模型合计')
  })

  it('降智维度按谱系区分：sol 的 gpt-6-sol 显示为升版而非降智', async () => {
    const wrapper = mountView(overview())
    const tab = wrapper.findAll('.section-tab').find((t) => t.text() === '降智')
    await tab?.trigger('click')
    // 当前选中 sol
    const chips = wrapper.findAll('.pair-chip')
    const upgraded = chips.find((c) => c.text().includes('gpt-6-sol'))
    expect(upgraded?.text()).toContain('升版')
    expect(upgraded?.classes()).toContain('verdict-upgraded')
    expect(wrapper.text()).toContain('同系列升版')
    expect(wrapper.text()).toContain('551')
  })

  it('结论带模型标签，点它跳到对应模型与维度', async () => {
    const wrapper = mountView(overview())
    // 先切到 astra，再点 sol 的结论
    await wrapper.findAll('.model-card')[0].trigger('click')
    const row = wrapper.findAll('.advisory-row').find((r) => r.text().includes('跌破地板'))
    expect(row?.find('.advisory-model').text()).toBe('gpt-5.6-sol')
    await row?.trigger('click')
    expect(activeModel(wrapper)).toBe('gpt-5.6-sol')
    expect(activeTab(wrapper)).toBe('票池')
    expect(wrapper.text()).toContain('fail_closed 的地板是保命阀。')
  })

  it('账号表每个模型一列票状态，筛选按当前选中模型', async () => {
    const wrapper = mountView(overview())
    const accountsTab = wrapper.findAll('.section-tab').find((t) => t.text() === '账号')
    await accountsTab?.trigger('click')
    const headers = wrapper.findAll('thead th').map((h) => h.text())
    expect(headers).toContain('gpt-6-astra 票')
    expect(headers).toContain('gpt-5.6-sol 票')
    // 当前选中 sol；564 在 sol 上是「从未拿到票」，按 holders 过滤应为空
    const select = wrapper.findAll('select')[0]
    await select.setValue('holders')
    expect(wrapper.text()).toContain('显示 0 / 1')
    // 切到 astra 再筛 holders，564 持 astra 新鲜票
    await wrapper.findAll('.model-switch-item')[0].trigger('click')
    expect(wrapper.text()).toContain('显示 1 / 1')
    expect(wrapper.text()).toContain('GPT-zhuyawen')
  })

  it('用户点过之后，刷新不会把人拽到别的模型', async () => {
    const wrapper = mountView(overview())
    await wrapper.findAll('.model-card')[0].trigger('click')
    await wrapper.setProps({ overview: overview() })
    expect(activeModel(wrapper)).toBe('gpt-6-astra')
  })

  it('门控清单变化后，选中模型若已不存在则回落到第一个', async () => {
    const wrapper = mountView(overview())
    expect(activeModel(wrapper)).toBe('gpt-5.6-sol')
    await wrapper.setProps({ overview: overview({ models: [modelView('gpt-6-astra')], advisories: [] }) })
    expect(activeModel(wrapper)).toBe('gpt-6-astra')
  })

  it('零持票的池显示「未启动」而不是跌破', async () => {
    const data = overview()
    data.models![1].pools![1].holders = 0
    data.models![1].pools![1].fresh = 0
    data.models![1].pools![1].floor_margin = 0
    const wrapper = mountView(data)
    const row = wrapper.findAll('tbody tr').find((r) => r.text().includes('gpt-pro-pool-2-auto'))
    expect(row?.text()).toContain('未启动')
    expect(row?.classes()).not.toContain('row-expired')
  })

  it('没有数据时不炸', async () => {
    const wrapper = mountView(null)
    expect(wrapper.text()).toContain('打票号池总览')
    expect(wrapper.findAll('.model-card')).toHaveLength(0)
    const harvest = wrapper.findAll('.section-tab').find((t) => t.text() === '打票')
    await harvest?.trigger('click')
    expect(wrapper.text()).toContain('暂无数据')
  })
})
