<template>
  <div class="stack">
    <section v-if="error" class="error-banner" role="alert">
      <strong>读取失败</strong>
      <span>{{ error }}</span>
    </section>

    <section class="hero-row">
      <div>
        <p class="eyebrow">Read-only snapshot</p>
        <h2>续费看板</h2>
        <p class="muted">套餐到期时间来自 OAuth 凭证快照；不会触发任何上游请求。</p>
      </div>
    </section>

    <section class="summary-grid">
      <button
        v-for="card in summaryCards"
        :key="card.key"
        type="button"
        class="summary-card"
        :class="[`tone-${card.tone}`, { active: filters.summaryMetric === card.key }]"
        @click="setSummaryMetric(card.key)"
      >
        <span>{{ card.label }}</span>
        <strong>{{ card.value }}</strong>
        <small>{{ card.hint }}</small>
      </button>
    </section>

    <section class="panel">
      <div class="filter-row">
        <label class="search-field">
          <span>搜索</span>
          <input v-model="filters.search" placeholder="账号 ID、名称、邮箱" />
        </label>
        <label>
          <span>到期分级</span>
          <select v-model="filters.expiryLevel">
            <option value="all">全部</option>
            <option value="expired">已过期</option>
            <option value="critical">3天内</option>
            <option value="warning">7天内</option>
            <option value="attention">14天内</option>
            <option value="upcoming">30天内</option>
            <option value="normal">正常</option>
            <option value="unknown">到期未知</option>
          </select>
        </label>
        <label>
          <span>账号状态</span>
          <select v-model="filters.status">
            <option value="all">全部</option>
            <option value="active">正常</option>
            <option value="inactive">停用</option>
            <option value="error">异常</option>
          </select>
        </label>
        <label>
          <span>可调度</span>
          <select v-model="filters.schedulable">
            <option value="all">全部</option>
            <option value="yes">是</option>
            <option value="no">否</option>
          </select>
        </label>
        <button class="ghost-button filter-clear" type="button" @click="clearFilters">清空筛选</button>
      </div>

      <div class="table-meta">
        <span>显示 {{ visibleAccounts.length }} / {{ snapshot?.accounts.length ?? 0 }} 个账号</span>
        <span>缓存时间：{{ formatDateTime(snapshot?.cached_at) }}</span>
      </div>

      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>告警</th>
              <th>账号</th>
              <th>状态</th>
              <th>套餐到期</th>
              <th>账号有效期</th>
              <th>5h 用量</th>
              <th>7d 用量</th>
              <th>用量快照</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="account in visibleAccounts" :key="account.id" :class="`row-${account.expiry_level}`">
              <td>
                <div class="badge-stack">
                  <span class="level-badge" :class="`level-${account.expiry_level}`">
                    {{ expiryLevelLabel(account.expiry_level) }}
                  </span>
                  <span v-if="account.expiry_conflict" class="mini-badge danger">账号先到期</span>
                  <span v-if="account.usage_warning" class="mini-badge warning">用量≥90%</span>
                </div>
              </td>
              <td>
                <div class="account-name" :title="account.name">{{ account.name }}</div>
                <div class="account-meta">#{{ account.id }}<span v-if="account.email"> · {{ account.email }}</span></div>
              </td>
              <td>
                <div class="badge-stack">
                  <span class="status-badge" :class="`status-${account.status}`">{{ statusLabel(account.status) }}</span>
                  <span class="mini-badge" :class="account.schedulable ? 'success' : 'muted'">
                    {{ account.schedulable ? '可调度' : '不可调度' }}
                  </span>
                </div>
              </td>
              <td>
                <div class="expiry-date">{{ formatDate(account.subscription_expires_at) }}</div>
                <div class="account-meta">{{ accountCountdown(account) }}</div>
              </td>
              <td>
                <div class="expiry-date">{{ formatDate(account.account_expires_at) }}</div>
                <div v-if="account.expiry_conflict" class="account-meta danger-text">早于套餐到期</div>
                <div v-else class="account-meta">{{ account.account_expires_at ? '调度有效期' : '未单独设置' }}</div>
              </td>
              <td><UsageCell :window="account.five_hour" :stale="account.usage_stale" /></td>
              <td><UsageCell :window="account.seven_day" :stale="account.usage_stale" /></td>
              <td>
                <div class="snapshot-cell" :class="{ stale: account.usage_stale }">
                  <strong>{{ formatRelativeTime(account.usage_snapshot_at, nowTick) }}</strong>
                  <span v-if="account.usage_stale">数据已过期</span>
                  <span v-else>有效</span>
                </div>
                <div v-if="account.error_message" class="account-error" :title="account.error_message">
                  {{ account.error_message }}
                </div>
              </td>
              <td>
                <div class="row-actions">
                  <button class="text-button" type="button" @click="copyAccount(account)">
                    {{ copiedAccountID === account.id ? '已复制' : '复制' }}
                  </button>
                  <a class="text-link" :href="account.admin_url" target="_blank" rel="noopener noreferrer">处理</a>
                </div>
              </td>
            </tr>
            <tr v-if="visibleAccounts.length === 0">
              <td colspan="9" class="empty-state">没有符合筛选条件的账号</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import type { Account, FilterState, Snapshot, SummaryMetric } from '../types'
import {
  expiryLevelLabel,
  filterAccounts,
  formatCountdown,
  formatDate,
  formatDateTime,
  formatRelativeTime,
  statusLabel
} from '../utils'
import UsageCell from './UsageCell.vue'

const props = defineProps<{
  snapshot: Snapshot | null
  loading: boolean
  error: string
}>()

const filters = reactive<FilterState>({
  search: '',
  summaryMetric: 'all',
  expiryLevel: 'all',
  status: 'all',
  schedulable: 'all'
})
const copiedAccountID = ref<number | null>(null)
const nowTick = ref(Date.now())
let clockTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  clockTimer = setInterval(() => {
    nowTick.value = Date.now()
  }, 30_000)
})

onBeforeUnmount(() => {
  if (clockTimer) clearInterval(clockTimer)
})

const summaryCards = computed(() => {
  const summary = props.snapshot?.summary
  return [
    { key: 'all' as const, label: '全部 Pro', value: summary?.total ?? 0, hint: '当前监控范围', tone: 'neutral' },
    { key: 'expired' as const, label: '套餐已过期', value: summary?.expired ?? 0, hint: '应优先处理', tone: 'danger' },
    { key: 'due_3_days' as const, label: '3天内', value: summary?.due_3_days ?? 0, hint: '紧急续费', tone: 'danger' },
    { key: 'due_7_days' as const, label: '7天内', value: summary?.due_7_days ?? 0, hint: '即将到期', tone: 'warning' },
    { key: 'due_14_days' as const, label: '14天内', value: summary?.due_14_days ?? 0, hint: '提前安排', tone: 'attention' },
    { key: 'due_30_days' as const, label: '30天内', value: summary?.due_30_days ?? 0, hint: '本周期内', tone: 'upcoming' },
    { key: 'status_error' as const, label: '状态异常', value: summary?.status_error ?? 0, hint: '需要检查', tone: 'danger' },
    { key: 'unschedulable' as const, label: '不可调度', value: summary?.unschedulable ?? 0, hint: '当前不接流量', tone: 'warning' },
    { key: 'expiry_conflict' as const, label: '有效期冲突', value: summary?.expiry_conflict ?? 0, hint: '账号先于套餐失效', tone: 'warning' },
    { key: 'usage_warning' as const, label: '用量告警', value: summary?.usage_warning ?? 0, hint: '5h/7d ≥90%', tone: 'warning' },
    { key: 'usage_snapshot_gap' as const, label: '快照缺失', value: summary?.usage_snapshot_gap ?? 0, hint: '无用量时间', tone: 'neutral' }
  ]
})

const visibleAccounts = computed(() => filterAccounts(props.snapshot?.accounts ?? [], filters))

function setSummaryMetric(metric: SummaryMetric) {
  filters.summaryMetric = filters.summaryMetric === metric ? 'all' : metric
}

function clearFilters() {
  filters.search = ''
  filters.summaryMetric = 'all'
  filters.expiryLevel = 'all'
  filters.status = 'all'
  filters.schedulable = 'all'
}

function accountCountdown(account: Account): string {
  if (!account.subscription_expires_at) return '到期时间缺失'
  const generatedAt = props.snapshot ? Date.parse(props.snapshot.generated_at) : Date.now()
  const elapsedSeconds = Math.max(0, Math.trunc((nowTick.value - generatedAt) / 1000))
  return formatCountdown(account.seconds_remaining - elapsedSeconds)
}

async function copyAccount(account: Account) {
  const text = `${account.id}\t${account.name}`
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const input = document.createElement('textarea')
    input.value = text
    document.body.appendChild(input)
    input.select()
    document.execCommand('copy')
    input.remove()
  }
  copiedAccountID.value = account.id
  window.setTimeout(() => {
    if (copiedAccountID.value === account.id) copiedAccountID.value = null
  }, 1500)
}
</script>
