<template>
  <div class="detail-body">
    <div class="filter-row iq-accounts-filters">
      <label>
        <span>时间窗口</span>
        <select :value="window" @change="changeWindow(($event.target as HTMLSelectElement).value as IQWindow)">
          <option value="24h">近 24 小时</option>
          <option value="7d">近 7 天</option>
          <option value="30d">近 30 天</option>
          <option value="all">全部历史</option>
        </select>
      </label>
      <label>
        <span>分组</span>
        <select :value="groupId" @change="changeGroup(Number(($event.target as HTMLSelectElement).value))">
          <option :value="0">全部账号</option>
          <option v-for="g in groupOptions" :key="g.id" :value="g.id">{{ g.name }}（#{{ g.id }}）</option>
        </select>
      </label>
      <label>
        <span>排序</span>
        <select v-model="sortKey">
          <option value="accuracy-asc">准确率从低到高</option>
          <option value="accuracy-desc">准确率从高到低</option>
          <option value="samples">样本数</option>
          <option value="recent">最近检测</option>
        </select>
      </label>
      <label class="search-field"><span>搜索</span><input v-model="search" placeholder="账号 ID、名称、分组" /></label>
      <label class="checkbox-field"><input v-model="hideInsufficient" type="checkbox" /><span>隐藏样本不足</span></label>
    </div>
    <div class="table-meta">
      <span>
        <template v-if="groupId">分组「{{ groupLabel }}」：只算检测时属于这个分组的记录 · </template>
        {{ view?.tested ?? 0 }} 个号测过 · 当前可用 {{ view?.available ?? 0 }} 个 ·
        整体 {{ formatAccuracy(view?.overall.accuracy_percent) }}（{{ view?.overall.correct ?? 0 }}/{{ view?.overall.graded ?? 0 }}）·
        样本少于 {{ view?.min_samples ?? thresholds.min_samples }} 次标「样本不足」，不参与高低排名
      </span>
      <span v-if="loading" class="spinner small"></span>
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>账号</th><th>所在分组</th><th>准确率</th><th>答对 / 判分</th><th>近 10 次</th><th>出错</th><th>换模型</th><th>JSON</th>
            <th>平均耗时</th><th>最近答案</th><th>分模型</th><th>最近检测</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in visible" :key="row.account_id" :class="{ 'row-expired': rowTone(row) === 'danger', 'row-warning': rowTone(row) === 'warning' }">
            <td>
              <div class="account-name" :title="row.account_name">{{ row.account_name }}</div>
              <div class="account-meta">
                #{{ row.account_id }}
                <span v-if="row.account_type === 'apikey'" class="mini-badge warning">API Key 号</span>
                <span v-if="!row.available" class="mini-badge warning">当前不可用</span>
              </div>
            </td>
            <td>
              <div class="iq-group-chips">
                <button v-for="g in row.groups ?? []" :key="g.id" type="button" class="iq-group-chip" :class="{ sampled: g.id === groupId }"
                  :title="`只看分组 ${g.name}`" @click="changeGroup(g.id)">{{ g.name }}</button>
                <span v-if="!(row.groups ?? []).length" class="muted">-</span>
              </div>
            </td>
            <td>
              <strong :class="accuracyClass(row)">{{ formatAccuracy(row.accuracy_percent) }}</strong>
              <div v-if="row.insufficient" class="account-meta"><span class="mini-badge muted">样本不足</span></div>
            </td>
            <td>{{ row.correct }} / {{ row.graded }}</td>
            <td><IQHistoryStrip compact :items="(row.recent ?? []).map((o) => ({ outcome: o }))" :slots="10" /></td>
            <td>{{ row.errors || '-' }}</td>
            <td><span :class="{ 'danger-text': row.swapped > 0 }">{{ row.swapped || '-' }}</span></td>
            <td>{{ formatAccuracy(row.json_percent) }}</td>
            <td>{{ formatMs(row.avg_duration_ms) }}</td>
            <td>
              <strong>{{ row.last_answer || '-' }}</strong>
              <div class="account-meta" v-if="row.last_upstream_model">{{ row.last_upstream_model }}</div>
            </td>
            <td>
              <div v-for="m in row.models ?? []" :key="`${m.model}@${m.reasoning_effort}`" class="account-meta">
                {{ shortModel(m.model) }}{{ m.reasoning_effort ? `@${m.reasoning_effort}` : '' }} {{ formatAccuracy(m.accuracy_percent) }}（{{ m.correct }}/{{ m.graded }}）
              </div>
            </td>
            <td>{{ formatRelativeTime(row.last_at, now) }}</td>
            <td>
              <div class="row-actions">
                <button class="text-button" type="button" @click="emit('open-probes', row)">采样详情</button>
                <button class="text-button" type="button" :disabled="!canRun || !row.available" @click="emit('run', row)">立即检测</button>
              </div>
            </td>
          </tr>
          <tr v-if="visible.length === 0">
            <td colspan="13" class="empty-state">{{ view ? '窗口内还没有检测记录' : '加载中…' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import type { IQAccountRow, IQAccountsView, IQGroupRef, IQThresholds, IQWindow } from '../types'
import { formatAccuracy, formatMs, formatRelativeTime } from '../utils'
import IQHistoryStrip from './IQHistoryStrip.vue'

const props = defineProps<{
  now: number
  refreshKey: string | undefined
  canRun: boolean
  thresholds: IQThresholds
  groupOptions: IQGroupRef[]
  initialGroupId: number
}>()
const emit = defineEmits<{ run: [row: IQAccountRow]; 'open-probes': [row: IQAccountRow]; error: [error: unknown] }>()

const window = ref<IQWindow>('7d')
const groupId = ref(props.initialGroupId)
const sortKey = ref<'accuracy-asc' | 'accuracy-desc' | 'samples' | 'recent'>('accuracy-asc')
const search = ref('')
const hideInsufficient = ref(false)
const view = ref<IQAccountsView | null>(null)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    view.value = await api.iqAccounts(window.value, groupId.value)
  } catch (error) {
    emit('error', error)
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(() => props.refreshKey, () => void load())
watch(() => props.initialGroupId, (value) => { changeGroup(value) })

function changeWindow(value: IQWindow) {
  window.value = value
  void load()
}

function changeGroup(value: number) {
  if (groupId.value === value) return
  groupId.value = value
  void load()
}

const groupLabel = computed(() => props.groupOptions.find((g) => g.id === groupId.value)?.name ?? `#${groupId.value}`)

const visible = computed(() => {
  const needle = search.value.trim().toLowerCase()
  const rows = (view.value?.accounts ?? []).filter((row) => {
    if (hideInsufficient.value && row.insufficient) return false
    const groups = (row.groups ?? []).map((g) => g.name).join(' ')
    return !needle || `${row.account_id} ${row.account_name} ${groups}`.toLowerCase().includes(needle)
  })
  const accuracy = (row: IQAccountRow) => row.accuracy_percent ?? -1
  const sorted = [...rows]
  switch (sortKey.value) {
    case 'accuracy-desc':
      sorted.sort((a, b) => Number(a.insufficient) - Number(b.insufficient) || accuracy(b) - accuracy(a) || b.graded - a.graded)
      break
    case 'samples':
      sorted.sort((a, b) => b.graded - a.graded || a.account_id - b.account_id)
      break
    case 'recent':
      sorted.sort((a, b) => (b.last_at ?? '').localeCompare(a.last_at ?? ''))
      break
    default:
      // 后端已按「样本足够在前、准确率升序」排好
      break
  }
  return sorted
})

function rowTone(row: IQAccountRow): 'danger' | 'warning' | '' {
  if (row.insufficient || row.accuracy_percent === null) return ''
  if (row.accuracy_percent < props.thresholds.warning_percent) return 'danger'
  if (row.accuracy_percent < props.thresholds.healthy_percent) return 'warning'
  return ''
}

function accuracyClass(row: IQAccountRow): string {
  const tone = rowTone(row)
  if (tone === 'danger') return 'danger-text'
  if (tone === 'warning') return 'warning-text'
  return row.insufficient ? 'muted' : 'success-text'
}

function shortModel(m: string): string { return m.replace(/^gpt-/, '') }

defineExpose({ load })
</script>
