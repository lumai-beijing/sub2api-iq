<template>
  <div class="detail-body">
    <div class="filter-row iq-probes-filters">
      <label>
        <span>来源</span>
        <select :value="filter.source" @change="update({ source: ($event.target as HTMLSelectElement).value, accountId: undefined })">
          <option v-for="option in sourceOptions" :key="option.key" :value="option.key">{{ option.label }}</option>
        </select>
      </label>
      <label>
        <span>结果</span>
        <select :value="filter.outcome" @change="update({ outcome: ($event.target as HTMLSelectElement).value as ProbeFilter['outcome'] })">
          <option value="">全部</option>
          <option value="correct">答对</option>
          <option value="wrong">答错</option>
          <option value="no_answer">没给答案</option>
          <option value="error">出错</option>
        </select>
      </label>
      <label>
        <span>题面</span>
        <select :value="filter.scope" @change="update({ scope: ($event.target as HTMLSelectElement).value as IQScope })">
          <option value="all">全部题面</option>
          <option value="random">只看随机题面</option>
          <option value="fixed">只看固定题面（改版前）</option>
        </select>
      </label>
      <div class="probe-scope">
        <span class="eyebrow">当前范围</span>
        <strong>{{ scopeTitle }}</strong>
      </div>
      <button v-if="filter.accountId || filter.model" class="ghost-button filter-clear" type="button" @click="update({ accountId: undefined, model: undefined })">
        去掉{{ filter.accountId ? '账号' : '模型' }}筛选
      </button>
    </div>
    <div class="table-meta"><span>最近 {{ probes.length }} 条（最多 180 条）</span><span v-if="loading" class="spinner small"></span></div>
    <IQProbeTable :probes="probes" :loading="loading" :group-names="groupNames" :hide-ticket="filter.source.startsWith('provider:')" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import type { IQOutcome, IQProbe, IQScope } from '../types'
import IQProbeTable from './IQProbeTable.vue'

export interface ProbeFilter {
  source: string
  accountId?: number
  model?: string
  outcome: '' | IQOutcome
  scope: IQScope
  title?: string
}

const props = defineProps<{
  filter: ProbeFilter
  sourceOptions: { key: string; label: string }[]
  groupNames: Record<number, string>
  refreshKey: string | undefined
}>()
const emit = defineEmits<{ 'update:filter': [filter: ProbeFilter]; error: [error: unknown] }>()

const probes = ref<IQProbe[]>([])
const loading = ref(false)

const scopeTitle = computed(() => {
  const source = props.sourceOptions.find((o) => o.key === props.filter.source)?.label ?? props.filter.source
  const parts = [props.filter.title || source]
  if (props.filter.model && !props.filter.title?.includes(props.filter.model)) parts.push(props.filter.model)
  return parts.join(' · ')
})

async function load() {
  loading.value = true
  try {
    const view = await api.iqProbes({
      source: props.filter.source, accountId: props.filter.accountId, model: props.filter.model,
      outcome: props.filter.outcome || undefined, scope: props.filter.scope, limit: 180
    })
    probes.value = view.probes ?? []
  } catch (error) {
    emit('error', error)
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(() => [props.filter, props.refreshKey], () => void load(), { deep: true })

function update(patch: Partial<ProbeFilter>) {
  emit('update:filter', { ...props.filter, ...patch, title: undefined })
}
</script>
