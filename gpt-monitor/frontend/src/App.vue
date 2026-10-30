<template>
  <main v-if="checking" class="centered-page">
    <div class="loading-card">
      <span class="spinner"></span>
      <span>正在检查登录状态…</span>
    </div>
  </main>

  <LoginView
    v-else-if="!session.authenticated"
    :loading="loggingIn"
    :error="loginError"
    @submit="handleLogin"
  />

  <AppShell
    v-else
    :tabs="tabs"
    :active="activeTab"
    :updated-at="updatedAt"
    :updated-label="activeTab === 'renewal' ? '快照时间' : '生成时间'"
    :loading="activeLoading"
    :username="session.username"
    @refresh="refreshActive"
    @logout="handleLogout"
    @select="selectTab"
  >
    <DashboardView
      v-if="activeTab === 'renewal'"
      :snapshot="snapshot"
      :loading="loadingSnapshot"
      :error="snapshotError"
    />
    <IQView
      v-else-if="activeTab === 'iq'"
      :overview="iqOverview"
      :error="iqError"
      @refresh="loadIQOverview"
      @unauthorized="handleUnauthorized"
    />
    <CodexTicketView
      v-else
      :overview="codexOverview"
      :daily-trend="dailyTrend"
      :daily-loading="loadingDaily"
      :error="codexError"
      @load-daily="loadDailyTrend"
    />
  </AppShell>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, APIError } from './api'
import AppShell from './components/AppShell.vue'
import CodexTicketView from './components/CodexTicketView.vue'
import DashboardView from './components/DashboardView.vue'
import IQView from './components/IQView.vue'
import LoginView from './components/LoginView.vue'
import type { CodexDailyTrend, CodexOverview, IQOverview, Session, Snapshot } from './types'
import { formatDateTime } from './utils'

type TabKey = 'renewal' | 'codex' | 'iq'

const checking = ref(true)
const loggingIn = ref(false)
const loadingSnapshot = ref(false)
const loadingCodex = ref(false)
const loadingDaily = ref(false)
const loadingIQ = ref(false)
const loginError = ref('')
const snapshotError = ref('')
const codexError = ref('')
const iqError = ref('')
const session = ref<Session>({ authenticated: false })
const snapshot = ref<Snapshot | null>(null)
const codexOverview = ref<CodexOverview | null>(null)
const dailyTrend = ref<CodexDailyTrend | null>(null)
const iqOverview = ref<IQOverview | null>(null)
const codexAvailable = ref(false)
const iqAvailable = ref(false)
const activeTab = ref<TabKey>('renewal')
let refreshTimer: ReturnType<typeof setInterval> | null = null

const tabs = computed(() => {
  const items = [{ key: 'renewal', label: '续费看板' }]
  if (codexAvailable.value) items.push({ key: 'codex', label: '292 打票看板' })
  if (iqAvailable.value) items.push({ key: 'iq', label: '智商检测' })
  return items
})

const activeLoading = computed(() =>
  ({ renewal: loadingSnapshot.value, codex: loadingCodex.value, iq: loadingIQ.value })[activeTab.value]
)

const updatedAt = computed(() =>
  formatDateTime(({
    renewal: snapshot.value?.generated_at,
    codex: codexOverview.value?.generated_at,
    iq: iqOverview.value?.generated_at
  })[activeTab.value])
)

onMounted(async () => {
  try {
    session.value = await api.session()
    if (session.value.authenticated) {
      await detectFeatures()
      await loadActive()
    }
  } catch (error) {
    snapshotError.value = messageFromError(error)
  } finally {
    checking.value = false
    scheduleRefresh()
  }
})

onBeforeUnmount(stopRefresh)

async function detectFeatures() {
  try {
    const features = await api.features()
    codexAvailable.value = features.codex_ticket
    iqAvailable.value = Boolean(features.iq)
  } catch {
    codexAvailable.value = false
    iqAvailable.value = false
  }
}

async function handleLogin(username: string, password: string) {
  loggingIn.value = true
  loginError.value = ''
  try {
    session.value = await api.login(username, password)
    await detectFeatures()
    await loadActive()
    scheduleRefresh()
  } catch (error) {
    loginError.value = messageFromError(error)
  } finally {
    loggingIn.value = false
  }
}

async function handleLogout() {
  try {
    await api.logout()
  } finally {
    session.value = { authenticated: false }
    snapshot.value = null
    codexOverview.value = null
    dailyTrend.value = null
    iqOverview.value = null
    stopRefresh()
  }
}

function selectTab(key: string) {
  if (key !== 'renewal' && key !== 'codex' && key !== 'iq') return
  activeTab.value = key
  void loadActive()
}

function loadActive(): Promise<void> {
  return ({ renewal: loadSnapshot, codex: loadCodexOverview, iq: loadIQOverview })[activeTab.value]()
}

async function loadIQOverview() {
  if (!session.value.authenticated) return
  loadingIQ.value = true
  iqError.value = ''
  try {
    iqOverview.value = await api.iqOverview()
  } catch (error) {
    if (!handleAuthError(error)) iqError.value = messageFromError(error)
  } finally {
    loadingIQ.value = false
  }
}

function handleUnauthorized() {
  handleAuthError(new APIError(401, '未登录'))
}

function refreshActive() {
  void loadActive()
}

async function loadSnapshot() {
  if (!session.value.authenticated) return
  loadingSnapshot.value = true
  snapshotError.value = ''
  try {
    snapshot.value = await api.snapshot()
  } catch (error) {
    if (!handleAuthError(error)) snapshotError.value = messageFromError(error)
  } finally {
    loadingSnapshot.value = false
  }
}

async function loadCodexOverview() {
  if (!session.value.authenticated) return
  loadingCodex.value = true
  codexError.value = ''
  try {
    codexOverview.value = await api.codexOverview()
  } catch (error) {
    if (!handleAuthError(error)) codexError.value = messageFromError(error)
  } finally {
    loadingCodex.value = false
  }
}

// 7 天趋势对 usage_logs 的扫描代价高，只在用户明确点击时才拉。
async function loadDailyTrend() {
  if (!session.value.authenticated) return
  loadingDaily.value = true
  try {
    dailyTrend.value = await api.codexDailyTrend()
  } catch (error) {
    if (!handleAuthError(error)) codexError.value = messageFromError(error)
  } finally {
    loadingDaily.value = false
  }
}

function handleAuthError(error: unknown): boolean {
  if (error instanceof APIError && error.status === 401) {
    session.value = { authenticated: false }
    snapshot.value = null
    codexOverview.value = null
    iqOverview.value = null
    stopRefresh()
    return true
  }
  return false
}

function scheduleRefresh() {
  if (refreshTimer) return
  refreshTimer = setInterval(() => void loadActive(), 5 * 60 * 1000)
}

function stopRefresh() {
  if (!refreshTimer) return
  clearInterval(refreshTimer)
  refreshTimer = null
}

function messageFromError(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
</script>
