<template>
  <div class="codex-view">
    <section v-if="error" class="error-banner" role="alert">
      <strong>读取失败</strong><span>{{ error }}</span>
    </section>
    <section v-if="overview?.freshness.degraded" class="error-banner soft" role="status">
      <strong>数据降级</strong>
      <span>{{ (overview.freshness.notes ?? []).join('；') || '部分面板展示的是上一份成功结果' }}</span>
    </section>

    <!-- 总览 -->
    <section class="overview">
      <header class="overview-head">
        <div>
          <p class="eyebrow">turn-state {{ config.target_length }} · {{ models.length }} 个门控模型</p>
          <h2>打票号池总览</h2>
        </div>
        <div class="state-chips">
          <span class="chip" :class="config.enabled ? 'chip-ok' : 'chip-crit'">打票 {{ config.enabled ? '已开' : '已关' }}</span>
          <span class="chip" :class="config.fail_closed ? 'chip-ok' : 'chip-warn'">无票冷却 {{ config.fail_closed ? '已开' : '已关' }}</span>
          <span class="chip chip-mute">可调度 opt-in {{ population.eligible }} / {{ population.oauth_total }}</span>
          <span class="chip chip-mute">票池 {{ formatRelativeTime(overview?.freshness.pool_at, nowTick) }}</span>
          <span class="chip chip-mute">用量 {{ formatRelativeTime(overview?.freshness.usage_at, nowTick) }}</span>
        </div>
      </header>

      <!-- 每个门控模型一张卡：既是该模型的健康摘要，也是切换入口 -->
      <div class="model-grid" v-if="models.length">
        <button
          v-for="card in modelCards"
          :key="card.model"
          type="button"
          class="model-card"
          :class="[`tone-${card.tone}`, { active: card.model === selectedModel }]"
          @click="selectModel(card.model, 'pool')"
        >
          <div class="model-card-head">
            <strong class="model-name">{{ card.model }}</strong>
            <span class="model-tone-badge">{{ toneLabel(card.tone) }}</span>
          </div>
          <dl class="model-metrics">
            <div :class="`tone-${card.coverageTone}`"><dt>持票覆盖</dt><dd>{{ card.holders }}<em>/{{ card.eligible }}</em></dd></div>
            <div :class="`tone-${card.marginTone}`"><dt>地板余量</dt><dd>{{ card.margin }}</dd></div>
            <div :class="`tone-${card.degradationTone}`"><dt>当前降智</dt><dd>{{ card.degradation }}</dd></div>
            <div><dt>采集</dt><dd>{{ card.harvest }}<em>张/分</em></dd></div>
          </dl>
          <span class="model-card-note">{{ card.note }}</span>
        </button>
      </div>

      <!-- 与模型无关的全局瓷砖 -->
      <div class="tile-grid compact">
        <button v-for="tile in globalTiles" :key="tile.key" type="button" class="tile" :class="[`tone-${tile.tone}`, { active: section === tile.section && !tile.model }]" @click="goTo(tile.section)">
          <span class="tile-label">{{ tile.label }}</span>
          <strong class="tile-value">{{ tile.value }}<em v-if="tile.suffix">{{ tile.suffix }}</em></strong>
          <span class="tile-note">{{ tile.note }}</span>
        </button>
      </div>
    </section>

    <!-- 结论 -->
    <section class="panel" v-if="advisories.length">
      <div class="advisory-strip">
        <button v-for="(item, index) in advisories" :key="index" type="button" class="advisory-row" :class="`tone-${item.level}`" @click="openAdvisory(item)">
          <span class="advisory-dot" aria-hidden="true"></span>
          <span class="advisory-badge">{{ advisoryLevelLabel(item.level) }}</span>
          <span v-if="item.model" class="advisory-model">{{ item.model }}</span>
          <span class="advisory-title">{{ item.title }}</span>
          <span class="advisory-evidence">{{ item.evidence }}</span>
          <span class="advisory-go" aria-hidden="true">›</span>
        </button>
      </div>
      <p class="advisory-detail" v-if="expandedAdvisory">{{ expandedAdvisory }}</p>
    </section>

    <!-- 详情 -->
    <section class="panel detail">
      <nav class="section-nav" aria-label="维度切换">
        <button v-for="item in sections" :key="item.key" type="button" class="section-tab" :class="{ active: section === item.key }" :aria-current="section === item.key ? 'page' : undefined" @click="selectSection(item.key)">
          {{ item.label }}
        </button>
        <div v-if="modelScoped && models.length > 1" class="model-switch" role="tablist" aria-label="模型切换">
          <button v-for="m in models" :key="m.model" type="button" role="tab" class="model-switch-item" :class="{ active: m.model === selectedModel }" :aria-selected="m.model === selectedModel" @click="selectModel(m.model)">
            {{ m.model }}
          </button>
        </div>
      </nav>

      <!-- 票池 -->
      <div v-if="section === 'pool'" class="detail-body">
        <p class="section-hint">
          <code>{{ selectedModel }}</code> · 好票 = 长度 {{ config.target_length }} 且未过期。每个模型的票、地板、拦截各自独立。
        </p>
        <div class="stat-row">
          <div class="stat"><span>持票</span><strong>{{ current.pool.holders }}</strong><small>新鲜 {{ current.pool.fresh }} · 临期 {{ current.pool.stale }}</small></div>
          <div class="stat"><span>可调度 opt-in</span><strong>{{ current.pool.eligible }}</strong><small>覆盖率 {{ formatPercent(current.pool.coverage_percent, 1) }}</small></div>
          <div class="stat"><span>票已过期</span><strong>{{ current.pool.expired }}</strong><small>看门狗会清理</small></div>
          <div class="stat"><span>从未拿到票</span><strong>{{ current.pool.never }}</strong><small>冷却中，仍在尝试打票</small></div>
          <div class="stat"><span>平均剩余票龄</span><strong>{{ formatDuration(current.pool.avg_remaining_seconds) }}</strong><small>TTL {{ formatDuration(config.ttl_seconds) }}</small></div>
          <div class="stat"><span>平均尝试次数</span><strong>{{ formatNumber(current.pool.avg_attempts) }}</strong><small>打几发中一张</small></div>
        </div>
        <div class="table-wrap" v-if="current.pools.length">
          <table>
            <thead><tr><th>分组</th><th>成员</th><th>可调度</th><th>持票</th><th>新鲜</th><th>地板</th><th>余量</th><th>覆盖率</th><th>近{{ current.degradation.window_hours }}h 降智</th></tr></thead>
            <tbody>
              <tr v-for="item in current.pools" :key="item.group_id" :class="{ 'row-expired': !item.floor_satisfied && item.holders > 0, 'row-warning': item.holders === 0 && item.floor > 0 }">
                <td><strong>{{ item.name }}</strong><div class="account-meta">#{{ item.group_id }}</div></td>
                <td>{{ item.members }}</td>
                <td>{{ item.schedulable }}</td>
                <td><strong>{{ item.holders }}</strong></td>
                <td>{{ item.fresh }}</td>
                <td>{{ item.floor > 0 ? item.floor : '未设' }}</td>
                <td>
                  <span class="mini-badge" :class="item.floor_satisfied ? 'success' : item.holders === 0 ? 'muted' : 'danger'">
                    {{ item.floor > 0 ? (item.holders === 0 ? '未启动' : `${item.floor_margin.toFixed(1)}×`) : '—' }}
                  </span>
                </td>
                <td>{{ formatPercent(item.coverage_percent, 1) }}</td>
                <td>{{ item.requests ? formatPercent(item.degraded_percent) : '-' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <CodexTrendChart :samples="trend" :models="modelNames" caption="持票账号数趋势（进程内采样，重启清空）" />
      </div>

      <!-- 打票 -->
      <div v-else-if="section === 'harvest'" class="detail-body">
        <p class="section-hint"><code>{{ selectedModel }}</code> · {{ current.harvest.note }} 采集预算是所有门控模型共用的一个计数器。</p>
        <div class="stat-row">
          <div class="stat"><span>在手票</span><strong>{{ current.harvest.captured }}</strong><small>覆盖最近 {{ current.harvest.window_minutes }} 分钟</small></div>
          <div class="stat"><span>采集速率</span><strong>{{ formatNumber(current.harvest.capture_per_minute) }}</strong><small>张/分，全模型预算上限 {{ formatNumber(current.harvest.cycle_ceiling_per_minute) }} 发/分</small></div>
          <div class="stat"><span>平均尝试次数</span><strong>{{ formatNumber(current.harvest.avg_attempts) }}</strong><small>打几发中一张好票</small></div>
          <div class="stat"><span>最近一次采到</span><strong>{{ formatRelativeTime(current.harvest.newest_captured_at, nowTick) }}</strong><small>最早 {{ formatRelativeTime(current.harvest.oldest_captured_at, nowTick) }}</small></div>
        </div>
        <TrendChart :points="harvestPoints" caption="在手票的采集时刻分布（按分钟）" />
      </div>

      <!-- 降智 -->
      <div v-else-if="section === 'degradation'" class="detail-body">
        <p class="section-hint">
          <code>{{ selectedModel }}</code> · 降智 = 上游返回了跨系列或更低版本的模型；同系列升版（如 gpt-5.6-sol → gpt-6-sol）不算，与看门狗同一套规则。当前状态看近 {{ current.degradation.recent_hours }} 小时。
        </p>
        <div class="stat-row">
          <div class="stat" :class="degradationTone(current.degradation)">
            <span>当前（近 {{ current.degradation.recent_hours }}h）</span>
            <strong>{{ formatPercent(current.degradation.recent_degraded_percent) }}</strong>
            <small>{{ current.degradation.recent_degraded }} / {{ current.degradation.recent_requests }} 笔</small>
          </div>
          <div class="stat"><span>最近一小时</span><strong>{{ formatPercent(latestHour?.degraded_percent ?? null) }}</strong><small>{{ latestHour ? `${latestHour.degraded} / ${latestHour.requests} 笔` : '窗口内无请求' }}</small></div>
          <div class="stat">
            <span>{{ current.degradation.window_hours }}h 滚动均值</span>
            <strong>{{ formatPercent(current.degradation.degraded_percent) }}</strong>
            <small>{{ current.degradation.degraded }} / {{ current.degradation.requests }} 笔<template v-if="windowDraining(current.degradation)">，仍混着旧数据</template></small>
          </div>
          <div class="stat"><span>同系列升版</span><strong>{{ current.degradation.upgraded }}</strong><small>不计入降智</small></div>
          <div class="stat"><span>未记录上游模型</span><strong>{{ formatPercent(current.degradation.blank_percent, 1) }}</strong><small>会让降智率被低估</small></div>
        </div>

        <div class="pair-list" v-if="(current.degradation.pairs ?? []).length">
          <span class="pair-title">上游实际返回：</span>
          <span v-for="p in current.degradation.pairs ?? []" :key="p.response || '(空)'" class="pair-chip" :class="`verdict-${p.verdict}`">
            {{ p.response || '（未记录）' }} <em>{{ verdictLabel(p.verdict) }}</em> · {{ p.requests.toLocaleString('en-US') }}
          </span>
        </div>

        <TrendChart :points="degradationPoints" caption="逐小时请求量与降智率" :legend="{ bar: '请求数', line: '降智率' }" />
        <div class="lazy-block">
          <button class="secondary-button" type="button" :disabled="dailyLoading" @click="$emit('load-daily')">
            <span v-if="dailyLoading" class="spinner small"></span>
            {{ dailyTrend ? '刷新 7 天趋势' : '加载 7 天趋势' }}
          </button>
          <span class="hint">按天趋势要扫约 280MB 数据页，不进自动刷新，结果长缓存。</span>
        </div>
        <TrendChart v-if="dailyTrend" :points="dailyPoints" :caption="`按天降智率 · ${selectedModel}`" :legend="{ bar: '请求数', line: '降智率' }" />
      </div>

      <!-- 容量 -->
      <div v-else-if="section === 'capacity'" class="detail-body">
        <p class="section-hint">全部门控模型合计。判断要不要补号：额度剩余率下滑、单号并发走高、临近到期，都是补号信号。</p>
        <div class="stat-row">
          <div class="stat"><span>平均 RPM</span><strong>{{ formatNumber(load.rpm) }}</strong><small>峰值 {{ formatNumber(load.peak_rpm) }}（小时均）</small></div>
          <div class="stat"><span>平均并发</span><strong>{{ formatNumber(load.avg_concurrency) }}</strong><small>峰值 {{ formatNumber(load.peak_concurrency) }}</small></div>
          <div class="stat"><span>单号 RPM</span><strong>{{ formatNumber(load.rpm_per_eligible_account, 3) }}</strong><small>{{ load.active_accounts }} 个号近 {{ load.window_hours }}h 有流量</small></div>
          <div class="stat" :class="quotaTone"><span>7 天额度平均剩余</span><strong>{{ formatPercent(quota.avg_7d_remaining_percent, 1) }}</strong><small>{{ quota.accounts_7d_above_80 }} 个号已用 ≥80%</small></div>
          <div class="stat"><span>5h 额度平均已用</span><strong>{{ formatPercent(quota.avg_5h_used_percent, 1) }}</strong><small>{{ quota.accounts_5h_above_80 }} 个号 ≥80%</small></div>
          <div class="stat" :class="quota.expired || quota.expiring_in_7_days ? 'tone-warning' : ''"><span>套餐到期</span><strong>{{ quota.expiring_in_7_days }}</strong><small>7 天内；已过期 {{ quota.expired }}，30 天内 {{ quota.expiring_in_30_days }}</small></div>
        </div>
        <TrendChart :points="quotaPoints" caption="7 天额度剩余率分布（可调度 opt-in 号）" />
        <p class="hint" v-if="quota.snapshot_stale">{{ quota.snapshot_stale }} 个号的额度快照已过期，均值只覆盖有新鲜快照的号。</p>
      </div>

      <!-- 配置 -->
      <div v-else-if="section === 'config'" class="detail-body">
        <p class="section-hint">「已配置」= settings 表里显式配过；「内置默认」= 没有行，回落到代码默认值。整型项填 0 不等于关闭，会被当成未设置。</p>
        <div class="table-wrap">
          <table class="dense">
            <thead><tr><th>配置项</th><th>生效值</th><th>来源</th><th>改动方式</th></tr></thead>
            <tbody>
              <tr v-for="item in config.values" :key="item.key" :class="{ 'row-highlight': item.key === 'openai_codex_ticket_models' }">
                <td><code>{{ item.key.replace('openai_codex_ticket_', '') }}</code></td>
                <td><strong>{{ item.value === '' ? '（空）' : item.value }}</strong></td>
                <td><span class="mini-badge" :class="item.source === 'db' ? 'success' : 'muted'">{{ item.source === 'db' ? '已配置' : '内置默认' }}</span></td>
                <td><span class="mini-badge" :class="item.hot_reload ? 'muted' : 'warning'">{{ item.hot_reload ? '后台热改' : '需重建容器' }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- 账号 -->
      <div v-else class="detail-body">
        <div class="filter-row">
          <label class="search-field"><span>搜索</span><input v-model="search" placeholder="账号 ID、名称、邮箱" /></label>
          <label>
            <span>{{ selectedModel }} 票状态</span>
            <select v-model="ticketFilter">
              <option value="all">全部</option>
              <option value="holders">持票（新鲜+临期）</option>
              <option value="fresh">新鲜票</option>
              <option value="stale">临期票</option>
              <option value="expired">票已过期</option>
              <option value="none">从未拿到票</option>
              <option value="off">未 opt-in</option>
            </select>
          </label>
          <label>
            <span>分组</span>
            <select v-model="groupFilter">
              <option value="all">全部</option>
              <option v-for="item in current.pools" :key="item.group_id" :value="String(item.group_id)">{{ item.name }}</option>
            </select>
          </label>
        </div>
        <div class="table-meta"><span>显示 {{ visibleAccounts.length }} / {{ accounts.length }} 个账号</span></div>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>账号</th><th>分组</th>
                <th v-for="m in modelNames" :key="m" :class="{ 'th-selected': m === selectedModel }">{{ m }} 票</th>
                <th>近{{ current.degradation.window_hours }}h 请求</th><th>降智</th><th>7d 剩余</th><th>5h 已用</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="account in visibleAccounts" :key="account.id">
                <td>
                  <div class="account-name" :title="account.name">{{ account.name }}</div>
                  <div class="account-meta">
                    #{{ account.id }}
                    <span v-if="!account.schedulable" class="mini-badge warning">不可调度</span>
                    <span v-if="account.status !== 'active'" class="mini-badge danger">{{ account.status }}</span>
                  </div>
                </td>
                <td><span v-for="group in account.groups ?? []" :key="group.id" class="mini-badge muted">{{ group.name }}</span></td>
                <td v-for="m in modelNames" :key="m" :class="{ 'td-selected': m === selectedModel }">
                  <template v-if="ticketOf(account, m)">
                    <span class="level-badge" :class="`ticket-${ticketOf(account, m)!.state}`">{{ ticketStateLabel(ticketOf(account, m)!.state) }}</span>
                    <div class="account-meta" v-if="ticketOf(account, m)!.remaining_seconds > 0">剩 {{ formatDuration(ticketOf(account, m)!.remaining_seconds) }}</div>
                  </template>
                </td>
                <td>
                  <template v-if="account.requests">
                    {{ account.requests }}
                    <div class="account-meta" v-if="(account.usage ?? []).length > 1">
                      <span v-for="u in account.usage ?? []" :key="u.model">{{ shortModel(u.model) }} {{ u.requests }} </span>
                    </div>
                  </template>
                  <template v-else>-</template>
                </td>
                <td>
                  <span v-if="account.requests" :class="{ 'danger-text': account.degraded / account.requests > 0.05 }">
                    {{ account.degraded }}（{{ formatPercent(100 * account.degraded / account.requests, 1) }}）
                  </span>
                  <span v-else>-</span>
                </td>
                <td>{{ formatPercent(account.quota_7d_remaining_percent, 0) }}</td>
                <td>{{ formatPercent(account.quota_5h_used_percent, 0) }}</td>
              </tr>
              <tr v-if="visibleAccounts.length === 0"><td :colspan="6 + modelNames.length" class="empty-state">没有符合筛选条件的账号</td></tr>
            </tbody>
          </table>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type {
  CodexAccountTicket, CodexAdvisory, CodexDailyTrend, CodexDegradationSummary,
  CodexModelView, CodexOverview, CodexPoolBreakdown, CodexTicketAccount, TicketFilter
} from '../types'
import {
  advisoryLevelLabel, formatDayLabel, formatDuration, formatHourLabel, formatMinuteLabel,
  formatNumber, formatPercent, formatRelativeTime, ticketStateLabel
} from '../utils'
import CodexTrendChart from './CodexTrendChart.vue'
import TrendChart, { type ChartPoint } from './TrendChart.vue'

type SectionKey = 'pool' | 'harvest' | 'degradation' | 'capacity' | 'config' | 'accounts'
type Tone = 'danger' | 'warning' | 'success' | 'neutral'

const props = defineProps<{
  overview: CodexOverview | null
  dailyTrend: CodexDailyTrend | null
  dailyLoading: boolean
  error: string
}>()
defineEmits<{ 'load-daily': [] }>()

const sections: { key: SectionKey; label: string }[] = [
  { key: 'pool', label: '票池' }, { key: 'harvest', label: '打票' }, { key: 'degradation', label: '降智' },
  { key: 'capacity', label: '容量' }, { key: 'config', label: '配置' }, { key: 'accounts', label: '账号' }
]

const section = ref<SectionKey>('pool')
const selectedModel = ref('')
const pinned = ref(false)
const search = ref('')
const ticketFilter = ref<TicketFilter>('all')
const groupFilter = ref('all')
const nowTick = ref(Date.now())
let clockTimer: ReturnType<typeof setInterval> | null = null
onMounted(() => { clockTimer = setInterval(() => { nowTick.value = Date.now() }, 30_000) })
onBeforeUnmount(() => { if (clockTimer) clearInterval(clockTimer) })

const emptyModel = (name: string): CodexModelView => ({
  model: name,
  pool: { eligible: 0, holders: 0, fresh: 0, stale: 0, expired: 0, never: 0, coverage_percent: 0, fresh_percent: 0, avg_attempts: 0, avg_remaining_seconds: 0 },
  pools: [],
  harvest: { window_minutes: 60, captured: 0, avg_attempts: 0, oldest_captured_at: null, newest_captured_at: null, capture_per_minute: 0, cycle_ceiling_per_minute: 0, buckets: [], note: '' },
  degradation: { window_hours: 24, requests: 0, degraded: 0, upgraded: 0, unknown: 0, blank: 0, degraded_percent: 0, blank_percent: 0, recent_hours: 3, recent_requests: 0, recent_degraded: 0, recent_degraded_percent: 0, pairs: [], buckets: [] }
})

const config = computed(() => props.overview?.config ?? {
  enabled: false, fail_closed: false, floor_min_holders: 0, fresh_min_seconds: 300, target_length: 292, ttl_seconds: 3600,
  harvest_probe_interval_seconds: 6, harvest_backoff_max_seconds: 3600, harvest_concurrency: 4, harvest_max_probes_per_cycle: 16,
  prefer_holders: true, gated_models: [], gated_models_source: 'default' as const, values: []
})
const population = computed(() => props.overview?.population ?? { oauth_total: 0, opt_in: 0, not_opt_in: 0, schedulable: 0, eligible: 0 })
const models = computed<CodexModelView[]>(() => props.overview?.models ?? [])
const modelNames = computed(() => models.value.map((m) => m.model))
const load = computed(() => props.overview?.load ?? { window_hours: 24, requests: 0, rpm: 0, avg_concurrency: 0, peak_rpm: 0, peak_concurrency: 0, avg_duration_ms: 0, active_accounts: 0, rpm_per_eligible_account: 0 })
const quota = computed(() => props.overview?.quota ?? { avg_5h_used_percent: null, avg_7d_used_percent: null, avg_7d_remaining_percent: null, accounts_7d_above_80: 0, accounts_5h_above_80: 0, snapshot_stale: 0, histogram_7d: [], expiring_in_7_days: 0, expiring_in_30_days: 0, expired: 0 })
const advisories = computed(() => props.overview?.advisories ?? [])
const trend = computed(() => props.overview?.trend ?? [])
const accounts = computed(() => props.overview?.accounts ?? [])

// Go 的空切片会序列化成 null，这里归一化成数组，模板里不用到处判空。
type ModelViewSafe = Omit<CodexModelView, 'pools'> & { pools: CodexPoolBreakdown[] }
const current = computed<ModelViewSafe>(() => {
  const found = models.value.find((m) => m.model === selectedModel.value) ?? models.value[0] ?? emptyModel(selectedModel.value || '—')
  return { ...found, pools: found.pools ?? [] }
})
const modelScoped = computed(() => section.value === 'pool' || section.value === 'harvest' || section.value === 'degradation' || section.value === 'accounts')

// ---------- 判级 ----------

const toneRank: Record<Tone, number> = { danger: 0, warning: 1, neutral: 2, success: 3 }
const worst = (...tones: Tone[]): Tone => tones.reduce((a, b) => (toneRank[a] <= toneRank[b] ? a : b), 'success')
function toneLabel(tone: Tone): string { return { danger: '需处理', warning: '注意', neutral: '—', success: '健康' }[tone] }

function coverageTone(m: CodexModelView): Tone {
  if (m.pool.eligible === 0) return 'neutral'
  if (m.pool.coverage_percent < 20) return 'danger'
  if (m.pool.coverage_percent < 40) return 'warning'
  return 'success'
}
function currentDegradation(d: CodexDegradationSummary): number {
  return d.recent_requests >= 50 ? d.recent_degraded_percent : d.degraded_percent
}
function degradationTone(d: CodexDegradationSummary): string {
  if (d.requests === 0) return ''
  const v = currentDegradation(d)
  return v >= 10 ? 'tone-danger' : v >= 2 ? 'tone-warning' : 'tone-success'
}
function windowDraining(d: CodexDegradationSummary): boolean {
  return d.recent_requests >= 50 && d.degraded_percent - d.recent_degraded_percent >= 5
}
function thinnestPool(m: CodexModelView) {
  const pools = (m.pools ?? []).filter((p) => p.floor > 0)
  if (!pools.length) return null
  return pools.reduce((w, p) => (p.floor_margin < w.floor_margin ? p : w))
}
function marginTone(m: CodexModelView): Tone {
  const thin = thinnestPool(m)
  if (!thin || !config.value.fail_closed) return 'neutral'
  if (thin.holders === 0) return 'neutral' // 尚未启动，不是跌破
  if (!thin.floor_satisfied) return 'danger'
  return thin.floor_margin < 1.5 ? 'warning' : 'success'
}

const modelCards = computed(() => models.value.map((m) => {
  const thin = thinnestPool(m)
  const dTone = (degradationTone(m.degradation).replace('tone-', '') || 'neutral') as Tone
  const cTone = coverageTone(m)
  const mTone = marginTone(m)
  const harvestTone: Tone = m.pool.eligible > 0 && m.harvest.captured === 0 ? 'warning' : 'neutral'
  const note = thin
    ? thin.holders === 0 ? `${thin.name} 尚无持票号，拦截未启动` : `最薄 ${thin.name} ${thin.holders}/${thin.floor}`
    : '无分池数据'
  return {
    model: m.model,
    holders: m.pool.holders, eligible: m.pool.eligible,
    margin: thin ? (thin.holders === 0 ? '未启动' : `${thin.floor_margin.toFixed(1)}×`) : '—',
    degradation: m.degradation.requests ? formatPercent(currentDegradation(m.degradation)) : '无流量',
    harvest: formatNumber(m.harvest.capture_per_minute),
    coverageTone: cTone, marginTone: mTone, degradationTone: dTone,
    tone: worst(cTone, mTone, dTone, harvestTone),
    note
  }
}))

const quotaTone = computed(() => {
  const v = quota.value.avg_7d_remaining_percent
  if (v === null) return ''
  return v < 20 ? 'tone-danger' : v < 40 ? 'tone-warning' : 'tone-success'
})

const globalTiles = computed(() => {
  const totalRequests = models.value.reduce((n, m) => n + m.degradation.requests, 0)
  const expiryTone: Tone = quota.value.expired || quota.value.expiring_in_7_days ? 'warning' : 'neutral'
  return [
    { key: 'requests', section: 'capacity' as SectionKey, model: '', label: `近 ${load.value.window_hours}h 门控请求`, value: totalRequests.toLocaleString('en-US'), suffix: '', note: `${models.value.length} 个模型合计 · ${load.value.active_accounts} 个号有流量`, tone: 'neutral' as Tone },
    { key: 'concurrency', section: 'capacity' as SectionKey, model: '', label: '平均并发', value: formatNumber(load.value.avg_concurrency), suffix: '', note: `RPM ${formatNumber(load.value.rpm)} · 峰值并发 ${formatNumber(load.value.peak_concurrency)}`, tone: 'neutral' as Tone },
    { key: 'quota', section: 'capacity' as SectionKey, model: '', label: '7 天额度平均剩余', value: formatPercent(quota.value.avg_7d_remaining_percent, 1), suffix: '', note: `${quota.value.accounts_7d_above_80} 个号已用 ≥80%`, tone: (quotaTone.value.replace('tone-', '') || 'neutral') as Tone },
    { key: 'expiry', section: 'capacity' as SectionKey, model: '', label: '套餐到期', value: String(quota.value.expiring_in_7_days), suffix: ' / 7天', note: `已过期 ${quota.value.expired} · 30 天内 ${quota.value.expiring_in_30_days}`, tone: expiryTone }
  ]
})

// ---------- 导航 ----------

function goTo(key: SectionKey) { section.value = key; pinned.value = true }
function selectSection(key: SectionKey) { section.value = key; pinned.value = true }
function selectModel(name: string, key?: SectionKey) {
  selectedModel.value = name
  if (key) section.value = key
  pinned.value = true
}
function sectionForCategory(category: string): SectionKey {
  return ({ 票池: 'pool', 打票: 'harvest', 降智: 'degradation', 容量: 'capacity', 续费: 'capacity', 配置: 'config', 可观测性: 'degradation' } as Record<string, SectionKey>)[category] ?? 'pool'
}
function openAdvisory(item: CodexAdvisory) {
  if (item.model) selectedModel.value = item.model
  section.value = sectionForCategory(item.category)
  pinned.value = true
}
const expandedAdvisory = computed(() => {
  const match = advisories.value.find((a) => sectionForCategory(a.category) === section.value && (!a.model || a.model === selectedModel.value))
  return match?.detail ?? ''
})

// 首屏：落在最严重结论所属的模型与维度；用户点过之后不再自动跳。
watch(() => props.overview, (value) => {
  if (!value) return
  const names = (value.models ?? []).map((m) => m.model)
  if (!names.includes(selectedModel.value)) selectedModel.value = names[0] ?? ''
  if (pinned.value) return
  const urgent = (value.advisories ?? []).find((a) => a.level === 'critical' || a.level === 'warning')
  if (urgent) {
    if (urgent.model) selectedModel.value = urgent.model
    section.value = sectionForCategory(urgent.category)
  }
}, { immediate: true })

// ---------- 图表数据 ----------

const latestHour = computed(() => {
  const buckets = current.value.degradation.buckets ?? []
  return buckets.length ? buckets[buckets.length - 1] : null
})
const degradationPoints = computed<ChartPoint[]>(() =>
  (current.value.degradation.buckets ?? []).map((b) => ({ label: formatHourLabel(b.hour), value: b.requests, secondary: b.degraded_percent }))
)
const harvestPoints = computed<ChartPoint[]>(() =>
  (current.value.harvest.buckets ?? []).map((b) => ({ label: formatMinuteLabel(b.minute), value: b.captured }))
)
const dailyPoints = computed<ChartPoint[]>(() =>
  (props.dailyTrend?.buckets ?? []).filter((b) => b.model === selectedModel.value).map((b) => ({ label: formatDayLabel(b.day), value: b.requests, secondary: b.degraded_percent }))
)
const quotaPoints = computed<ChartPoint[]>(() =>
  (quota.value.histogram_7d ?? []).map((b) => ({ label: b.label, value: b.count }))
)

// ---------- 账号 ----------

function ticketOf(account: CodexTicketAccount, m: string): CodexAccountTicket | undefined {
  return (account.tickets ?? []).find((t) => t.model === m)
}
function shortModel(m: string): string { return m.replace(/^gpt-/, '') }
function verdictLabel(v: string): string {
  return ({ same: '相同', upgraded: '升版', degraded: '降智', unknown: '未识别', blank: '未记录' } as Record<string, string>)[v] ?? v
}
const visibleAccounts = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return accounts.value.filter((account) => {
    if (needle && !`${account.id} ${account.name} ${account.email}`.toLowerCase().includes(needle)) return false
    const state = ticketOf(account, selectedModel.value)?.state ?? 'off'
    if (ticketFilter.value === 'holders') {
      if (state !== 'fresh' && state !== 'stale') return false
    } else if (ticketFilter.value !== 'all' && state !== ticketFilter.value) {
      return false
    }
    if (groupFilter.value !== 'all' && !(account.groups ?? []).some((g) => String(g.id) === groupFilter.value)) return false
    return true
  })
})
</script>
