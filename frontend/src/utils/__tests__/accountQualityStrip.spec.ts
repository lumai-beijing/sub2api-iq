import { describe, expect, it } from 'vitest'
import type { PelicanTestConfig, ScheduledTestPlan, ScheduledTestResult } from '@/types'
import {
  buildQualityStrip,
  classifyStripResult,
  pickQualityStripPlan,
  QUALITY_STRIP_ROUNDS,
  qualityStripModels,
  qualityStripResultLimit
} from '../accountQualityStrip'

const candyConfig = (modelIds: string[], patch: Partial<PelicanTestConfig> = {}): PelicanTestConfig => ({
  question_kind: 'candy',
  prompt: '',
  reasoning_effort: 'xhigh',
  parallel_count: 1,
  model_ids: modelIds,
  ...patch
})

function plan(id: number, patch: Partial<ScheduledTestPlan> = {}): ScheduledTestPlan {
  return {
    id,
    account_id: 9,
    model_id: 'gpt-6-astra',
    cron_expression: '*/30 * * * *',
    enabled: true,
    max_results: 100,
    auto_recover: false,
    last_run_at: null,
    next_run_at: null,
    created_at: '',
    updated_at: '',
    pelican_config: candyConfig(['gpt-6-astra']),
    ...patch
  }
}

function result(id: number, patch: Partial<ScheduledTestResult> = {}): ScheduledTestResult {
  return {
    id,
    plan_id: 1,
    status: 'success',
    response_text: '21',
    error_message: '',
    latency_ms: 1200,
    started_at: '',
    finished_at: '',
    created_at: '2026-01-01T00:00:00Z',
    ...patch
  }
}

const resultConfig = (modelId: string): PelicanTestConfig => candyConfig([modelId], { model_id: modelId })

describe('accountQualityStrip', () => {
  describe('pickQualityStripPlan', () => {
    it('ignores non-candy rules and prefers the enabled rule with more models', () => {
      const pelican = plan(1, {
        last_run_at: '2026-01-09T00:00:00Z',
        pelican_config: candyConfig(['gpt-6-astra'], { question_kind: 'pelican' })
      })
      const stateProbe = plan(2, {
        last_run_at: '2026-01-09T00:00:00Z',
        pelican_config: candyConfig(['gpt-6-astra'], { question_kind: 'state_probe' })
      })
      const single = plan(3, { last_run_at: '2026-01-09T00:00:00Z' })
      const dual = plan(4, {
        last_run_at: '2026-01-01T00:00:00Z',
        pelican_config: candyConfig(['gpt-6-astra', 'gpt-6.1-sol'])
      })

      expect(pickQualityStripPlan([pelican, stateProbe, single, dual])?.id).toBe(4)
    })

    it('puts disabled rules behind every enabled rule', () => {
      const disabledDual = plan(5, {
        enabled: false,
        pelican_config: candyConfig(['gpt-6-astra', 'gpt-6.1-sol'])
      })
      const enabledSingle = plan(6, { enabled: true })

      expect(pickQualityStripPlan([disabledDual, enabledSingle])?.id).toBe(6)
    })

    it('breaks ties by newest last_run_at and then by largest id', () => {
      const older = plan(7, { last_run_at: '2026-01-01T00:00:00Z' })
      const newer = plan(8, { last_run_at: '2026-01-02T00:00:00Z' })
      const neverRun = plan(9, { last_run_at: null })
      expect(pickQualityStripPlan([neverRun, older, newer])?.id).toBe(8)

      const first = plan(10, { last_run_at: '2026-01-02T00:00:00Z' })
      const second = plan(11, { last_run_at: '2026-01-02T00:00:00Z' })
      expect(pickQualityStripPlan([first, second])?.id).toBe(11)
    })

    it('returns null when there is no candy rule', () => {
      expect(pickQualityStripPlan([])).toBeNull()
      expect(pickQualityStripPlan([
        plan(1, { pelican_config: candyConfig(['gpt-6-astra'], { question_kind: 'pelican' }) }),
        plan(2, { pelican_config: undefined })
      ])).toBeNull()
    })
  })

  describe('qualityStripModels', () => {
    it('trims, drops empties, deduplicates, and falls back to the plan model', () => {
      expect(qualityStripModels(plan(1, {
        pelican_config: candyConfig([' gpt-6-astra ', '', 'gpt-6.1-sol', 'gpt-6-astra'])
      }))).toEqual(['gpt-6-astra', 'gpt-6.1-sol'])

      expect(qualityStripModels(plan(2, {
        model_id: 'gpt-6-astra',
        pelican_config: candyConfig(['  '])
      }))).toEqual(['gpt-6-astra'])

      expect(qualityStripModels(plan(3, {
        model_id: '   ',
        pelican_config: candyConfig([])
      }))).toEqual([])
    })
  })

  describe('classifyStripResult', () => {
    it('classifies success as ok', () => {
      expect(classifyStripResult(result(1))).toBe('ok')
    })

    it('classifies answer mismatches as bad', () => {
      expect(classifyStripResult(result(1, { status: 'failed', error_message: 'answer_mismatch' }))).toBe('bad')
      expect(classifyStripResult(result(2, { status: 'failed', error_message: 'answer_mismatch: expected 21' }))).toBe('bad')
    })

    it('classifies an incorrect verdict as bad even without an answer_mismatch prefix', () => {
      expect(classifyStripResult(result(1, {
        status: 'failed',
        error_message: 'judge says no',
        quality_judgment: { verdict: 'incorrect', reason: '' }
      }))).toBe('bad')
    })

    it('treats upstream failures and inconclusive judgments as errors, not wrong answers', () => {
      expect(classifyStripResult(result(1, {
        status: 'failed',
        error_message: 'Our servers are currently overloaded'
      }))).toBe('error')
      expect(classifyStripResult(result(2, {
        status: 'failed',
        error_message: 'judge_inconclusive',
        quality_judgment: { verdict: 'unknown', reason: '' }
      }))).toBe('error')
      expect(classifyStripResult(result(3, { status: 'running' }))).toBe('error')
    })
  })

  describe('buildQualityStrip', () => {
    it('keeps only the newest six of eight rounds in oldest-to-newest order', () => {
      const stripPlan = plan(1, {
        pelican_config: candyConfig(['gpt-6-astra', 'gpt-6.1-sol'])
      })
      const results: ScheduledTestResult[] = []
      for (let round = 1; round <= 8; round += 1) {
        const firstId = round * 10 + 1
        results.push(result(firstId, {
          quality_round_id: `round-${round}`,
          created_at: `2026-01-0${round}T00:00:00Z`,
          pelican_config: resultConfig('gpt-6-astra')
        }))
        results.push(result(firstId + 1, {
          quality_round_id: `round-${round}`,
          created_at: `2026-01-0${round}T00:00:01Z`,
          pelican_config: resultConfig('gpt-6.1-sol')
        }))
      }

      const strip = buildQualityStrip(stripPlan, results)

      expect(strip.rounds).toHaveLength(QUALITY_STRIP_ROUNDS)
      expect(strip.rounds.map(round => round.key)).toEqual([
        'round-3', 'round-4', 'round-5', 'round-6', 'round-7', 'round-8'
      ])
      expect(strip.rounds[0].at).toBe('2026-01-03T00:00:00Z')
      expect(strip.rounds[5].at).toBe('2026-01-08T00:00:00Z')
      expect(strip.rounds.every(round => round.cells.every(cell => cell.state === 'ok'))).toBe(true)
    })

    it('groups results with the same round id into one cell in models order', () => {
      const stripPlan = plan(2, {
        pelican_config: candyConfig(['gpt-6.1-sol', 'gpt-6-astra'])
      })
      const strip = buildQualityStrip(stripPlan, [
        result(20, {
          quality_round_id: 'round-a',
          pelican_config: resultConfig('gpt-6-astra'),
          response_text: 'first model'
        }),
        result(21, {
          quality_round_id: 'round-a',
          pelican_config: resultConfig('gpt-6.1-sol'),
          response_text: 'second model'
        })
      ])

      expect(strip.rounds).toHaveLength(1)
      expect(strip.rounds[0].cells.map(cell => cell.model)).toEqual(['gpt-6.1-sol', 'gpt-6-astra'])
      expect(strip.rounds[0].cells.map(cell => cell.answer)).toEqual(['second model', 'first model'])
    })

    it('marks a model without a result in the round as none', () => {
      const stripPlan = plan(3, {
        pelican_config: candyConfig(['gpt-6-astra', 'gpt-6.1-sol'])
      })
      const strip = buildQualityStrip(stripPlan, [
        result(30, {
          quality_round_id: 'round-a',
          pelican_config: resultConfig('gpt-6-astra'),
          response_text: '21'
        })
      ])

      expect(strip.rounds[0].cells[0]).toMatchObject({ model: 'gpt-6-astra', state: 'ok', answer: '21' })
      expect(strip.rounds[0].cells[1]).toEqual({
        model: 'gpt-6.1-sol', state: 'none', answer: '', error: '', latencyMs: 0
      })
    })

    it('treats every empty round id as its own round', () => {
      const stripPlan = plan(4)
      const strip = buildQualityStrip(stripPlan, [
        result(40, { quality_round_id: '', pelican_config: resultConfig('gpt-6-astra') }),
        result(41, { quality_round_id: '', pelican_config: resultConfig('gpt-6-astra') })
      ])

      expect(strip.rounds.map(round => round.key)).toEqual(['r40', 'r41'])
    })

    it('drops results whose model is not in the rule models', () => {
      const stripPlan = plan(5, { pelican_config: candyConfig(['gpt-6-astra']) })
      const strip = buildQualityStrip(stripPlan, [
        result(50, { pelican_config: resultConfig('gpt-6.1-sol') }),
        result(51, { pelican_config: resultConfig('gpt-6-astra') })
      ])

      expect(strip.rounds).toHaveLength(1)
      expect(strip.rounds[0].key).toBe('r51')
    })

    it('merges duplicate model results in one round, preferring bad and the newest detail', () => {
      const stripPlan = plan(6)
      const strip = buildQualityStrip(stripPlan, [
        result(60, {
          quality_round_id: 'round-a',
          status: 'failed',
          error_message: 'answer_mismatch: expected 21',
          response_text: 'old',
          latency_ms: 900,
          pelican_config: resultConfig('gpt-6-astra')
        }),
        result(61, {
          quality_round_id: 'round-a',
          status: 'success',
          response_text: 'fresh answer',
          latency_ms: 1500,
          pelican_config: resultConfig('gpt-6-astra')
        })
      ])

      expect(strip.rounds[0].cells[0]).toMatchObject({
        state: 'bad',
        answer: 'fresh answer',
        latencyMs: 1500
      })
    })

    it('keeps the last 24 characters of the answer and truncates errors to 60 characters', () => {
      const stripPlan = plan(7)
      const tail = 'ABCDEFGHIJKLMNOPQRSTUVWX'
      const longError = 'E'.repeat(80)
      const strip = buildQualityStrip(stripPlan, [
        result(70, {
          quality_round_id: 'round-a',
          status: 'failed',
          error_message: longError,
          response_text: `${'prefix '.repeat(6)}\n${tail}\n`,
          pelican_config: resultConfig('gpt-6-astra')
        })
      ])

      expect(strip.rounds[0].cells[0].answer).toBe(tail)
      expect(strip.rounds[0].cells[0].answer).not.toContain('\n')
      expect(strip.rounds[0].cells[0].error).toBe('E'.repeat(60))
    })

    it('falls back to the plan id as the rule name', () => {
      const strip = buildQualityStrip(plan(88, {
        pelican_config: candyConfig(['gpt-6-astra'], { rule_name: '   ' })
      }), [])

      expect(strip.ruleName).toBe('#88')
      expect(strip.planId).toBe(88)
    })
  })

  describe('qualityStripResultLimit', () => {
    it('scales with models and parallel count, capped at 60', () => {
      expect(qualityStripResultLimit(plan(1, {
        pelican_config: candyConfig(['gpt-6-astra', 'gpt-6.1-sol'])
      }))).toBe(16)

      expect(qualityStripResultLimit(plan(2, {
        pelican_config: candyConfig(['gpt-6-astra', 'gpt-6.1-sol'], { parallel_count: 3 })
      }))).toBe(40)

      expect(qualityStripResultLimit(plan(3, {
        pelican_config: candyConfig(['gpt-6-astra', 'gpt-6.1-sol'], { parallel_count: 10 })
      }))).toBe(60)
    })
  })
})
