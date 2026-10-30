<template>
  <div class="iq-view">
    <section v-if="error" class="error-banner" role="alert">
      <strong>读取失败</strong><span>{{ error }}</span>
    </section>
    <section v-if="notice" class="error-banner soft" role="status">
      <strong>{{ notice.title }}</strong><span>{{ notice.text }}</span>
    </section>

    <!-- 总览：状态 + 结论 -->
    <section class="overview">
      <header class="overview-head">
        <div>
          <p class="eyebrow">{{ questionLine }}</p>
          <h2>智商检测</h2>
        </div>
        <div class="state-chips">
          <span class="chip" :class="levelChip(overview?.level)" title="号池整体与选中分组里最差的判级（自己的号）">号池 · {{ overview?.level_label ?? '加载中' }}</span>
          <span class="chip" :class="schedule.running ? 'chip-ok' : schedule.enabled ? 'chip-mute' : 'chip-warn'">
            {{ schedule.running ? `检测中（${schedule.running_targets} 个号）` : schedule.enabled ? `下一轮 ${formatTimeUntil(schedule.next_run_at, nowTick)}` : '定时已关闭' }}
          </span>
          <span class="chip chip-mute">{{ roundSummary }}</span>
          <span class="chip chip-mute">上次 {{ formatRelativeTime(schedule.last_run_at, nowTick) }}</span>
          <span class="chip" :class="overview?.gate.open ? 'chip-mute' : 'chip-crit'">sub2api {{ overview?.gate.current || '—' }}</span>
        </div>
      </header>

      <section v-if="advisories.length" class="panel iq-advisories" aria-label="结论">
        <div class="advisory-strip">
          <button v-for="(item, index) in visibleAdvisories" :key="index" type="button" class="advisory-row" :class="[`tone-${item.level}`, { active: expanded === item }]" @click="openAdvisory(item)">
            <span class="advisory-dot" aria-hidden="true"></span>
            <span class="advisory-badge">{{ advisoryLevelLabel(item.level) }}</span>
            <span class="advisory-title">
              <span v-if="item.source" class="iq-advisory-source">{{ item.source }}</span>
              <span v-if="item.model" class="advisory-model">{{ item.model }}</span>
              {{ item.title }}
            </span>
            <span class="advisory-evidence" :title="item.evidence">{{ item.evidence }}</span>
            <span class="advisory-go" aria-hidden="true">›</span>
          </button>
        </div>
        <p v-if="expanded && (expanded.detail || expanded.evidence)" class="advisory-detail">
          <template v-if="expanded.detail">{{ expanded.detail }}</template>
          <span v-if="expanded.evidence" class="iq-advisory-evidence">{{ expanded.evidence }}</span>
        </p>
        <button v-if="advisories.length > advisoryLimit" class="text-button iq-advisories-more" type="button" @click="showAllAdvisories = !showAllAdvisories">
          {{ showAllAdvisories ? '收起' : `展开全部 ${advisories.length} 条` }}
        </button>
      </section>
    </section>

    <!-- 总览矩阵 -->
    <IQBoard
      :board="board"
      :loading="boardLoading"
      :window="boardWindow"
      :scope="boardScope"
      :thresholds="thresholds"
      :now="nowTick"
      :selected="selectedKey"
      :pool-can-run="canRunPool"
      @update:window="changeBoard($event, boardScope)"
      @update:scope="changeBoard(boardWindow, $event)"
      @select="selectCell"
      @run="runRow"
      @goto="selectSection"
    />

    <IQCellDetail
      v-if="selected"
      ref="detail"
      :source-key="selected.sourceKey"
      :source-name="selected.sourceName"
      :kind="selected.kind"
      :model="selected.model"
      :scope="boardScope"
      :refresh-key="overview?.generated_at"
      :can-run="selected.kind === 'provider' || canRunPool"
      :group-names="groupNames"
      @run="runSelected"
      @close="selected = null"
      @open-probes="openProbes({ source: selected.sourceKey, model: selected.model, title: `${selected.sourceName} · ${selected.model}` })"
      @error="handleError"
    />

    <!-- 管理与明细 -->
    <section class="panel detail">
      <nav class="section-nav" aria-label="维度切换">
        <button v-for="item in sections" :key="item.key" type="button" class="section-tab" :class="{ active: section === item.key }" :aria-current="section === item.key ? 'page' : undefined" @click="selectSection(item.key)">
          {{ item.label }}<span v-if="item.count" class="iq-tab-count">{{ item.count }}</span>
        </button>
      </nav>

      <IQGroupPanel
        v-if="section === 'groups'"
        :now="nowTick"
        :refresh-key="overview?.generated_at"
        :can-run="canRunPool"
        :thresholds="thresholds"
        @run="(g) => run({ group_id: g.id }, `已开始检测分组 ${g.name}`)"
        @open-accounts="(g) => openAccounts(g.id)"
        @open-probes="(g) => openProbes({ source: `group:${g.id}`, title: `分组 ${g.name}` })"
        @loaded="rememberGroups"
        @saved="emit('refresh')"
        @flash="flash"
        @error="handleError"
      />

      <IQProviderPanel
        v-else-if="section === 'providers'"
        :now="nowTick"
        :refresh-key="overview?.generated_at"
        @open-probes="openProbes"
        @select-cell="(source, name, model) => selectCellByKey(source, name, 'provider', model)"
        @changed="emit('refresh')"
        @flash="flash"
        @error="handleError"
      />

      <IQAccountsPanel
        v-else-if="section === 'accounts'"
        :now="nowTick"
        :refresh-key="overview?.generated_at"
        :can-run="canRunPool"
        :thresholds="thresholds"
        :group-options="groupOptions"
        :initial-group-id="accountsGroupId"
        @run="(row) => run({ account_id: row.account_id }, `已开始检测 #${row.account_id}`)"
        @open-probes="(row) => openProbes({ source: 'accounts', accountId: row.account_id, title: `#${row.account_id} ${row.account_name}` })"
        @error="handleError"
      />

      <IQProbesPanel
        v-else-if="section === 'probes'"
        :filter="probeFilter"
        :source-options="sourceOptions"
        :group-names="groupNames"
        :refresh-key="overview?.generated_at"
        @update:filter="probeFilter = $event"
        @error="handleError"
      />

      <!-- 设置 -->
      <div v-else class="detail-body">
        <IQScheduleSettings :schedule="schedule" :saving="saving" :now="nowTick" @save="saveSchedule" />

        <p class="section-hint">
          测试模型写成 <code>模型@推理强度</code>，多个用逗号分隔，例如 <code>gpt-6-astra@xhigh, gpt-5.6-sol@max</code>。
          推理强度可选 none / low / medium / high / xhigh / max，不写表示用模型默认。号池和分组共用这份清单，每轮给每个抽中的号随机分配一个。
        </p>
        <div class="iq-settings">
          <label class="search-field">
            <span>测试模型（当前{{ overview?.models_source === 'settings' ? '已自定义' : '为内置默认' }}）</span>
            <input v-model="modelsDraft" placeholder="gpt-6-astra@xhigh, gpt-5.6-sol@max" />
          </label>
          <div class="iq-settings-actions">
            <button class="primary-button" type="button" :disabled="saving || !modelsDirty" @click="saveModels">保存测试模型</button>
            <button class="secondary-button" type="button" :disabled="!canRunPool" @click="run({}, '已开始一轮检测')">立即跑一轮</button>
          </div>
        </div>
        <div class="stat-row">
          <div class="stat"><span>并发</span><strong>{{ schedule.concurrency }}</strong><small>同一时刻最多占用几个号</small></div>
          <div class="stat" :class="overview?.gate.open ? 'tone-success' : 'tone-danger'">
            <span>sub2api 版本</span><strong>{{ overview?.gate.current || '—' }}</strong>
            <small>要求 ≥ {{ overview?.gate.required }}{{ overview?.gate.error ? `；${overview.gate.error}` : '' }}</small>
          </div>
          <div class="stat"><span>判级阈值</span><strong>{{ thresholds.healthy_percent }}% / {{ thresholds.warning_percent }}%</strong><small>≥ 前者正常，低于后者算降智；最近 {{ thresholds.recent_window }} 次、至少 {{ thresholds.min_samples }} 次才判级</small></div>
        </div>
        <section class="iq-question">
          <h4>题目</h4>
          <p class="iq-field-hint">
            糖果题，标准答案 {{ question.answer }}。<template v-if="question.randomized">
            每次发题都随机改写叙述方式（约 {{ spaceLabel }} 种组合）：换措辞和同义词、数量的写法（行内、分条、表格、句子）、口味的顺序、
            阿拉伯数字或汉字；所有条件、数字、问法和答案都不变，也不加任何提示。这样上游没法靠缓存同一道题的答案拿分。</template>
          </p>
          <details>
            <summary>原题</summary>
            <pre>{{ question.prompt }}</pre>
          </details>
          <details v-for="(sample, index) in question.samples ?? []" :key="index">
            <summary>随机题面示例 {{ index + 1 }}</summary>
            <pre>{{ sample }}</pre>
          </details>
        </section>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, APIError } from '../api'
import type {
  IQAdvisory, IQBoard as IQBoardData, IQBoardColumn, IQBoardRow, IQBoardWindow, IQGroupRef, IQGroupsView, IQLevel, IQOverview,
  IQQuestion, IQSchedule, IQScheduleConfig, IQScope, IQSourceKind, IQThresholds
} from '../types'
import { advisoryLevelLabel, formatRelativeTime, formatTimeUntil } from '../utils'
import IQAccountsPanel from './IQAccountsPanel.vue'
import IQBoard from './IQBoard.vue'
import IQCellDetail from './IQCellDetail.vue'
import IQGroupPanel from './IQGroupPanel.vue'
import IQProbesPanel, { type ProbeFilter } from './IQProbesPanel.vue'
import IQProviderPanel from './IQProviderPanel.vue'
import IQScheduleSettings from './IQScheduleSettings.vue'

type SectionKey = 'groups' | 'providers' | 'accounts' | 'probes' | 'settings'

const props = defineProps<{
  overview: IQOverview | null
  error: string
}>()
const emit = defineEmits<{ refresh: []; unauthorized: [] }>()

const section = ref<SectionKey>('groups')
const boardWindow = ref<IQBoardWindow>('recent')
const boardScope = ref<IQScope>('all')
const boardOverride = ref<IQBoardData | null>(null)
const boardLoading = ref(false)
const selected = ref<{ sourceKey: string; sourceName: string; kind: IQSourceKind; model: string } | null>(null)
const detail = ref<InstanceType<typeof IQCellDetail> | null>(null)
const probeFilter = ref<ProbeFilter>({ source: 'pool', outcome: '', scope: 'all' })
const accountsGroupId = ref(0)
const knownGroups = ref<IQGroupRef[]>([])
const modelsDraft = ref('')
const saving = ref(false)
const notice = ref<{ title: string; text: string } | null>(null)
const expanded = ref<IQAdvisory | null>(null)
const showAllAdvisories = ref(false)
const nowTick = ref(Date.now())
const advisoryLimit = 6
let clockTimer: ReturnType<typeof setInterval> | null = null
let noticeTimer: ReturnType<typeof setTimeout> | null = null

onMounted(() => {
  clockTimer = setInterval(() => { nowTick.value = Date.now() }, 1000)
})
onBeforeUnmount(() => {
  if (clockTimer) clearInterval(clockTimer)
  if (noticeTimer) clearTimeout(noticeTimer)
})

const schedule = computed<IQSchedule>(() => props.overview?.schedule ?? {
  enabled: true, interval_seconds: 300, jitter_seconds: 60, sample_mode: 'ratio', sample_percent: 10, sample_min: 2, sample_count: 5,
  config_source: 'default', paused: false, concurrency: 2, running: false, running_targets: 0,
  last_run_at: null, last_finished_at: null, last_round_seconds: null, next_run_at: null, next_anchor_at: null,
  skipped_slots: 0, candidates: 0, sample_size: 0, groups: 0, group_per_round: 2, last_round_pool: 0, last_round_top_ups: 0
})
const thresholds = computed<IQThresholds>(() => props.overview?.thresholds ?? { recent_window: 30, min_samples: 5, healthy_percent: 80, warning_percent: 50, history_points: 180 })
const question = computed<IQQuestion>(() => props.overview?.question ?? { id: '', prompt: '', answer: 21, randomized: false, space: 0, samples: [] })
const canRunPool = computed(() => Boolean(props.overview?.gate.open) && !schedule.value.running)

// 矩阵：默认口径直接用总览带回来的；切换口径后单独拉。
const board = computed<IQBoardData | null>(() => boardOverride.value ?? props.overview?.board ?? null)
const advisories = computed(() => props.overview?.board.advisories ?? [])
const visibleAdvisories = computed(() => showAllAdvisories.value ? advisories.value : advisories.value.slice(0, advisoryLimit))
const selectedKey = computed(() => selected.value ? `${selected.value.sourceKey}|${selected.value.model}` : '')

const intervalLabel = computed(() => secondsLabel(schedule.value.interval_seconds))
const questionLine = computed(() => `糖果题 · 标准答案 ${question.value.answer}${question.value.randomized ? ' · 题面每次随机改写' : ''}`)
const roundSummary = computed(() => {
  const s = schedule.value
  const sample = s.sample_mode === 'count' ? `${s.sample_count} 个号` : `${s.sample_percent}% 可用号（至少 ${s.sample_min}）`
  const groups = s.groups ? ` + ${s.groups} 个分组各补到 ${s.group_per_round} 个` : ''
  return `每 ${intervalLabel.value} ±${secondsLabel(s.jitter_seconds)} · 抽 ${sample}${groups}`
})
const spaceLabel = computed(() => {
  const space = question.value.space
  if (space >= 1e12) return `${(space / 1e12).toFixed(1)} 万亿`
  if (space >= 1e8) return `${(space / 1e8).toFixed(1)} 亿`
  return space.toLocaleString('en-US')
})

// 分组名：分组面板读到的全量 + 矩阵里正在测的
const groupNames = computed<Record<number, string>>(() => {
  const names: Record<number, string> = {}
  for (const g of knownGroups.value) names[g.id] = g.name
  for (const row of board.value?.rows ?? []) if (row.kind === 'group') names[row.id] = row.name
  return names
})
const groupOptions = computed<IQGroupRef[]>(() => {
  const selectedIds = props.overview?.groups.ids ?? []
  const all = Object.entries(groupNames.value).map(([id, name]) => ({ id: Number(id), name }))
  return all.sort((a, b) => Number(selectedIds.includes(b.id)) - Number(selectedIds.includes(a.id)) || a.id - b.id)
})
const sourceOptions = computed(() => {
  const options = [{ key: 'pool', label: '号池整体（按比例抽样的 OAuth 号）' }, { key: 'accounts', label: 'sub2api 全部号（含分组补测、手动）' }]
  for (const row of board.value?.rows ?? []) {
    if (row.kind === 'group') options.push({ key: row.key, label: `分组 · ${row.name}` })
  }
  for (const row of board.value?.rows ?? []) {
    if (row.kind === 'provider') options.push({ key: row.key, label: `第三方 · ${row.name}` })
  }
  if (!options.some((o) => o.key === probeFilter.value.source)) {
    options.push({ key: probeFilter.value.source, label: probeFilter.value.title || probeFilter.value.source })
  }
  return options
})

const sections = computed<{ key: SectionKey; label: string; count?: number }[]>(() => [
  { key: 'groups', label: '分组', count: props.overview?.groups.ids?.length || undefined },
  { key: 'providers', label: '第三方', count: (board.value?.rows ?? []).filter((r) => r.kind === 'provider').length || undefined },
  { key: 'accounts', label: '账号排行' },
  { key: 'probes', label: '采样详情' },
  { key: 'settings', label: '设置' }
])

// 设置里的草稿跟着服务端值走，除非用户正在编辑。
const serverModels = computed(() => (props.overview?.models ?? []).map((m) => m.label).join(', '))
const modelsDirty = computed(() => normalizeModels(modelsDraft.value) !== normalizeModels(serverModels.value))
watch(serverModels, (value, previous) => {
  if (!modelsDraft.value || normalizeModels(modelsDraft.value) === normalizeModels(previous ?? '')) modelsDraft.value = value
}, { immediate: true })
function normalizeModels(raw: string): string {
  return raw.split(',').map((s) => s.trim().toLowerCase()).filter(Boolean).join(',')
}

// 总览刷新时（5 分钟自动 / 手动），非默认口径的矩阵也跟着重拉。
watch(() => props.overview?.generated_at, () => {
  if (boardOverride.value) void loadBoard()
})

function selectSection(key: SectionKey) {
  section.value = key
}

async function changeBoard(window: IQBoardWindow, scope: IQScope) {
  boardWindow.value = window
  boardScope.value = scope
  if (window === 'recent' && scope === 'all') {
    boardOverride.value = null
    return
  }
  await loadBoard()
}

async function loadBoard() {
  boardLoading.value = true
  try {
    boardOverride.value = await api.iqBoard(boardWindow.value, boardScope.value)
  } catch (error) {
    handleError(error)
  } finally {
    boardLoading.value = false
  }
}

function selectCell(row: IQBoardRow, column: IQBoardColumn) {
  selectCellByKey(row.key, row.name, row.kind, column.label)
}

function selectCellByKey(sourceKey: string, sourceName: string, kind: IQSourceKind, model: string) {
  const name = kind === 'group' ? `分组 ${sourceName}` : sourceName
  if (selected.value?.sourceKey === sourceKey && selected.value.model === model) {
    selected.value = null
    return
  }
  selected.value = { sourceKey, sourceName: name, kind, model }
  void nextTick(() => {
    const el = (detail.value?.$el as HTMLElement | undefined)
    if (el && typeof el.scrollIntoView === 'function') el.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
  })
}

// 结论：点了有来源和模型的，直接打开那一格；只有来源的，跳到对应的管理页。
function openAdvisory(item: IQAdvisory) {
  expanded.value = expanded.value === item ? null : item
  const key = item.source_key
  if (!key) return
  const row = (board.value?.rows ?? []).find((r) => r.key === key)
  const model = item.focus_model || item.model
  if (model && row) {
    if (selectedKey.value !== `${key}|${model}`) selectCellByKey(row.key, row.name, row.kind, model)
    return
  }
  if (key.startsWith('group:')) selectSection('groups')
  else if (key.startsWith('provider:')) selectSection('providers')
}

function openProbes(scope: { source: string; accountId?: number; model?: string; title?: string }) {
  probeFilter.value = { source: scope.source, accountId: scope.accountId, model: scope.model, title: scope.title, outcome: '', scope: boardScope.value }
  section.value = 'probes'
}

function openAccounts(groupId: number) {
  accountsGroupId.value = groupId
  section.value = 'accounts'
}

function rememberGroups(view: IQGroupsView) {
  knownGroups.value = (view.groups ?? []).map((g) => ({ id: g.id, name: g.name }))
}

function levelChip(level: IQLevel | undefined): string {
  return ({ healthy: 'chip-ok', warning: 'chip-warn', danger: 'chip-crit', blocked: 'chip-crit', paused: 'chip-warn' } as Record<string, string>)[level ?? ''] ?? 'chip-mute'
}

function secondsLabel(seconds: number): string {
  if (seconds >= 3600 && seconds % 3600 === 0) return `${seconds / 3600} 小时`
  if (seconds >= 60 && seconds % 60 === 0) return `${seconds / 60} 分钟`
  return `${seconds} 秒`
}

// ---------- 操作 ----------

function runRow(row: IQBoardRow) {
  if (row.kind === 'provider') void runProvider(row.id, row.name)
  else if (row.kind === 'group') void run({ group_id: row.id }, `已开始检测分组 ${row.name}`)
  else void run({}, '已开始一轮检测')
}

function runSelected() {
  const cell = selected.value
  if (!cell) return
  const [kind, rawID] = cell.sourceKey.split(':')
  if (kind === 'provider') void runProvider(Number(rawID), cell.sourceName, cell.model)
  else if (kind === 'group') void run({ group_id: Number(rawID), model: cell.model }, `已开始检测${cell.sourceName}`)
  else void run({}, '已开始一轮检测')
}

async function run(body: { account_id?: number; group_id?: number; model?: string }, message: string) {
  try {
    const response = await api.iqRun(body)
    flash('检测已受理', `${message}（${response.targets} 个号）。推理较慢，结果会在 1～3 分钟内出现。`)
    refreshSoon()
  } catch (error) {
    handleError(error)
  }
}

async function runProvider(id: number, name: string, model?: string) {
  try {
    const response = await api.iqRunProvider(id, model)
    flash('检测已受理', `${name}：${response.targets} 题已开始，推理较慢，结果会在 1～3 分钟内出现。`)
    refreshSoon()
  } catch (error) {
    handleError(error)
  }
}

// 测试在后台跑：马上刷一次看到「检测中」，过一会儿再刷新拿结果。
function refreshSoon() {
  emit('refresh')
  setTimeout(() => emit('refresh'), 45_000)
  setTimeout(() => emit('refresh'), 120_000)
}

async function saveModels() {
  saving.value = true
  try {
    const view = await api.iqUpdateSettings({ models: modelsDraft.value })
    modelsDraft.value = (view.models ?? []).map((m) => m.label).join(', ')
    flash('已保存', '下一轮起按新的测试模型出题（号池和分组共用）。')
    emit('refresh')
  } catch (error) {
    handleError(error)
  } finally {
    saving.value = false
  }
}

async function saveSchedule(config: IQScheduleConfig) {
  saving.value = true
  try {
    await api.iqUpdateSettings({ schedule: config })
    flash('定时配置已保存', config.enabled ? '已按新配置重新排定下一轮。' : '定时检测已关闭，手动检测仍可用。')
    emit('refresh')
  } catch (error) {
    handleError(error)
  } finally {
    saving.value = false
  }
}

function flash(title: string, text: string) {
  notice.value = { title, text }
  if (noticeTimer) clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => { notice.value = null }, 8000)
}

function handleError(error: unknown) {
  if (error instanceof APIError && error.status === 401) {
    emit('unauthorized')
    return
  }
  flash('操作失败', error instanceof Error ? error.message : String(error))
}
</script>
