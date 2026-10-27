<template>
  <div data-testid="account-quality-strip" role="img" :aria-label="ariaLabel">
    <span
      v-if="!strip"
      class="text-xs text-gray-400 dark:text-gray-500"
      :title="t('admin.accounts.iqStrip.noPlan')"
    >—</span>
    <template v-else>
      <div class="flex gap-0.5">
        <div
          v-for="(item, index) in displayRounds"
          :key="item.round?.key ?? `blank-${index}`"
          class="flex h-6 w-3.5 flex-col overflow-hidden rounded-sm border border-gray-200 dark:border-dark-600"
          :title="cellTitle(item)"
        >
          <div
            v-for="cell in item.cells"
            :key="cell.model"
            class="flex-1"
            :class="stateClasses[cell.state]"
          />
        </div>
      </div>
      <div v-if="legend" class="mt-0.5 text-[10px] text-gray-400">{{ legend }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  QUALITY_STRIP_ROUNDS,
  type QualityStrip,
  type StripCellState,
  type StripModelCell,
  type StripRound
} from '@/utils/accountQualityStrip'

const props = defineProps<{
  strip?: QualityStrip | null
}>()

const { t } = useI18n()

const stateClasses: Record<StripCellState, string> = {
  ok: 'bg-emerald-500',
  bad: 'bg-red-500',
  error: 'bg-gray-400 dark:bg-gray-500',
  none: 'bg-gray-100 dark:bg-dark-700'
}

interface DisplayRound {
  round: StripRound | null
  cells: StripModelCell[]
}

const shortModel = (model: string): string => model.replace(/^gpt-/, '')

const stateLabel = (state: StripCellState): string => {
  if (state === 'ok') return t('admin.accounts.iqStrip.ok')
  if (state === 'bad') return t('admin.accounts.iqStrip.bad')
  if (state === 'error') return t('admin.accounts.iqStrip.error')
  return t('admin.accounts.iqStrip.none')
}

const emptyCell = (model: string): StripModelCell => ({
  model,
  state: 'none',
  answer: '',
  error: '',
  latencyMs: 0
})

const displayRounds = computed<DisplayRound[]>(() => {
  if (!props.strip) return []
  const models = props.strip.models
  const blanks = Math.max(0, QUALITY_STRIP_ROUNDS - props.strip.rounds.length)
  const padded: DisplayRound[] = Array.from({ length: blanks }, () => ({
    round: null,
    cells: models.map(emptyCell)
  }))
  return [
    ...padded,
    ...props.strip.rounds.map(round => ({ round, cells: round.cells }))
  ]
})

const padNumber = (value: number): string => String(value).padStart(2, '0')

const formatRoundTime = (value: string): string => {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return `${padNumber(date.getMonth() + 1)}-${padNumber(date.getDate())} ${padNumber(date.getHours())}:${padNumber(date.getMinutes())}`
}

// 答对/答错显示模型的回答（答错时要看到它答了几），报错才显示错误信息。
const cellDetail = (cell: StripModelCell): string => {
  if (cell.state === 'none') return ''
  const detail = cell.state === 'error' ? (cell.error || cell.answer) : (cell.answer || cell.error)
  return detail || '—'
}

const cellTitle = (item: DisplayRound): string => {
  if (!item.round) return t('admin.accounts.iqStrip.none')

  const lines = [formatRoundTime(item.round.at)]
  for (const cell of item.round.cells) {
    const name = shortModel(cell.model)
    if (cell.state === 'none') {
      lines.push(`${name}：${stateLabel('none')}`)
      continue
    }
    const seconds = t('admin.accounts.iqStrip.seconds', { n: (cell.latencyMs / 1000).toFixed(1) })
    lines.push(`${name}：${cellDetail(cell)}（${seconds}）${stateLabel(cell.state)}`)
  }
  return lines.filter(Boolean).join('\n')
}

const legend = computed(() => {
  if (!props.strip) return ''
  const names = props.strip.models.map(shortModel)
  if (names.length === 2) {
    return t('admin.accounts.iqStrip.legend', { top: names[0], bottom: names[1] })
  }
  if (names.length === 1) return names[0]
  return names.join(' / ')
})

const ariaLabel = computed(() => {
  if (!props.strip) return t('admin.accounts.iqStrip.noPlan')
  const lastRound = props.strip.rounds[props.strip.rounds.length - 1]
  const statuses = lastRound
    ? lastRound.cells.map(cell => `${shortModel(cell.model)} ${stateLabel(cell.state)}`).join('，')
    : t('admin.accounts.iqStrip.none')
  return `${props.strip.ruleName} ${statuses}`
})
</script>
