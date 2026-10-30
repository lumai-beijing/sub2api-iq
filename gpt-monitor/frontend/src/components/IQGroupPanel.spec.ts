import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import IQGroupPanel from './IQGroupPanel.vue'
import type { IQGroupRow, IQGroupsView } from '../types'
import { stats } from '../test/iqFixtures'

const thresholds = { recent_window: 30, min_samples: 5, healthy_percent: 80, warning_percent: 50, history_points: 180 }

function group(overrides: Partial<IQGroupRow>): IQGroupRow {
  return {
    id: 1, name: 'g', status: 'active', selected: false, missing: false, members: 10, oauth: 10, apikey: 0, available: 5,
    requests_24h: 0, stats: stats({ graded: 0, correct: 0, wrong: 0, errors: 0, accuracy_percent: null }), insufficient: true, last_at: null,
    ...overrides
  }
}

function view(overrides: Partial<IQGroupsView> = {}): IQGroupsView {
  return {
    generated_at: '', window: '7d', since: null, min_samples: 5, config: { ids: [8], per_round: 2 },
    limits: { max_groups: 30, max_per_round: 10 }, pool: stats({ accuracy_percent: 25, correct: 5, graded: 20 }),
    groups: [
      group({ id: 8, name: 'gpt-pro-pool-1', selected: true, members: 39, available: 11, requests_24h: 7742,
        stats: stats({ accuracy_percent: 40, correct: 8, graded: 20 }), insufficient: false, last_at: '2026-10-06T01:59:00Z' }),
      group({ id: 6, name: 'gpt-pro-pool-2-auto', members: 42, available: 14, requests_24h: 8159 }),
      group({ id: 33, name: 'vd-openai-pool', members: 10, oauth: 0, apikey: 10, available: 4 }),
      group({ id: 3, name: 'gpt-pro-10', members: 4, available: 0 }),
      group({ id: 99, name: '#99', selected: false, missing: true, members: 0, available: 0, requests_24h: -1 })
    ],
    ...overrides
  }
}

function jsonResponse(body: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

describe('IQGroupPanel', () => {
  let calls: string[]
  beforeEach(() => {
    calls = []
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url} ${init?.body ?? ''}`)
      if (url.startsWith('/api/v1/iq/settings')) {
        return jsonResponse({ models: [], models_source: 'settings', schedule: {}, schedule_source: 'settings', groups: { ids: [8, 6], per_round: 3 }, paused: false })
      }
      return jsonResponse(view())
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('列出全部 OpenAI 分组：成员构成、可用数、24 小时请求、答对率与号池参照', async () => {
    const wrapper = mount(IQGroupPanel, { props: { now: Date.now(), refreshKey: 'a', canRun: true, thresholds } })
    await flushPromises()
    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(5)
    expect(rows[0].text()).toContain('gpt-pro-pool-1')
    expect(rows[0].text()).toContain('检测中')
    expect(rows[0].text()).toContain('7,742')
    expect(rows[0].classes()).toContain('row-expired') // 40% < 50%
    expect((rows[0].find('input').element as HTMLInputElement).checked).toBe(true)
    expect(rows[2].text()).toContain('API Key 10')
    expect(rows[3].find('.warning-text').text()).toBe('0') // 有号但一个都不可用
    expect(rows[3].findAll('.text-button')[0].attributes('disabled')).toBeDefined()
    expect(rows[4].text()).toContain('sub2api 里已删除')
    expect(wrapper.find('.table-meta').text()).toContain('号池整体同期 25%')
    expect(wrapper.find('.primary-button').attributes('disabled')).toBeDefined() // 没改过不能保存
  })

  it('勾选顺序即矩阵顺序，保存时发出分组和每轮个数', async () => {
    const wrapper = mount(IQGroupPanel, { props: { now: Date.now(), refreshKey: 'a', canRun: true, thresholds } })
    await flushPromises()
    await wrapper.findAll('tbody tr')[1].find('input').setValue(true) // 加选 6
    expect(wrapper.text()).toContain('已勾选 2 个分组')
    const perRound = wrapper.find('.iq-groups-foot input')
    await perRound.setValue(11)
    expect(wrapper.find('.form-error').text()).toContain('1 到 10')
    await perRound.setValue(3)
    expect(wrapper.text()).toContain('最多再测 6 个号')
    await wrapper.find('.primary-button').trigger('click')
    await flushPromises()
    const put = calls.find((c) => c.startsWith('PUT /api/v1/iq/settings'))
    expect(put).toContain('"groups":{"ids":[8,6],"per_round":3}')
    expect(wrapper.emitted('saved')?.[0]?.[0]).toEqual([8, 6])
    expect(wrapper.emitted('flash')?.[0]?.[1]).toContain('2 个分组各至少测 3 个号')
  })

  it('行内操作：立即检测、看账号、采样详情', async () => {
    const wrapper = mount(IQGroupPanel, { props: { now: Date.now(), refreshKey: 'a', canRun: true, thresholds } })
    await flushPromises()
    const buttons = wrapper.findAll('tbody tr')[0].findAll('.text-button')
    await buttons[0].trigger('click')
    await buttons[1].trigger('click')
    await buttons[2].trigger('click')
    expect((wrapper.emitted('run')?.[0]?.[0] as IQGroupRow).id).toBe(8)
    expect((wrapper.emitted('open-accounts')?.[0]?.[0] as IQGroupRow).id).toBe(8)
    expect((wrapper.emitted('open-probes')?.[0]?.[0] as IQGroupRow).id).toBe(8)
  })
})
