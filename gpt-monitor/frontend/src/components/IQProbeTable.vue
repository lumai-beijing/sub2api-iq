<template>
  <div class="table-wrap">
    <table class="dense iq-probe-table">
      <thead>
        <tr>
          <th>时间</th>
          <th>{{ whoLabel }}</th>
          <th v-if="!hideModel">模型</th>
          <th>结果</th>
          <th>答案</th>
          <th>上游模型</th>
          <th v-if="!hideTicket">292</th>
          <th>耗时</th>
          <th>推理要点 / 错误</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="p in probes" :key="p.id">
          <td>
            {{ formatDateTime(p.created_at) }}
            <div class="account-meta">
              {{ p.trigger === 'manual' ? '手动' : '定时' }} · {{ p.variant === 'fixed' ? '固定题面' : '随机题面' }}
            </div>
          </td>
          <td>
            <div class="account-name" :title="p.account_name">{{ p.account_name }}</div>
            <div class="account-meta">
              <template v-if="p.provider_id">第三方</template>
              <template v-else>
                #{{ p.account_id }}
                <span v-if="p.account_type === 'apikey'" class="mini-badge warning">API Key 号</span>
              </template>
            </div>
            <div v-if="!p.provider_id && (p.group_ids ?? []).length" class="iq-group-chips">
              <span v-for="id in p.group_ids ?? []" :key="id" class="iq-group-chip" :class="{ sampled: id === p.sample_group_id }"
                :title="id === p.sample_group_id ? '为这个分组补测的' : '检测时所在的分组'">{{ groupName(id) }}</span>
            </div>
          </td>
          <td v-if="!hideModel">{{ p.model }}<div class="account-meta">{{ p.reasoning_effort || '默认推理' }}</div></td>
          <td><span class="level-badge" :class="`iq-outcome-${p.outcome}`">{{ iqOutcomeLabel(p.outcome) }}</span></td>
          <td>
            <strong>{{ p.answer_value || '-' }}</strong>
            <div class="account-meta" v-if="p.answer_format">{{ formatLabel(p.answer_format) }}</div>
          </td>
          <td>
            {{ p.upstream_model || '-' }}
            <div class="account-meta" v-if="p.model_verdict === 'degraded'"><span class="mini-badge danger">换模型</span></div>
          </td>
          <td v-if="!hideTicket">{{ p.ticket_injected === null ? '-' : p.ticket_injected ? '已注入' : '未注入' }}</td>
          <td>{{ formatMs(p.duration_ms) }}</td>
          <td class="probe-answer">
            <span v-if="p.outcome === 'error'" class="danger-text">{{ p.error_message }}</span>
            <div v-else-if="p.answer_reasoning">{{ p.answer_reasoning }}</div>
            <details v-if="p.prompt">
              <summary>这次的题面</summary>
              <pre>{{ p.prompt }}</pre>
            </details>
            <details v-if="p.answer_excerpt">
              <summary>回答原文</summary>
              <pre>{{ p.answer_excerpt }}</pre>
            </details>
          </td>
        </tr>
        <tr v-if="probes.length === 0">
          <td :colspan="columnCount" class="empty-state">{{ loading ? '加载中…' : '没有记录' }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { IQProbe } from '../types'
import { formatDateTime, formatMs, iqOutcomeLabel } from '../utils'

const props = withDefaults(defineProps<{
  probes: IQProbe[]
  loading?: boolean
  groupNames?: Record<number, string>
  hideModel?: boolean
  hideTicket?: boolean
  whoLabel?: string
}>(), { loading: false, groupNames: () => ({}), hideModel: false, hideTicket: false, whoLabel: '账号 / 来源' })

const columnCount = computed(() => 9 - (props.hideModel ? 1 : 0) - (props.hideTicket ? 1 : 0))

function groupName(id: number): string {
  return props.groupNames[id] ?? `#${id}`
}

function formatLabel(format: string): string {
  return ({ json: 'JSON 作答', text: '未按 JSON，取文末数字', none: '没解析出答案' } as Record<string, string>)[format] ?? format
}
</script>
