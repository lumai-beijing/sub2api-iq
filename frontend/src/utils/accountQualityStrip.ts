import type { ScheduledTestPlan, ScheduledTestResult } from '@/types'

export type StripCellState = 'ok' | 'bad' | 'error' | 'none'

export interface StripModelCell {
  model: string
  state: StripCellState
  answer: string
  error: string
  latencyMs: number
}

export interface StripRound {
  key: string
  at: string
  cells: StripModelCell[]
}

export interface QualityStrip {
  planId: number
  ruleName: string
  models: string[]
  rounds: StripRound[]
}

export const QUALITY_STRIP_ROUNDS = 6

const trimString = (value: unknown): string => (typeof value === 'string' ? value.trim() : '')

export function qualityStripModels(plan: ScheduledTestPlan): string[] {
  const models = [
    ...new Set(
      (plan.pelican_config?.model_ids ?? [])
        .map(trimString)
        .filter(Boolean)
    )
  ]
  if (models.length > 0) return models

  const fallback = trimString(plan.model_id)
  return fallback ? [fallback] : []
}

const lastRunTime = (plan: ScheduledTestPlan): number | null => {
  if (!plan.last_run_at) return null
  const time = Date.parse(plan.last_run_at)
  return Number.isFinite(time) ? time : null
}

export function pickQualityStripPlan(plans: ScheduledTestPlan[]): ScheduledTestPlan | null {
  const candidates = plans.filter(plan => plan.pelican_config?.question_kind === 'candy')
  if (candidates.length === 0) return null

  return [...candidates].sort((left, right) => {
    if (left.enabled !== right.enabled) return left.enabled ? -1 : 1

    const modelDelta = qualityStripModels(right).length - qualityStripModels(left).length
    if (modelDelta !== 0) return modelDelta

    const leftTime = lastRunTime(left)
    const rightTime = lastRunTime(right)
    if (leftTime === null && rightTime !== null) return 1
    if (leftTime !== null && rightTime === null) return -1
    if (leftTime !== null && rightTime !== null && leftTime !== rightTime) {
      return rightTime - leftTime
    }

    return right.id - left.id
  })[0]
}

export function classifyStripResult(result: ScheduledTestResult): StripCellState {
  if (result.status === 'success') return 'ok'
  if (
    result.status === 'failed' &&
    (
      (result.error_message ?? '').startsWith('answer_mismatch') ||
      result.quality_judgment?.verdict === 'incorrect'
    )
  ) {
    return 'bad'
  }
  return 'error'
}

interface RoundGroup {
  key: string
  maxResultId: number
  at: string
  atTime: number
  byModel: Map<string, ScheduledTestResult[]>
}

// 糖果题要求输出 {"answer": 21, ...}：有 answer 字段就只显示它的值（取最后一个），否则显示回答结尾。
const answerFieldPattern = /"answer"\s*:\s*(?:(-?\d+(?:\.\d+)?)|"([^"]*)")/g

const formatAnswer = (text: string): string => {
  const raw = text ?? ''
  const last = [...raw.matchAll(answerFieldPattern)].pop()
  const value = last?.[1] ?? last?.[2]?.match(/-?\d+(?:\.\d+)?/)?.[0]
  if (value) return value
  const normalized = raw.trim().replace(/\r\n|\r|\n/g, ' ')
  return normalized.length > 24 ? normalized.slice(-24) : normalized
}

const formatError = (text: string): string => (text ?? '').slice(0, 60)

const resultModel = (result: ScheduledTestResult, plan: ScheduledTestPlan): string =>
  trimString(result.pelican_config?.model_id) || trimString(plan.model_id)

const mergeCellState = (results: ScheduledTestResult[]): StripCellState => {
  const states = results.map(classifyStripResult)
  if (states.includes('bad')) return 'bad'
  if (states.includes('ok')) return 'ok'
  return 'error'
}

const latestResult = (results: ScheduledTestResult[]): ScheduledTestResult =>
  results.reduce((latest, result) => (result.id >= latest.id ? result : latest))

export function buildQualityStrip(plan: ScheduledTestPlan, results: ScheduledTestResult[]): QualityStrip {
  const models = qualityStripModels(plan)
  const groups = new Map<string, RoundGroup>()

  for (const result of results) {
    const model = resultModel(result, plan)
    if (!models.includes(model)) continue

    const roundId = trimString(result.quality_round_id)
    const key = roundId || `r${result.id}`
    let group = groups.get(key)
    if (!group) {
      const createdTime = Date.parse(result.created_at)
      group = {
        key,
        maxResultId: result.id,
        at: result.created_at,
        atTime: Number.isFinite(createdTime) ? createdTime : Number.POSITIVE_INFINITY,
        byModel: new Map()
      }
      groups.set(key, group)
    } else {
      const createdTime = Date.parse(result.created_at)
      if (Number.isFinite(createdTime) && createdTime < group.atTime) {
        group.atTime = createdTime
        group.at = result.created_at
      }
      group.maxResultId = Math.max(group.maxResultId, result.id)
    }

    const entries = group.byModel.get(model) ?? []
    entries.push(result)
    group.byModel.set(model, entries)
  }

  const rounds = [...groups.values()]
    .sort((left, right) => left.maxResultId - right.maxResultId)
    .slice(-QUALITY_STRIP_ROUNDS)
    .map((group): StripRound => ({
      key: group.key,
      at: group.at,
      cells: models.map((model): StripModelCell => {
        const entries = group.byModel.get(model)
        if (!entries || entries.length === 0) {
          return { model, state: 'none', answer: '', error: '', latencyMs: 0 }
        }
        const latest = latestResult(entries)
        const latency = Number(latest.latency_ms)
        return {
          model,
          state: mergeCellState(entries),
          answer: formatAnswer(latest.response_text),
          error: formatError(latest.error_message),
          latencyMs: Number.isFinite(latency) ? latency : 0
        }
      })
    }))

  const ruleName = trimString(plan.pelican_config?.rule_name) || `#${plan.id}`

  return {
    planId: plan.id,
    ruleName,
    models,
    rounds
  }
}

export function qualityStripResultLimit(plan: ScheduledTestPlan): number {
  const modelCount = Math.max(1, qualityStripModels(plan).length)
  const parallelCount = Math.max(1, plan.pelican_config?.parallel_count || 1)
  return Math.min(60, QUALITY_STRIP_ROUNDS * modelCount * parallelCount + 4)
}
