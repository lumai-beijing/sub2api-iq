<template>
  <section class="panel iq-cell-detail" :class="`level-${card?.level ?? 'unknown'}`" aria-labelledby="iq-cell-detail-title">
    <header class="iq-cell-detail-head">
      <div>
        <p class="eyebrow">{{ kindLabel }}</p>
        <h3 id="iq-cell-detail-title">
          {{ sourceName }} <span class="iq-cell-detail-sep">·</span> <code>{{ model }}</code>
          <span v-if="card" class="iq-level-badge" :class="`level-${card.level}`"><i aria-hidden="true"></i>{{ card.level_label }}</span>
        </h3>
      </div>
      <div class="iq-settings-actions">
        <button class="secondary-button" type="button" :disabled="!canRun" @click="emit('run')">立即检测</button>
        <button class="ghost-button" type="button" @click="emit('open-probes')">全部采样</button>
        <button class="ghost-button" type="button" aria-label="收起详情" @click="emit('close')">收起</button>
      </div>
    </header>

    <p v-if="loading && !view" class="hint">加载中…</p>
    <template v-else-if="card">
      <div class="iq-cell-detail-body">
        <dl class="iq-metrics">
          <div><dt>正确率（最近 {{ card.recent.graded }} 次）</dt><dd>{{ formatAccuracy(card.recent.accuracy_percent) }}</dd></div>
          <div><dt>答对</dt><dd>{{ card.recent.correct }}<em>/{{ card.recent.graded }}</em></dd></div>
          <div><dt>同期出错</dt><dd>{{ card.recent.errors }}</dd></div>
          <div><dt>JSON 作答</dt><dd>{{ formatAccuracy(card.recent.json_percent) }}</dd></div>
          <div><dt>平均耗时</dt><dd>{{ formatMs(card.recent.avg_duration_ms) }}</dd></div>
          <div><dt>换模型</dt><dd :class="{ 'danger-text': card.history.swapped > 0 }">{{ card.history.swapped }}</dd></div>
        </dl>
        <div class="iq-history">
          <div class="iq-history-head">
            <span>最近 {{ (card.points ?? []).length }} 次（左旧右新）</span>
            <span>{{ formatAccuracy(card.history.accuracy_percent) }} · {{ card.history.correct }}/{{ card.history.graded }}<template v-if="card.history.errors"> · 出错 {{ card.history.errors }}</template></span>
          </div>
          <IQHistoryStrip :items="historyItems" :slots="slots" />
          <p v-if="hasFixed" class="iq-field-hint">浅色是随机化之前的固定题面。</p>
        </div>
      </div>
      <h4 class="iq-cell-detail-sub">最近 {{ (view?.probes ?? []).length }} 次作答</h4>
      <IQProbeTable :probes="view?.probes ?? []" :group-names="groupNames" hide-model :hide-ticket="kind === 'provider'"
        :who-label="kind === 'provider' ? '来源' : '账号'" />
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import type { IQCardView, IQScope, IQSourceKind } from '../types'
import { formatAccuracy, formatDateTime, formatMs, iqOutcomeLabel } from '../utils'
import IQHistoryStrip, { type StripItem } from './IQHistoryStrip.vue'
import IQProbeTable from './IQProbeTable.vue'

const props = defineProps<{
  sourceKey: string
  sourceName: string
  kind: IQSourceKind
  model: string
  scope: IQScope
  refreshKey: string | undefined
  canRun: boolean
  groupNames: Record<number, string>
}>()
const emit = defineEmits<{ run: []; close: []; 'open-probes': []; error: [error: unknown] }>()

const view = ref<IQCardView | null>(null)
const loading = ref(false)
const card = computed(() => view.value?.card ?? null)
const kindLabel = computed(() => ({ pool: '号池整体', group: '分组', provider: '第三方供应商' })[props.kind])
const slots = 120

async function load() {
  loading.value = true
  try {
    view.value = await api.iqCard(props.sourceKey, props.model, props.scope)
  } catch (error) {
    emit('error', error)
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(() => [props.sourceKey, props.model, props.scope, props.refreshKey], () => void load())

const historyItems = computed<StripItem[]>(() => (card.value?.points ?? []).slice(-slots).map((p) => ({
  outcome: p.outcome,
  variant: p.variant,
  title: `${formatDateTime(p.at)}${props.kind === 'provider' ? '' : ` · #${p.account_id} ${p.account_name}`} · ${iqOutcomeLabel(p.outcome)}` +
    (p.answer ? ` · 答 ${p.answer}` : '') + (p.upstream_model ? ` · ${p.upstream_model}` : '') + (p.swapped ? '（换模型）' : '') +
    (p.variant === 'fixed' ? '（固定题面）' : '')
})))
const hasFixed = computed(() => (card.value?.points ?? []).some((p) => p.variant === 'fixed'))
</script>
