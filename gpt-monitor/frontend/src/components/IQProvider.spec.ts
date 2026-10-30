import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import IQProviderForm from './IQProviderForm.vue'
import IQProviderPanel from './IQProviderPanel.vue'
import type { IQProvider, IQProvidersView, IQStats } from '../types'

const limits = { max_providers: 20, max_models: 8, max_samples_per_model: 5 }

function stats(overrides: Partial<IQStats> = {}): IQStats {
  return { graded: 10, correct: 9, wrong: 1, no_answer: 0, errors: 0, swapped: 0, json_format: 10, accuracy_percent: 90, json_percent: 100, avg_duration_ms: 20000, ...overrides }
}

function provider(overrides: Partial<IQProvider> = {}): IQProvider {
  return {
    id: 3, name: '中转 A', base_url: 'https://relay-a.example.com/v1', endpoint: 'https://relay-a.example.com/v1/chat/completions',
    host: 'relay-a.example.com', api_format: 'chat_completions', api_key_hint: '••••3456', insecure: false,
    models: [{ model: 'gpt-6-astra', reasoning_effort: 'xhigh', label: 'gpt-6-astra@xhigh' }],
    schedule: { enabled: true, interval_seconds: 900, jitter_seconds: 60, samples_per_model: 2 },
    level: 'healthy', level_label: '智商正常',
    cards: [{
      spec: { model: 'gpt-6-astra', reasoning_effort: 'xhigh', label: 'gpt-6-astra@xhigh' }, level: 'healthy', level_label: '智商正常',
      recent: stats(), history: stats(), last_round: stats({ graded: 2, correct: 2 }), last_at: '2026-10-06T02:00:00Z',
      points: [
        { at: '2026-10-06T01:50:00Z', outcome: 'wrong', account_id: 0, account_name: '中转 A', answer: '29', upstream_model: 'gpt-6-astra', swapped: false, duration_ms: 1000, variant: 'fixed' },
        { at: '2026-10-06T02:00:00Z', outcome: 'correct', account_id: 0, account_name: '中转 A', answer: '21', upstream_model: 'gpt-6-astra', swapped: false, duration_ms: 1000, variant: 'random' }
      ]
    }],
    status: { running: false, running_targets: 0, last_run_at: '2026-10-06T02:00:00Z', last_finished_at: null, last_round_seconds: 40, next_run_at: '2026-10-06T02:14:30Z', next_anchor_at: '2026-10-06T02:15:00Z', skipped_slots: 0 },
    created_at: '2026-10-06T01:00:00Z', updated_at: '2026-10-06T01:00:00Z', ...overrides
  }
}

function jsonResponse(body: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

describe('IQProviderForm', () => {
  it('新增：校验、预览实际地址、估算每天题数，保存时发出完整配置', async () => {
    const wrapper = mount(IQProviderForm, { props: { provider: null, limits, defaultModels: 'gpt-6-astra@xhigh', saving: false } })
    const inputs = () => wrapper.findAll('input')
    expect(wrapper.find('.form-error').text()).toContain('名称')

    await inputs()[0].setValue('中转 B')
    await inputs()[1].setValue('https://relay-b.example.com/v1/chat/completions/')
    expect(wrapper.text()).toContain('https://relay-b.example.com/v1/chat/completions')
    expect(wrapper.find('.form-error').text()).toContain('API Key')
    await inputs()[2].setValue('sk-test-0000111122223333')
    expect(wrapper.find('.form-error').exists()).toBe(false)
    // 默认 15 分钟、1 个模型、每轮 1 次 → 每天 96 题
    expect(wrapper.text()).toContain('约每天 96 题')

    await inputs()[3].setValue('gpt-6-astra@turbo')
    expect(wrapper.find('.form-error').text()).toContain('推理强度无效')
    await inputs()[3].setValue('gpt-6-astra@xhigh, gpt-5.6-sol@max')

    await wrapper.find('form').trigger('submit')
    expect(wrapper.emitted('save')?.[0]?.[0]).toMatchObject({
      name: '中转 B', base_url: 'https://relay-b.example.com/v1/chat/completions/', api_format: 'chat_completions',
      api_key: 'sk-test-0000111122223333', models: 'gpt-6-astra@xhigh, gpt-5.6-sol@max',
      enabled: true, interval_seconds: 900, jitter_seconds: 60, samples_per_model: 1
    })
  })

  it('http 地址给出明文警告', async () => {
    const wrapper = mount(IQProviderForm, { props: { provider: null, limits, defaultModels: '', saving: false } })
    await wrapper.findAll('input')[1].setValue('http://relay.example.com/v1')
    expect(wrapper.text()).toContain('http 明文传输')
  })

  it('编辑：Key 可留空，但换了主机就必须重填', async () => {
    const wrapper = mount(IQProviderForm, { props: { provider: provider(), limits, defaultModels: '', saving: false } })
    expect(wrapper.find('.form-error').exists()).toBe(false) // 留空 Key 合法
    expect(wrapper.text()).toContain('当前 ••••3456')
    await wrapper.findAll('input')[1].setValue('https://evil.example.com/v1')
    expect(wrapper.find('.form-error').text()).toContain('重新填写 API Key')
    await wrapper.findAll('input')[2].setValue('sk-new-0000111122223333')
    expect(wrapper.find('.form-error').exists()).toBe(false)
  })

  it('填入号池测试模型', async () => {
    const wrapper = mount(IQProviderForm, { props: { provider: null, limits, defaultModels: 'gpt-6-astra@medium,gpt-6.1-sol@medium', saving: false } })
    await wrapper.findAll('input')[3].setValue('x')
    await wrapper.find('.text-button').trigger('click')
    expect((wrapper.findAll('input')[3].element as HTMLInputElement).value).toBe('gpt-6-astra@medium,gpt-6.1-sol@medium')
  })
})

describe('IQProviderPanel', () => {
  beforeEach(() => {
    vi.useFakeTimers({ now: new Date('2026-10-06T02:10:00Z'), toFake: ['Date', 'setTimeout', 'clearTimeout'] })
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('列出供应商：只露 Key 末 4 位，显示独立周期与下次时间，每个模型一行摘要', async () => {
    const view: IQProvidersView = { generated_at: '', providers: [provider()], limits, default_models: 'gpt-6-astra@xhigh' }
    vi.stubGlobal('fetch', vi.fn(() => jsonResponse(view)))
    const wrapper = mount(IQProviderPanel, { props: { now: Date.now(), refreshKey: 'a' } })
    await flushPromises()
    const card = wrapper.find('.iq-provider')
    expect(card.text()).toContain('中转 A')
    expect(card.text()).toContain('••••3456')
    expect(card.text()).toContain('每 15 分钟（±1 分钟）')
    expect(card.text()).toContain('每个模型每轮 2 次')
    expect(card.text()).toContain('4 分 30 秒后')
    const models = card.findAll('.iq-provider-model')
    expect(models).toHaveLength(1)
    expect(models[0].text()).toContain('90%')
    expect(models[0].findAll('.iq-pip.variant-fixed')).toHaveLength(1) // 固定题面的点画浅

    await models[0].findAll('.iq-provider-model-actions button')[1].trigger('click') // 看详情
    expect(wrapper.emitted('select-cell')?.[0]).toEqual(['provider:3', '中转 A', 'gpt-6-astra@xhigh'])
  })

  it('点「立即检测」调对应供应商的接口', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url} ${init?.body ?? ''}`)
      if (url.includes('/run')) return jsonResponse({ kind: 'provider', targets: 2 }, 202)
      return jsonResponse({ generated_at: '', providers: [provider()], limits, default_models: '' })
    }))
    const wrapper = mount(IQProviderPanel, { props: { now: Date.now(), refreshKey: 'a' } })
    await flushPromises()
    await wrapper.find('.iq-provider-model-actions button').trigger('click') // 只测这个模型
    await flushPromises()
    expect(calls.some((c) => c.startsWith('POST /api/v1/iq/providers/3/run') && c.includes('gpt-6-astra@xhigh'))).toBe(true)
    expect(wrapper.emitted('flash')?.[0]?.[0]).toBe('检测已受理')
  })
})
