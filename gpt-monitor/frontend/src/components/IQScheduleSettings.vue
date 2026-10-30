<template>
  <section class="iq-schedule" aria-labelledby="iq-schedule-title">
    <header class="iq-schedule-head">
      <div>
        <h3 id="iq-schedule-title">定时检测</h3>
        <p class="iq-schedule-sub">
          {{ schedule.config_source === 'settings' ? '已在这里自定义' : '当前为部署默认值' }} · 保存后立即生效，不用重启
        </p>
      </div>
      <label class="iq-switch">
        <input type="checkbox" :checked="draft.enabled" :disabled="saving" @change="draft.enabled = ($event.target as HTMLInputElement).checked" />
        <span class="iq-switch-track" aria-hidden="true"><span class="iq-switch-thumb"></span></span>
        <span class="iq-switch-label">{{ draft.enabled ? '已开启' : '已关闭' }}</span>
      </label>
    </header>

    <div class="iq-schedule-grid" :class="{ muted: !draft.enabled }">
      <!-- 每轮抽几个号 -->
      <fieldset class="iq-field">
        <legend>每轮检测的账号</legend>
        <div class="iq-segment" role="radiogroup" aria-label="抽样方式">
          <button type="button" role="radio" :aria-checked="draft.sample_mode === 'ratio'" :class="{ active: draft.sample_mode === 'ratio' }" @click="draft.sample_mode = 'ratio'">按比例</button>
          <button type="button" role="radio" :aria-checked="draft.sample_mode === 'count'" :class="{ active: draft.sample_mode === 'count' }" @click="draft.sample_mode = 'count'">按个数</button>
        </div>
        <div v-if="draft.sample_mode === 'ratio'" class="iq-inline">
          <label>
            <span>比例</span>
            <span class="iq-input-unit"><input v-model.number="draft.sample_percent" type="number" min="1" max="100" step="1" inputmode="numeric" /><em>%</em></span>
          </label>
          <label>
            <span>至少</span>
            <span class="iq-input-unit"><input v-model.number="draft.sample_min" type="number" min="1" max="200" step="1" inputmode="numeric" /><em>个</em></span>
          </label>
        </div>
        <div v-else class="iq-inline">
          <label>
            <span>每轮</span>
            <span class="iq-input-unit"><input v-model.number="draft.sample_count" type="number" min="1" max="200" step="1" inputmode="numeric" /><em>个</em></span>
          </label>
        </div>
        <p class="iq-field-hint">按当前 {{ schedule.candidates }} 个可用 OAuth 号，每轮抽 <strong>{{ previewSample }}</strong> 个{{ previewSampleNote }}</p>
        <p v-if="schedule.groups > 0" class="iq-field-hint">
          另外给 {{ schedule.groups }} 个分组各补测到至少 {{ schedule.group_per_round }} 个号（在「分组」里设置）<template
            v-if="schedule.last_round_pool + schedule.last_round_top_ups > 0">，上一轮共测 {{ schedule.last_round_pool + schedule.last_round_top_ups }} 个：号池 {{ schedule.last_round_pool }} + 分组补测 {{ schedule.last_round_top_ups }}</template>。
        </p>
      </fieldset>

      <!-- 锚定周期 + 随机范围 -->
      <fieldset class="iq-field">
        <legend>检测周期</legend>
        <div class="iq-inline">
          <label>
            <span>每隔</span>
            <span class="iq-input-unit">
              <input v-model.number="intervalValue" type="number" min="1" step="1" inputmode="numeric" />
              <select v-model="intervalUnit" aria-label="周期单位">
                <option value="60">分钟</option>
                <option value="3600">小时</option>
              </select>
            </span>
          </label>
          <label>
            <span>随机 ±</span>
            <span class="iq-input-unit">
              <input v-model.number="jitterValue" type="number" min="0" step="1" inputmode="numeric" />
              <select v-model="jitterUnit" aria-label="随机范围单位">
                <option value="1">秒</option>
                <option value="60">分钟</option>
              </select>
            </span>
          </label>
        </div>
        <p class="iq-field-hint">
          锚点对齐到每 {{ formatSecondsLabel(draft.interval_seconds) }} 的整点（{{ anchorExample }}），
          每轮在锚点前后 {{ formatSecondsLabel(draft.jitter_seconds) }} 内随机开跑；随机范围最多是周期的一半。
        </p>
      </fieldset>
    </div>

    <p v-if="validationError" class="form-error" role="alert">{{ validationError }}</p>
    <p v-else-if="slowRoundWarning" class="iq-warning" role="status">{{ slowRoundWarning }}</p>

    <footer class="iq-schedule-foot">
      <div class="iq-schedule-status">
        <template v-if="schedule.enabled">
          <span>下次锚点 <strong>{{ formatClock(schedule.next_anchor_at) }}</strong></span>
          <span>实际开跑 <strong>{{ formatClock(schedule.next_run_at) }}</strong>（{{ formatTimeUntil(schedule.next_run_at, now) }}）</span>
        </template>
        <span v-else>定时检测已关闭，可以随时手动「立即跑一轮」。</span>
        <span v-if="schedule.last_round_seconds !== null">上一轮耗时 {{ formatSecondsLabel(schedule.last_round_seconds) }}</span>
        <span v-if="schedule.skipped_slots > 0" class="warning-text">因上一轮超时已跳过 {{ schedule.skipped_slots }} 个周期</span>
      </div>
      <div class="iq-settings-actions">
        <button class="ghost-button" type="button" :disabled="saving || !dirty" @click="reset">还原</button>
        <button class="primary-button" type="button" :disabled="saving || !dirty || Boolean(validationError)" @click="save">
          <span v-if="saving" class="spinner small"></span>保存定时配置
        </button>
      </div>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { IQSchedule, IQScheduleConfig } from '../types'
import { formatTimeUntil } from '../utils'

const props = defineProps<{
  schedule: IQSchedule
  saving: boolean
  now: number
}>()
const emit = defineEmits<{ save: [config: IQScheduleConfig] }>()

const MIN_INTERVAL = 60
const MAX_INTERVAL = 24 * 3600
const MAX_SAMPLE = 200

function pickConfig(s: IQScheduleConfig): IQScheduleConfig {
  return {
    enabled: s.enabled, interval_seconds: s.interval_seconds, jitter_seconds: s.jitter_seconds,
    sample_mode: s.sample_mode, sample_percent: s.sample_percent, sample_min: s.sample_min, sample_count: s.sample_count
  }
}

const draft = reactive<IQScheduleConfig>(pickConfig(props.schedule))
// 周期和随机范围按「数值 + 单位」编辑，换算成秒存进 draft。
const intervalUnit = ref<'60' | '3600'>('60')
const jitterUnit = ref<'1' | '60'>('1')
const intervalValue = ref(0)
const jitterValue = ref(0)

function loadUnits(config: IQScheduleConfig) {
  intervalUnit.value = config.interval_seconds % 3600 === 0 ? '3600' : '60'
  intervalValue.value = config.interval_seconds / Number(intervalUnit.value)
  jitterUnit.value = config.jitter_seconds >= 60 && config.jitter_seconds % 60 === 0 ? '60' : '1'
  jitterValue.value = config.jitter_seconds / Number(jitterUnit.value)
}
loadUnits(draft)

watch([intervalValue, intervalUnit], () => { draft.interval_seconds = Math.round(Number(intervalValue.value) * Number(intervalUnit.value)) })
watch([jitterValue, jitterUnit], () => { draft.jitter_seconds = Math.round(Number(jitterValue.value) * Number(jitterUnit.value)) })

const serverConfig = computed(() => pickConfig(props.schedule))
const dirty = computed(() => JSON.stringify(pickConfig(draft)) !== JSON.stringify(serverConfig.value))

// 服务端配置变了（保存成功、或别人在另一个窗口改了）且本地没有未保存的修改时，跟着更新。
watch(serverConfig, (next, previous) => {
  if (previous && JSON.stringify(pickConfig(draft)) !== JSON.stringify(previous)) return
  Object.assign(draft, next)
  loadUnits(next)
})

function reset() {
  Object.assign(draft, serverConfig.value)
  loadUnits(serverConfig.value)
}

const validationError = computed(() => {
  const d = draft
  if (!Number.isInteger(d.interval_seconds) || d.interval_seconds < MIN_INTERVAL || d.interval_seconds > MAX_INTERVAL) {
    return '检测周期需在 1 分钟到 24 小时之间'
  }
  if (!Number.isInteger(d.jitter_seconds) || d.jitter_seconds < 0) return '随机范围不能为负'
  if (d.jitter_seconds * 2 > d.interval_seconds) {
    return `随机范围最多是周期的一半（当前周期最多 ±${formatSecondsLabel(Math.floor(d.interval_seconds / 2))}）`
  }
  if (d.sample_mode === 'ratio') {
    if (!Number.isInteger(d.sample_percent) || d.sample_percent < 1 || d.sample_percent > 100) return '抽样比例需在 1% 到 100% 之间'
    if (!Number.isInteger(d.sample_min) || d.sample_min < 1 || d.sample_min > MAX_SAMPLE) return `每轮最少个数需在 1 到 ${MAX_SAMPLE} 之间`
  } else if (!Number.isInteger(d.sample_count) || d.sample_count < 1 || d.sample_count > MAX_SAMPLE) {
    return `每轮个数需在 1 到 ${MAX_SAMPLE} 之间`
  }
  return ''
})

// 与后端 SampleSizeFor 同一套算法：比例向上取整、不低于下限、不超过可用总数。
const previewSample = computed(() => {
  const available = props.schedule.candidates
  if (available <= 0) return 0
  const n = draft.sample_mode === 'count'
    ? draft.sample_count
    : Math.max(Math.ceil((available * draft.sample_percent) / 100), draft.sample_min)
  return Math.min(Math.max(n || 0, 1), available)
})
const previewSampleNote = computed(() => {
  const available = props.schedule.candidates
  if (available <= 0) return '（可用号数在第一轮之后显示）'
  if (draft.sample_mode === 'ratio') {
    const raw = Math.ceil((available * draft.sample_percent) / 100)
    if (raw < draft.sample_min && previewSample.value === draft.sample_min) return `（${draft.sample_percent}% 只有 ${raw} 个，按下限取 ${draft.sample_min} 个）`
  }
  if (draft.sample_mode === 'count' && draft.sample_count > available) return `（超过可用总数，只能测全部 ${available} 个）`
  return ''
})

// 每轮最多并发几个号、每个号答题要多久，决定了周期太短时会不会追不上。分组补测的号也算在一轮里。
const slowRoundWarning = computed(() => {
  const last = props.schedule.last_round_seconds
  if (!draft.enabled || last === null || last <= 0) return ''
  const concurrency = Math.max(props.schedule.concurrency, 1)
  const lastTargets = props.schedule.last_round_pool + props.schedule.last_round_top_ups || props.schedule.sample_size
  const perBatch = lastTargets > 0 ? last / Math.ceil(lastTargets / concurrency) : last
  const estimate = Math.ceil((previewSample.value + props.schedule.last_round_top_ups) / concurrency) * perBatch
  const window = draft.interval_seconds - draft.jitter_seconds
  if (estimate <= window) return ''
  return `按上一轮的速度估算，这个配置一轮约要 ${formatSecondsLabel(Math.round(estimate))}，超过了周期，会自动跳过追不上的轮次。可以加长周期或减少每轮个数。`
})

// 与后端 anchorOrigin 一致：锚点按北京时间对齐。周期能整除一天时落在当地整点上。
const anchorExample = computed(() => {
  const seconds = draft.interval_seconds
  if (seconds <= 0) return ''
  if (seconds === 3600) return '北京时间每个整点'
  if (seconds % 3600 === 0 && 86400 % seconds === 0) {
    const hours = Array.from({ length: Math.min(86400 / seconds, 4) }, (_, i) => (i * seconds) / 3600)
    return `北京时间 ${hours.join('、')}${86400 / seconds > 4 ? '…' : ''} 点`
  }
  if (3600 % seconds === 0 && seconds % 60 === 0) {
    const minutes = Array.from({ length: Math.min(3600 / seconds, 4) }, (_, i) => String((i * seconds) / 60).padStart(2, '0'))
    return `每小时的 :${minutes.join('、:')}${3600 / seconds > 4 ? '…' : ''}`
  }
  return '按固定节奏连续推进，不随重启漂移'
})

function save() {
  if (validationError.value) return
  emit('save', pickConfig(draft))
}

function formatSecondsLabel(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds)) return '-'
  if (seconds >= 3600 && seconds % 3600 === 0) return `${seconds / 3600} 小时`
  if (seconds >= 60 && seconds % 60 === 0) return `${seconds / 60} 分钟`
  if (seconds >= 60) return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
  return `${seconds} 秒`
}

function formatClock(value: string | null): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(date)
}
</script>
