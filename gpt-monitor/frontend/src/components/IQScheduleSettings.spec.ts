import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import IQScheduleSettings from './IQScheduleSettings.vue'
import type { IQSchedule } from '../types'

function schedule(overrides: Partial<IQSchedule> = {}): IQSchedule {
  return {
    enabled: true, interval_seconds: 300, jitter_seconds: 60, sample_mode: 'ratio', sample_percent: 10, sample_min: 2, sample_count: 5,
    config_source: 'default', paused: false, concurrency: 2, running: false, running_targets: 0,
    last_run_at: null, last_finished_at: null, last_round_seconds: 84,
    next_run_at: '2026-10-06T02:04:51Z', next_anchor_at: '2026-10-06T02:05:00Z', skipped_slots: 0,
    candidates: 51, sample_size: 6, groups: 0, group_per_round: 2, last_round_pool: 0, last_round_top_ups: 0, ...overrides
  }
}

const now = new Date('2026-10-06T02:00:00Z').getTime()

describe('IQScheduleSettings', () => {
  it('回显当前配置：周期按分钟、随机按秒、按比例预览抽几个', () => {
    const wrapper = mount(IQScheduleSettings, { props: { schedule: schedule(), saving: false, now } })
    const numbers = wrapper.findAll('input[type="number"]').map((i) => (i.element as HTMLInputElement).value)
    expect(numbers).toEqual(['10', '2', '5', '1']) // 比例、至少、周期 5 分钟、随机 1 分钟（60 秒整除，按分钟显示）
    const units = wrapper.findAll('select').map((s) => (s.element as HTMLSelectElement).value)
    expect(units).toEqual(['60', '60'])
    expect(wrapper.text()).toContain('每轮抽 6 个')
    expect(wrapper.text()).toContain('下次锚点')
    expect(wrapper.find('.primary-button').attributes('disabled')).toBeDefined() // 没改过不能保存
  })

  it('改成按个数 + 1 小时周期 ±5 分钟，保存时发出完整配置', async () => {
    const wrapper = mount(IQScheduleSettings, { props: { schedule: schedule(), saving: false, now } })
    await wrapper.findAll('.iq-segment button')[1].trigger('click')
    await wrapper.find('input[type="number"]').setValue(8) // 按个数时第一个数字框是「每轮」
    const [intervalInput, jitterInput] = wrapper.findAll('input[type="number"]').slice(1)
    const [intervalUnit, jitterUnit] = wrapper.findAll('select')
    await intervalInput.setValue(1)
    await intervalUnit.setValue('3600')
    await jitterInput.setValue(5)
    await jitterUnit.setValue('60')
    expect(wrapper.text()).toContain('每轮抽 8 个')
    expect(wrapper.text()).toContain('北京时间每个整点')

    await wrapper.find('.primary-button').trigger('click')
    expect(wrapper.emitted('save')?.[0]?.[0]).toEqual({
      enabled: true, interval_seconds: 3600, jitter_seconds: 300,
      sample_mode: 'count', sample_percent: 10, sample_min: 2, sample_count: 8
    })
  })

  it('随机范围超过周期一半时报错并禁止保存', async () => {
    const wrapper = mount(IQScheduleSettings, { props: { schedule: schedule(), saving: false, now } })
    const jitterInput = wrapper.findAll('input[type="number"]')[3]
    await wrapper.findAll('select')[1].setValue('1') // 随机范围改按秒填
    await jitterInput.setValue(200) // 5 分钟周期最多 ±150 秒
    expect(wrapper.find('.form-error').text()).toContain('最多是周期的一半')
    expect(wrapper.find('.primary-button').attributes('disabled')).toBeDefined()
    await jitterInput.setValue(150)
    expect(wrapper.find('.form-error').exists()).toBe(false)
  })

  it('比例低于下限时提示按下限取', async () => {
    const wrapper = mount(IQScheduleSettings, { props: { schedule: schedule({ candidates: 20 }), saving: false, now } })
    await wrapper.findAll('input[type="number"]')[1].setValue(5) // 20 个的 10% = 2，下限 5
    expect(wrapper.text()).toContain('每轮抽 5 个')
    expect(wrapper.text()).toContain('按下限取 5 个')
  })

  it('选了分组时说明补测，并按上一轮的构成预估', () => {
    const wrapper = mount(IQScheduleSettings, {
      props: { schedule: schedule({ groups: 3, group_per_round: 2, last_round_pool: 6, last_round_top_ups: 4 }), saving: false, now }
    })
    expect(wrapper.text()).toContain('另外给 3 个分组各补测到至少 2 个号')
    expect(wrapper.text()).toContain('上一轮共测 10 个：号池 6 + 分组补测 4')
  })

  it('关闭开关后保存 enabled=false；周期太短时提示会追不上', async () => {
    const wrapper = mount(IQScheduleSettings, { props: { schedule: schedule({ last_round_seconds: 240 }), saving: false, now } })
    // 上一轮 6 个号跑了 240 秒；改成每轮 30 个、1 分钟周期 → 估算远超周期
    await wrapper.findAll('.iq-segment button')[1].trigger('click')
    await wrapper.find('input[type="number"]').setValue(30)
    const [intervalInput, jitterInput] = wrapper.findAll('input[type="number"]').slice(1)
    await intervalInput.setValue(1)
    await jitterInput.setValue(0)
    expect(wrapper.find('.iq-warning').text()).toContain('会自动跳过追不上的轮次')

    await wrapper.find('.iq-switch input').setValue(false)
    expect(wrapper.find('.iq-warning').exists()).toBe(false) // 关闭时不提示
    await wrapper.find('.primary-button').trigger('click')
    expect((wrapper.emitted('save')?.[0]?.[0] as { enabled: boolean }).enabled).toBe(false)
  })

  it('服务端配置更新后跟着刷新，但不覆盖未保存的修改', async () => {
    const wrapper = mount(IQScheduleSettings, { props: { schedule: schedule(), saving: false, now } })
    await wrapper.setProps({ schedule: schedule({ interval_seconds: 600, config_source: 'settings' }) })
    expect((wrapper.findAll('input[type="number"]')[2].element as HTMLInputElement).value).toBe('10')

    await wrapper.findAll('input[type="number"]')[0].setValue(30) // 本地改了比例，没保存
    await wrapper.setProps({ schedule: schedule({ interval_seconds: 900, config_source: 'settings' }) })
    expect((wrapper.findAll('input[type="number"]')[0].element as HTMLInputElement).value).toBe('30')
  })
})
