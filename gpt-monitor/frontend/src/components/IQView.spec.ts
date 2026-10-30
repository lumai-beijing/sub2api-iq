import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import IQView from './IQView.vue'
import type { IQAccountsView, IQCardView } from '../types'
import { board, overview, stats } from '../test/iqFixtures'

const accounts: IQAccountsView = {
  generated_at: '2026-10-06T02:00:00Z', window: '7d', group_id: 0, since: '2026-09-29T02:00:00Z', min_samples: 5,
  tested: 2, available: 51, overall: stats(),
  accounts: [
    { ...stats({ accuracy_percent: 40, correct: 4, graded: 10 }), account_id: 101, account_name: 'low-iq', account_type: 'oauth',
      groups: [{ id: 8, name: 'gpt-pro-pool-1' }, { id: 37, name: 'gpt-pro-5' }], available: true, insufficient: false,
      last_at: '2026-10-06T01:59:00Z', recent: ['wrong', 'correct', 'wrong'], last_answer: '29', last_upstream_model: 'gpt-6-astra',
      models: [{ ...stats(), model: 'gpt-6-astra', reasoning_effort: 'medium', last_at: null }] },
    { ...stats({ accuracy_percent: 100, correct: 2, graded: 2 }), account_id: 49, account_name: 'timicc-key', account_type: 'apikey',
      groups: [{ id: 35, name: 'server-timicc' }], available: false, insufficient: true,
      last_at: '2026-10-06T01:50:00Z', recent: ['correct', 'correct'], last_answer: '21', last_upstream_model: 'gpt-6-astra', models: [] }
  ]
}

const cardView: IQCardView = {
  generated_at: '2026-10-06T02:00:00Z', source_key: 'group:8', source_name: '分组 gpt-pro-pool-1', kind: 'group', scope: 'all',
  card: {
    spec: { model: 'gpt-6-astra', reasoning_effort: 'medium', label: 'gpt-6-astra@medium' }, level: 'healthy', level_label: '智商正常',
    recent: stats({ accuracy_percent: 85 }), history: stats(), last_round: stats(), last_at: '2026-10-06T01:59:00Z',
    points: [
      { at: '2026-10-06T01:50:00Z', outcome: 'wrong', account_id: 1, account_name: 'a', answer: '29', upstream_model: 'gpt-6-astra', swapped: false, duration_ms: 1000, variant: 'fixed' },
      { at: '2026-10-06T01:59:00Z', outcome: 'correct', account_id: 3, account_name: 'c', answer: '21', upstream_model: 'gpt-6-astra', swapped: false, duration_ms: 1000, variant: 'random' }
    ]
  },
  probes: [{
    id: 9, provider_id: 0, run_id: 'r', account_id: 3, account_name: 'c', account_type: 'oauth', sample_group_id: 8, group_ids: [8, 37],
    variant: 'random', prompt: '题目：\n一只黑色的布袋里装着三种口味的糖……', model: 'gpt-6-astra', reasoning_effort: 'medium', outcome: 'correct',
    answer_value: '21', answer_format: 'json', answer_reasoning: '圆 9 + 星 12', answer_excerpt: '{"answer":21}', upstream_model: 'gpt-6-astra',
    model_verdict: 'same', ticket_injected: true, error_message: '', duration_ms: 23000, trigger: 'schedule', created_at: '2026-10-06T01:59:00Z'
  }]
}

function jsonResponse(body: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

const groupsView = {
  generated_at: '', window: '7d', since: null, min_samples: 5, config: { ids: [8], per_round: 2 }, limits: { max_groups: 30, max_per_round: 10 },
  pool: stats({ accuracy_percent: 25 }), groups: []
}

describe('IQView', () => {
  let calls: string[]
  beforeEach(() => {
    calls = []
    vi.useFakeTimers({ now: new Date('2026-10-06T02:00:00Z'), toFake: ['Date', 'setInterval', 'clearInterval', 'setTimeout', 'clearTimeout'] })
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url} ${init?.body ?? ''}`)
      if (url.startsWith('/api/v1/iq/accounts')) return jsonResponse(accounts)
      if (url.startsWith('/api/v1/iq/card')) return jsonResponse(cardView)
      if (url.startsWith('/api/v1/iq/board')) return jsonResponse(board({ window: '7d', advisories: null }))
      if (url.startsWith('/api/v1/iq/groups')) return jsonResponse(groupsView)
      if (url.startsWith('/api/v1/iq/probes')) return jsonResponse({ generated_at: '', probes: cardView.probes })
      if (url.startsWith('/api/v1/iq/run')) return jsonResponse({ run_id: 'r', kind: 'group', targets: 2 }, 202)
      return jsonResponse({ error: 'not found' }, 404)
    }))
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('头部与结论：号池状态、下一轮倒计时、本轮构成，结论按严重程度列出', async () => {
    const wrapper = mount(IQView, { props: { overview: overview(), error: '' } })
    await flushPromises()
    const chips = wrapper.find('.state-chips').text()
    expect(chips).toContain('号池 · 降智')
    expect(chips).toContain('下一轮 4 分 51 秒后')
    expect(chips).toContain('+ 1 个分组各补到 2 个')
    expect(wrapper.find('.eyebrow').text()).toContain('题面每次随机改写')
    const rows = wrapper.findAll('.iq-advisories .advisory-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].classes()).toContain('tone-critical')
    expect(rows[0].text()).toContain('号池整体')
    expect(rows[0].text()).toContain('gpt-6-astra@medium')
  })

  it('总览矩阵：号池、分组、第三方分段，格子带答对率、迷你历史条、最高与差值标记', async () => {
    const wrapper = mount(IQView, { props: { overview: overview(), error: '' } })
    await flushPromises()
    const table = wrapper.find('.iq-board-table')
    expect(table.findAll('.iq-board-section').map((s) => s.text())).toEqual([
      expect.stringContaining('分组'), expect.stringContaining('第三方供应商')
    ])
    const poolRow = table.find('tr[data-key="pool"]')
    expect(poolRow.classes()).toContain('row-expired')
    expect(poolRow.find('.iq-board-td').classes()).toContain('tone-danger')
    expect(poolRow.find('.iq-board-acc').text()).toBe('25%')
    // 迷你条：固定题面的点画浅
    const pips = poolRow.find('.iq-strip.mini').findAll('.iq-pip:not(.pad)')
    expect(pips).toHaveLength(3)
    expect(pips[0].classes()).toContain('variant-fixed')
    expect(pips[2].attributes('title')).toContain('答 21')

    const groupRow = table.find('tr[data-key="group:8"]')
    expect(groupRow.text()).toContain('比号池 +60')
    expect(groupRow.text()).toContain('最高')
    expect(groupRow.text()).toContain('样本不足')
    expect(groupRow.find('.iq-board-status').text()).toContain('跟号池一起')

    const providerRow = table.find('tr[data-key="provider:3"]')
    expect(providerRow.findAll('.iq-board-td')[1].text()).toBe('—') // 没在测这个模型
  })

  it('点格子展开详情：拉单格数据，展示完整历史条和最近作答（含题面与分组）', async () => {
    const wrapper = mount(IQView, { props: { overview: overview(), error: '' } })
    await flushPromises()
    await wrapper.find('tr[data-key="group:8"] .iq-board-cell').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.startsWith('GET /api/v1/iq/card?source=group%3A8&model=gpt-6-astra%40medium&scope=all'))).toBe(true)
    const detail = wrapper.find('.iq-cell-detail')
    expect(detail.text()).toContain('分组 gpt-pro-pool-1')
    expect(detail.text()).toContain('浅色是随机化之前的固定题面')
    expect(detail.find('.iq-probe-table').text()).toContain('这次的题面')
    expect(detail.find('.iq-group-chip.sampled').text()).toBe('gpt-pro-pool-1')

    // 详情里的「立即检测」测的是这个分组、这个模型
    await detail.find('.secondary-button').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.startsWith('POST /api/v1/iq/run') && c.includes('"group_id":8') && c.includes('gpt-6-astra@medium'))).toBe(true)
    expect(wrapper.find('.error-banner.soft').text()).toContain('检测已受理')

    await detail.find('[aria-label="收起详情"]').trigger('click')
    expect(wrapper.find('.iq-cell-detail').exists()).toBe(false)
  })

  it('点结论直接打开对应的格子，并展开解释', async () => {
    const wrapper = mount(IQView, { props: { overview: overview(), error: '' } })
    await flushPromises()
    await wrapper.find('.iq-advisories .advisory-row').trigger('click')
    await flushPromises()
    expect(wrapper.find('.advisory-detail').text()).toContain('最常见的错答是 29')
    expect(wrapper.find('.iq-board-cell.active').exists()).toBe(true)
    expect(calls.some((c) => c.includes('/api/v1/iq/card?source=pool&model=gpt-6-astra%40medium'))).toBe(true)
  })

  it('切换口径时单独拉矩阵；回到默认口径用总览的数据', async () => {
    const wrapper = mount(IQView, { props: { overview: overview(), error: '' } })
    await flushPromises()
    await wrapper.findAll('.iq-board .iq-segment button')[2].trigger('click') // 7 天
    await flushPromises()
    expect(calls.some((c) => c.startsWith('GET /api/v1/iq/board?window=7d&scope=all'))).toBe(true)
    expect(wrapper.find('.iq-board-sub').text()).toContain('近 7 天')
    await wrapper.find('.iq-board-scope').setValue('random')
    await flushPromises()
    expect(calls.some((c) => c.startsWith('GET /api/v1/iq/board?window=7d&scope=random'))).toBe(true)
  })

  it('sub2api 版本不够时，号池和分组的立即检测都不可用', async () => {
    const blocked = overview({
      level: 'blocked', level_label: '等待 sub2api 支持',
      gate: { open: false, required: 'lmuai-0.2.13.2', current: 'lmuai-0.2.10.3', checked_at: null }
    })
    const wrapper = mount(IQView, { props: { overview: blocked, error: '' } })
    await flushPromises()
    expect(wrapper.find('.chip-crit').text()).toContain('等待 sub2api 支持')
    const buttons = wrapper.findAll('.iq-board-status .text-button')
    expect(buttons[0].attributes('disabled')).toBeDefined() // 号池
    expect(buttons[1].attributes('disabled')).toBeDefined() // 分组
    expect(buttons[2].attributes('disabled')).toBeUndefined() // 第三方不受影响
  })

  it('账号排行：按分组筛选，展示号所在的分组和 API Key 号', async () => {
    const wrapper = mount(IQView, { props: { overview: overview(), error: '' } })
    await flushPromises()
    await wrapper.findAll('.section-tab')[2].trigger('click')
    await flushPromises()
    const rows = wrapper.findAll('.detail tbody tr')
    expect(rows[0].text()).toContain('low-iq')
    expect(rows[0].classes()).toContain('row-expired')
    expect(rows[0].findAll('.iq-group-chip').map((c) => c.text())).toEqual(['gpt-pro-pool-1', 'gpt-pro-5'])
    expect(rows[1].text()).toContain('API Key 号')
    expect(rows[1].text()).toContain('当前不可用')

    await rows[0].find('.iq-group-chip').trigger('click') // 点分组标签 = 只看这个分组
    await flushPromises()
    expect(calls.some((c) => c.startsWith('GET /api/v1/iq/accounts?window=7d&group_id=8'))).toBe(true)
  })
})
