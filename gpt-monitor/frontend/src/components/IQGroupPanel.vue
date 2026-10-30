<template>
  <div class="iq-groups">
    <div class="iq-groups-head">
      <p class="section-hint">
        勾选要横向对比的 sub2api 分组。分组的智商 = <strong>检测时属于这个分组的号</strong>的答题结果：号池每轮照常抽号，
        抽完后给每个勾选的分组补测到至少 {{ perRound || '-' }} 个号（组里的 API Key 号也会测到）。
        测的是号本身（经 sub2api 测试接口直连上游），不含网关的分组级设置（推理强度上限、模型映射）；
        要测用户经网关实际拿到的效果，可以把网关地址和该分组的 API Key 加成一个「第三方」。
      </p>
      <label class="iq-groups-window">
        <span>答对率窗口</span>
        <select :value="window" @change="changeWindow(($event.target as HTMLSelectElement).value as IQWindow)">
          <option value="24h">近 24 小时</option>
          <option value="7d">近 7 天</option>
          <option value="30d">近 30 天</option>
          <option value="all">全部历史</option>
        </select>
      </label>
    </div>
    <p v-if="view?.error" class="form-error iq-groups-error" role="alert">{{ view.error }}</p>

    <div class="table-meta">
      <span>
        共 {{ groups.length }} 个 OpenAI 分组 · 已勾选 {{ draftIds.length }} 个 ·
        号池整体同期 {{ formatAccuracy(view?.pool.accuracy_percent) }}（{{ view?.pool.correct ?? 0 }}/{{ view?.pool.graded ?? 0 }}），可作参照
      </span>
      <span v-if="loading" class="spinner small"></span>
    </div>
    <div class="table-wrap">
      <table class="iq-groups-table">
        <thead>
          <tr>
            <th>检测</th><th>分组</th><th>号</th><th>此刻可用</th><th>近 24h 请求</th><th>答对率</th><th>最近检测</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="g in groups" :key="g.id" :class="{ 'row-highlight': isDraft(g.id), 'row-expired': rowTone(g) === 'danger', 'row-warning': rowTone(g) === 'warning' }">
            <td>
              <input type="checkbox" class="iq-group-check" :checked="isDraft(g.id)" :disabled="!isDraft(g.id) && draftIds.length >= limits.max_groups"
                :aria-label="`检测分组 ${g.name}`" @change="toggle(g.id)" />
            </td>
            <td>
              <div class="account-name" :title="g.name">{{ g.name }}</div>
              <div class="account-meta">
                #{{ g.id }}
                <span v-if="g.missing" class="mini-badge danger">sub2api 里已删除</span>
                <span v-else-if="g.status && g.status !== 'active'" class="mini-badge warning">{{ g.status }}</span>
                <span v-if="g.selected" class="mini-badge success">检测中</span>
              </div>
            </td>
            <td>
              {{ g.members }}
              <div class="account-meta">OAuth {{ g.oauth }}<template v-if="g.apikey"> · API Key {{ g.apikey }}</template></div>
            </td>
            <td :class="{ 'warning-text': g.members > 0 && g.available === 0 }">{{ g.missing ? '-' : g.available }}</td>
            <td>{{ g.requests_24h < 0 ? '-' : g.requests_24h.toLocaleString('en-US') }}</td>
            <td>
              <strong :class="accuracyClass(g)">{{ formatAccuracy(g.stats.accuracy_percent) }}</strong>
              <div class="account-meta">
                {{ g.stats.correct }}/{{ g.stats.graded }}<template v-if="g.stats.errors"> · 出错 {{ g.stats.errors }}</template>
                <template v-if="g.insufficient && g.stats.graded"> · 样本不足</template>
              </div>
            </td>
            <td>{{ formatRelativeTime(g.last_at, now) }}</td>
            <td>
              <div class="row-actions">
                <button class="text-button" type="button" :disabled="!canRun || g.available === 0" @click="emit('run', g)">立即检测</button>
                <button class="text-button" type="button" :disabled="g.stats.graded + g.stats.errors === 0" @click="emit('open-accounts', g)">看账号</button>
                <button class="text-button" type="button" :disabled="g.stats.graded + g.stats.errors === 0" @click="emit('open-probes', g)">采样详情</button>
              </div>
            </td>
          </tr>
          <tr v-if="!groups.length">
            <td colspan="8" class="empty-state">{{ loading ? '加载中…' : '没有读到 OpenAI 分组' }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <footer class="iq-groups-foot">
      <label class="iq-inline-field">
        <span>每轮每个分组至少测</span>
        <span class="iq-input-unit"><input v-model.number="perRound" type="number" min="1" :max="limits.max_per_round" step="1" inputmode="numeric" /><em>个号</em></span>
      </label>
      <p class="iq-field-hint">
        已勾选 {{ draftIds.length }} 个分组：每轮在号池抽样之外最多再测 {{ draftIds.length * (perRound || 0) }} 个号（号池抽样已抽到的组内号不重复测），
        测试模型与号池相同。分组越多、每组越多，一轮越慢。
      </p>
      <p v-if="validationError" class="form-error" role="alert">{{ validationError }}</p>
      <div class="iq-settings-actions">
        <button class="ghost-button" type="button" :disabled="saving || !dirty" @click="reset">还原</button>
        <button class="primary-button" type="button" :disabled="saving || !dirty || Boolean(validationError)" @click="save">
          <span v-if="saving" class="spinner small"></span>保存分组设置
        </button>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import type { IQGroupRow, IQGroupsView, IQThresholds, IQWindow } from '../types'
import { formatAccuracy, formatRelativeTime } from '../utils'

const props = defineProps<{
  now: number
  refreshKey: string | undefined
  canRun: boolean
  thresholds: IQThresholds
}>()
const emit = defineEmits<{
  run: [group: IQGroupRow]
  'open-accounts': [group: IQGroupRow]
  'open-probes': [group: IQGroupRow]
  saved: [ids: number[]]
  loaded: [view: IQGroupsView]
  flash: [title: string, text: string]
  error: [error: unknown]
}>()

const window = ref<IQWindow>('7d')
const view = ref<IQGroupsView | null>(null)
const loading = ref(false)
const saving = ref(false)
const draftIds = ref<number[]>([])
const perRound = ref(2)

const groups = computed(() => view.value?.groups ?? [])
const limits = computed(() => view.value?.limits ?? { max_groups: 30, max_per_round: 10 })
const serverIds = computed(() => view.value?.config.ids ?? [])
const dirty = computed(() =>
  draftIds.value.join(',') !== serverIds.value.join(',') || perRound.value !== (view.value?.config.per_round ?? 2))

const validationError = computed(() => {
  if (!Number.isInteger(perRound.value) || perRound.value < 1 || perRound.value > limits.value.max_per_round) {
    return `每个分组每轮测 1 到 ${limits.value.max_per_round} 个号`
  }
  if (draftIds.value.length > limits.value.max_groups) return `最多同时检测 ${limits.value.max_groups} 个分组`
  return ''
})

async function load() {
  loading.value = true
  try {
    const wasDirty = view.value !== null && dirty.value
    view.value = await api.iqGroups(window.value)
    emit('loaded', view.value)
    if (!wasDirty) reset()
  } catch (error) {
    emit('error', error)
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(() => props.refreshKey, () => void load())

function changeWindow(value: IQWindow) {
  window.value = value
  void load()
}

function reset() {
  draftIds.value = [...serverIds.value]
  perRound.value = view.value?.config.per_round ?? 2
}

function isDraft(id: number): boolean {
  return draftIds.value.includes(id)
}

// 勾选顺序就是矩阵里分组的排列顺序：新勾的排在后面。
function toggle(id: number) {
  draftIds.value = isDraft(id) ? draftIds.value.filter((v) => v !== id) : [...draftIds.value, id]
}

async function save() {
  if (validationError.value) return
  saving.value = true
  try {
    const settings = await api.iqUpdateSettings({ groups: { ids: draftIds.value, per_round: perRound.value } })
    const ids = settings.groups.ids ?? []
    emit('flash', '分组设置已保存', ids.length ? `从下一轮起，${ids.length} 个分组各至少测 ${settings.groups.per_round} 个号。` : '已不再检测任何分组。')
    emit('saved', ids)
    await load()
    reset()
  } catch (error) {
    emit('error', error)
  } finally {
    saving.value = false
  }
}

function rowTone(g: IQGroupRow): 'danger' | 'warning' | '' {
  if (g.insufficient || g.stats.accuracy_percent === null) return ''
  if (g.stats.accuracy_percent < props.thresholds.warning_percent) return 'danger'
  if (g.stats.accuracy_percent < props.thresholds.healthy_percent) return 'warning'
  return ''
}

function accuracyClass(g: IQGroupRow): string {
  const tone = rowTone(g)
  if (tone === 'danger') return 'danger-text'
  if (tone === 'warning') return 'warning-text'
  return g.insufficient ? 'muted' : 'success-text'
}

defineExpose({ load })
</script>
