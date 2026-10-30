<template>
  <div class="iq-providers">
    <IQProviderForm
      v-if="editing !== undefined"
      :key="editing?.id ?? 'new'"
      :provider="editing"
      :limits="limits"
      :default-models="view?.default_models ?? ''"
      :saving="saving"
      @save="save"
      @cancel="editing = undefined"
    />

    <div v-else class="iq-providers-head">
      <p class="section-hint">
        第三方供应商各自独立定时检测（周期、测试模型分开配），和号池同一道题、同一套判分；每次检测都是新会话、题面随机改写。
        Key 加密存在监控本地，页面上只显示末 4 位。各家的答对率在上方总览里和号池、分组并排比较。
      </p>
      <button class="primary-button" type="button" :disabled="providers.length >= limits.max_providers" @click="editing = null">添加供应商</button>
    </div>

    <p v-if="loading && !view" class="hint">加载中…</p>
    <p v-else-if="!providers.length && editing === undefined" class="empty-state">还没有第三方供应商。点「添加供应商」，填 Base URL 和 API Key 就能开始测。</p>

    <article v-for="p in providers" :key="p.id" class="iq-provider" :class="`level-${p.level}`">
      <header class="iq-provider-head">
        <div>
          <div>
            <strong class="iq-provider-name">{{ p.name }}</strong>
            <span class="iq-level-badge" :class="`level-${p.level}`"><i aria-hidden="true"></i>{{ p.level_label }}</span>
          </div>
          <p class="iq-card-meta">
            <code>{{ p.endpoint }}</code> · Key {{ p.api_key_hint }}
            <span v-if="p.insecure" class="mini-badge warning">http 明文</span>
          </p>
          <p class="iq-card-meta">
            <template v-if="p.schedule.enabled">
              每 {{ secondsLabel(p.schedule.interval_seconds) }}（±{{ secondsLabel(p.schedule.jitter_seconds) }}）· 每个模型每轮 {{ p.schedule.samples_per_model }} 次 ·
              {{ p.status.running ? `检测中（${p.status.running_targets} 题）` : `下次 ${formatTimeUntil(p.status.next_run_at, now)}` }}
            </template>
            <template v-else>定时检测已关闭</template>
            <template v-if="p.status.last_run_at"> · 上次 {{ formatRelativeTime(p.status.last_run_at, now) }}</template>
          </p>
          <p v-if="p.status.last_error" class="iq-card-meta danger-text">上一轮没跑成：{{ p.status.last_error }}</p>
        </div>
        <div class="iq-settings-actions">
          <button class="secondary-button" type="button" :disabled="p.status.running" @click="run(p)">立即检测</button>
          <button class="secondary-button" type="button" @click="$emit('open-probes', { source: `provider:${p.id}`, title: p.name })">采样详情</button>
          <button class="ghost-button" type="button" @click="editing = p">编辑</button>
          <button class="ghost-button danger-text" type="button" @click="remove(p)">删除</button>
        </div>
      </header>

      <div class="iq-provider-models">
        <div v-for="card in p.cards ?? []" :key="card.spec.label" class="iq-provider-model" :class="`level-${card.level}`">
          <div class="iq-provider-model-name">
            <code>{{ card.spec.label }}</code>
            <span class="iq-level-badge small" :class="`level-${card.level}`"><i aria-hidden="true"></i>{{ card.level_label }}</span>
          </div>
          <div class="iq-provider-model-stats">
            <strong>{{ formatAccuracy(card.recent.accuracy_percent) }}</strong>
            <span>最近 {{ card.recent.graded }} 次答对 {{ card.recent.correct }}<template v-if="card.recent.errors"> · 出错 {{ card.recent.errors }}</template> · 平均 {{ formatMs(card.recent.avg_duration_ms) }}</span>
          </div>
          <IQHistoryStrip compact :items="strip(card)" :slots="30" />
          <div class="iq-provider-model-actions">
            <button class="text-button" type="button" :disabled="p.status.running" @click="run(p, card.spec.label)">只测这个模型</button>
            <button class="text-button" type="button" @click="$emit('select-cell', `provider:${p.id}`, p.name, card.spec.label)">看详情</button>
          </div>
        </div>
      </div>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import type { IQModelCard, IQProvider, IQProviderInput, IQProvidersView } from '../types'
import { formatAccuracy, formatDateTime, formatMs, formatRelativeTime, formatTimeUntil, iqOutcomeLabel } from '../utils'
import IQHistoryStrip, { type StripItem } from './IQHistoryStrip.vue'
import IQProviderForm from './IQProviderForm.vue'

const props = defineProps<{ now: number; refreshKey: string | undefined }>()
const emit = defineEmits<{
  'open-probes': [scope: { source: string; model?: string; title: string }]
  'select-cell': [source: string, name: string, model: string]
  changed: []
  flash: [title: string, text: string]
  error: [error: unknown]
}>()

const view = ref<IQProvidersView | null>(null)
const loading = ref(false)
const saving = ref(false)
// undefined = 没在编辑；null = 新增；IQProvider = 编辑这一个
const editing = ref<IQProvider | null | undefined>(undefined)

const providers = computed(() => view.value?.providers ?? [])
const limits = computed(() => view.value?.limits ?? { max_providers: 20, max_models: 8, max_samples_per_model: 5 })

async function load() {
  loading.value = true
  try {
    view.value = await api.iqProviders()
  } catch (error) {
    emit('error', error)
  } finally {
    loading.value = false
  }
}
onMounted(load)
// 看板整体刷新时（5 分钟自动 / 手动）跟着刷新
watch(() => props.refreshKey, () => { if (editing.value === undefined) void load() })

async function save(input: IQProviderInput) {
  saving.value = true
  try {
    const existing = editing.value
    if (existing) {
      await api.iqUpdateProvider(existing.id, input)
      emit('flash', '已保存', `${input.name} 已按新配置重新排定下一轮。`)
    } else {
      await api.iqCreateProvider(input)
      emit('flash', '已添加', `${input.name} 会在下一个锚点开始检测，也可以点「立即检测」。`)
    }
    editing.value = undefined
    await load()
    emit('changed')
  } catch (error) {
    emit('error', error)
  } finally {
    saving.value = false
  }
}

async function remove(p: IQProvider) {
  if (!window.confirm(`删除供应商「${p.name}」？`)) return
  const keep = window.confirm('要保留它的历史检测记录吗？\n\n确定 = 保留（之后不再显示，但数据还在库里）\n取消 = 一并删除')
  try {
    await api.iqDeleteProvider(p.id, keep)
    emit('flash', '已删除', keep ? `${p.name} 已删除，检测记录保留。` : `${p.name} 及其检测记录已删除。`)
    await load()
    emit('changed')
  } catch (error) {
    emit('error', error)
  }
}

async function run(p: IQProvider, model?: string) {
  try {
    const response = await api.iqRunProvider(p.id, model)
    emit('flash', '检测已受理', `${p.name}：${response.targets} 题已开始，推理较慢，结果会在 1～3 分钟内出现。`)
    await load()
    setTimeout(() => void load(), 45_000)
    setTimeout(() => void load(), 120_000)
  } catch (error) {
    emit('error', error)
  }
}

function strip(card: IQModelCard): StripItem[] {
  return (card.points ?? []).slice(-30).map((point) => ({
    outcome: point.outcome,
    variant: point.variant,
    title: `${formatDateTime(point.at)} · ${iqOutcomeLabel(point.outcome)}` +
      (point.answer ? ` · 答 ${point.answer}` : '') + (point.upstream_model ? ` · ${point.upstream_model}` : '') +
      (point.variant === 'fixed' ? '（固定题面）' : '')
  }))
}

function secondsLabel(seconds: number): string {
  if (seconds >= 3600 && seconds % 3600 === 0) return `${seconds / 3600} 小时`
  if (seconds >= 60 && seconds % 60 === 0) return `${seconds / 60} 分钟`
  return `${seconds} 秒`
}

defineExpose({ load })
</script>
