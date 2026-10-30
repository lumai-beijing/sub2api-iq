<template>
  <section class="panel iq-board" aria-labelledby="iq-board-title">
    <header class="iq-board-head">
      <div>
        <h3 id="iq-board-title">各来源答题情况</h3>
        <p class="iq-board-sub">{{ subtitle }}</p>
      </div>
      <div class="iq-board-controls">
        <div class="iq-segment" role="radiogroup" aria-label="统计口径">
          <button
            v-for="item in windows"
            :key="item.key"
            type="button"
            role="radio"
            :aria-checked="window === item.key"
            :class="{ active: window === item.key }"
            @click="emit('update:window', item.key)"
          >{{ item.label }}</button>
        </div>
        <select class="iq-board-scope" :value="scope" aria-label="题面口径" @change="emit('update:scope', ($event.target as HTMLSelectElement).value as IQScope)">
          <option value="all">全部题面</option>
          <option value="random">只看随机题面</option>
          <option value="fixed">只看固定题面（改版前）</option>
        </select>
        <span v-if="loading" class="spinner small" aria-label="加载中"></span>
      </div>
    </header>

    <div class="table-wrap">
      <table class="iq-board-table">
        <thead>
          <tr>
            <th>来源</th>
            <th v-for="column in columns" :key="column.label">
              <span class="iq-board-model">{{ column.model }}</span>
              <span class="iq-board-effort">推理 {{ column.reasoning_effort || '默认' }}</span>
            </th>
            <th>全部模型</th>
            <th>检测</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="section in sections" :key="section.key">
            <tr v-if="section.title" class="iq-board-section">
              <td :colspan="columns.length + 3">
                <span>{{ section.title }}</span>
                <small>{{ section.note }}</small>
              </td>
            </tr>
            <tr v-for="row in section.rows" :key="row.key" :class="rowClass(row)" :data-key="row.key">
              <td class="iq-board-source">
                <div class="iq-board-source-name">
                  <strong :title="row.name">{{ row.name }}</strong>
                  <span class="iq-level-badge small" :class="`level-${row.level}`"><i aria-hidden="true"></i>{{ row.level_label }}</span>
                </div>
                <div class="account-meta" :title="row.detail">{{ row.detail }}</div>
              </td>
              <td v-for="(cell, i) in row.cells ?? []" :key="i" class="iq-board-td" :class="cellClass(cell)">
                <button
                  v-if="hasData(cell) || cell.configured"
                  type="button"
                  class="iq-board-cell"
                  :class="{ active: selected === `${row.key}|${columns[i].label}` }"
                  :title="cellTitle(cell)"
                  :aria-label="`${row.name} ${columns[i].label}：${cellTitle(cell)}`"
                  @click="emit('select', row, columns[i])"
                >
                  <span class="iq-board-acc">{{ hasData(cell) ? formatAccuracy(cell.stats.accuracy_percent) : '还没有数据' }}</span>
                  <span v-if="hasData(cell)" class="iq-board-count">
                    {{ cell.stats.correct }}/{{ cell.stats.graded }}<template v-if="cell.stats.errors"> · 出错 {{ cell.stats.errors }}</template>
                  </span>
                  <IQHistoryStrip v-if="(cell.strip ?? []).length" mini :items="stripItems(cell)" :slots="30" />
                  <span v-if="cell.best || cell.insufficient || cell.gap_vs_pool !== null || (!cell.configured && hasData(cell))" class="iq-board-badges">
                    <span v-if="cell.best" class="mini-badge success">最高</span>
                    <span v-if="cell.insufficient && hasData(cell)" class="mini-badge muted">样本不足</span>
                    <span v-if="cell.gap_vs_pool !== null" class="mini-badge" :class="gapClass(cell.gap_vs_pool)">比号池 {{ formatGap(cell.gap_vs_pool) }}</span>
                    <span v-if="!cell.configured" class="mini-badge muted">已停测</span>
                  </span>
                </button>
                <span v-else class="iq-board-empty" title="这个来源没有在测这个模型">—</span>
              </td>
              <td class="iq-board-total" :class="cellClass(row.total)">
                <strong>{{ formatAccuracy(row.total.stats.accuracy_percent) }}</strong>
                <div class="account-meta">{{ row.total.stats.correct }}/{{ row.total.stats.graded }}</div>
              </td>
              <td class="iq-board-status">
                <div :class="{ 'warning-text': !row.enabled }">{{ statusText(row) }}</div>
                <div v-if="row.last_run_at" class="account-meta">上次 {{ formatRelativeTime(row.last_run_at, now) }}</div>
                <button class="text-button" type="button" :disabled="!canRun(row)" @click="emit('run', row)">立即检测</button>
              </td>
            </tr>
            <tr v-if="section.empty" class="iq-board-hint">
              <td :colspan="columns.length + 3">
                {{ section.empty }}
                <button class="text-button" type="button" @click="emit('goto', section.key === 'group' ? 'groups' : 'providers')">{{ section.key === 'group' ? '去选分组' : '去添加' }}</button>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>

    <footer class="iq-board-legend">
      <span><i class="iq-pip outcome-correct"></i>答对</span>
      <span><i class="iq-pip outcome-wrong"></i>答错</span>
      <span><i class="iq-pip outcome-no_answer"></i>没给答案</span>
      <span><i class="iq-pip outcome-error"></i>出错（不计入答对率）</span>
      <span><i class="iq-pip outcome-correct variant-fixed"></i>浅色 = 固定题面（改版前）</span>
      <span class="iq-board-legend-note">点格子看每次的答案；≥{{ thresholds.healthy_percent }}% 正常，&lt;{{ thresholds.warning_percent }}% 降智，少于 {{ thresholds.min_samples }} 次不判级。</span>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { IQBoard, IQBoardCell, IQBoardColumn, IQBoardRow, IQBoardWindow, IQScope, IQThresholds } from '../types'
import { formatAccuracy, formatDateTime, formatRelativeTime, formatTimeUntil, iqOutcomeLabel } from '../utils'
import IQHistoryStrip, { type StripItem } from './IQHistoryStrip.vue'

const props = defineProps<{
  board: IQBoard | null
  loading: boolean
  window: IQBoardWindow
  scope: IQScope
  thresholds: IQThresholds
  now: number
  selected: string
  poolCanRun: boolean
}>()
const emit = defineEmits<{
  'update:window': [window: IQBoardWindow]
  'update:scope': [scope: IQScope]
  select: [row: IQBoardRow, column: IQBoardColumn]
  run: [row: IQBoardRow]
  goto: [section: 'groups' | 'providers']
}>()

const windows = computed<{ key: IQBoardWindow; label: string }[]>(() => [
  { key: 'recent', label: `最近 ${props.thresholds.recent_window} 次` },
  { key: '24h', label: '24 小时' },
  { key: '7d', label: '7 天' },
  { key: '30d', label: '30 天' },
  { key: 'all', label: '全部' }
])

const columns = computed(() => props.board?.columns ?? [])
const rows = computed(() => props.board?.rows ?? [])

const subtitle = computed(() => {
  const scope = { all: '', random: ' · 只看随机题面', fixed: ' · 只看固定题面' }[props.scope]
  if (props.window === 'recent') {
    return `每格是最近 ${props.thresholds.recent_window} 次可判分的结果（当前状态，判级用）${scope}`
  }
  const label = windows.value.find((w) => w.key === props.window)?.label ?? ''
  return `每格是${props.window === 'all' ? '全部历史' : `近 ${label}`}的合计，适合长期横向比较${scope}`
})

const sections = computed(() => {
  const pool = rows.value.filter((r) => r.kind === 'pool')
  const groups = rows.value.filter((r) => r.kind === 'group')
  const providers = rows.value.filter((r) => r.kind === 'provider')
  return [
    { key: 'pool', title: '', note: '', rows: pool, empty: '' },
    {
      key: 'group', title: '分组', note: '检测时属于该分组的号；跟号池同一轮测', rows: groups,
      empty: groups.length ? '' : '还没选要对比的分组。'
    },
    {
      key: 'provider', title: '第三方供应商', note: '各自独立定时检测', rows: providers,
      empty: providers.length ? '' : '还没有第三方供应商。'
    }
  ]
})

function hasData(cell: IQBoardCell): boolean {
  return cell.stats.graded + cell.stats.errors > 0
}

function cellClass(cell: IQBoardCell): string {
  if (!hasData(cell) || cell.insufficient || cell.stats.accuracy_percent === null) return 'tone-muted'
  if (cell.level === 'danger') return 'tone-danger'
  if (cell.level === 'warning') return 'tone-warning'
  if (cell.best) return 'tone-best'
  return 'tone-ok'
}

function rowClass(row: IQBoardRow): string {
  if (row.level === 'danger') return 'row-expired'
  if (row.level === 'warning') return 'row-warning'
  return ''
}

function cellTitle(cell: IQBoardCell): string {
  if (!hasData(cell)) return '还没有数据'
  const s = cell.stats
  const scope = props.window === 'recent' ? `最近 ${s.graded} 次可判分` : '窗口内'
  return `${scope}：答对 ${s.correct} · 答错 ${s.wrong}${s.no_answer ? ` · 没给答案 ${s.no_answer}` : ''}` +
    (s.errors ? ` · 出错 ${s.errors}（不计入）` : '') + (s.swapped ? ` · 换模型 ${s.swapped}` : '')
}

function stripItems(cell: IQBoardCell): StripItem[] {
  return (cell.strip ?? []).map((p) => ({
    outcome: p.outcome,
    variant: p.variant,
    title: `${formatDateTime(p.at)}${p.who ? ` · ${p.who}` : ''} · ${iqOutcomeLabel(p.outcome)}${p.answer ? ` · 答 ${p.answer}` : ''}` +
      (p.variant === 'fixed' ? '（固定题面）' : '')
  }))
}

function formatGap(gap: number): string {
  const rounded = Math.round(gap)
  return rounded > 0 ? `+${rounded}` : rounded < 0 ? `−${Math.abs(rounded)}` : '持平'
}

function gapClass(gap: number): string {
  if (gap >= 15) return 'success'
  if (gap <= -15) return 'danger'
  return 'muted'
}

function statusText(row: IQBoardRow): string {
  if (row.running) return '检测中…'
  if (!row.enabled) return row.kind === 'provider' ? '定时已关' : '定时已关闭'
  if (!row.next_run_at) return row.kind === 'group' ? '跟号池一起测' : '等待排期'
  const next = `下一轮 ${formatTimeUntil(row.next_run_at, props.now)}`
  return row.kind === 'group' ? `跟号池一起 · ${next}` : next
}

function canRun(row: IQBoardRow): boolean {
  if (row.kind === 'provider') return !row.running
  return props.poolCanRun
}
</script>
