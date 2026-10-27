import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountQualityStrip from '../AccountQualityStrip.vue'
import type { QualityStrip, StripModelCell } from '@/utils/accountQualityStrip'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}(${Object.entries(params).map(([name, value]) => `${name}=${value}`).join(',')})` : key
    })
  }
})

const cell = (model: string, state: StripModelCell['state'], answer = '', error = ''): StripModelCell => ({
  model, state, answer, error, latencyMs: state === 'none' ? 0 : 6500
})

const strip = (rounds: QualityStrip['rounds']): QualityStrip => ({
  planId: 53,
  ruleName: '1027',
  models: ['gpt-6-astra', 'gpt-6.1-sol'],
  rounds
})

describe('AccountQualityStrip', () => {
  it('shows a dash when the account has no candy rule', () => {
    const wrapper = mount(AccountQualityStrip, { props: { strip: null } })
    expect(wrapper.text()).toBe('—')
  })

  it('pads to six cells and colors the top/bottom halves by model', () => {
    const wrapper = mount(AccountQualityStrip, {
      props: {
        strip: strip([
          { key: 'a', at: '2026-10-27T04:47:33Z', cells: [cell('gpt-6-astra', 'ok', '21个。'), cell('gpt-6.1-sol', 'bad', '29个', 'answer_mismatch')] }
        ])
      }
    })
    const cells = wrapper.findAll('[data-testid="account-quality-strip"] > div:first-child > div')
    expect(cells).toHaveLength(6)
    const halves = cells[5].findAll('div')
    expect(halves.map(h => h.classes())).toEqual([
      expect.arrayContaining(['bg-emerald-500']),
      expect.arrayContaining(['bg-red-500'])
    ])
    expect(cells[0].findAll('div').every(h => h.classes().includes('bg-gray-100'))).toBe(true)
    expect(wrapper.text()).toContain('admin.accounts.iqStrip.legend(top=6-astra,bottom=6.1-sol)')
  })

  it('shows the wrong answer for bad cells and the error for error cells in the tooltip', () => {
    const wrapper = mount(AccountQualityStrip, {
      props: {
        strip: strip([
          { key: 'a', at: '2026-10-27T04:47:33Z', cells: [cell('gpt-6-astra', 'error', '', 'Our servers are currently overloaded'), cell('gpt-6.1-sol', 'bad', '29个', 'answer_mismatch')] }
        ])
      }
    })
    const title = wrapper.findAll('[data-testid="account-quality-strip"] > div:first-child > div')[5].attributes('title') ?? ''
    expect(title).toContain('6-astra：Our servers are currently overloaded')
    expect(title).toContain('6.1-sol：29个（admin.accounts.iqStrip.seconds(n=6.5)）admin.accounts.iqStrip.bad')
    expect(title).not.toContain('answer_mismatch')
  })
})
