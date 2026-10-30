import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getById,
  getBatchTodayStats,
  getManagementCapabilities,
  getUpstreamBillingProbeSettings,
  getUpstreamBillingRatesWithEtag,
  probeUpstreamBilling,
  getAllProxies,
  getAllGroups,
  refreshCredentials,
  showError,
  showWarning,
  listQualityPlans,
  listResults
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getById: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getManagementCapabilities: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getUpstreamBillingRatesWithEtag: vi.fn(),
  probeUpstreamBilling: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  refreshCredentials: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
  listQualityPlans: vi.fn(),
  listResults: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getManagementCapabilities,
      list: listAccounts,
      getById,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      getUpstreamBillingRatesWithEtag,
      probeUpstreamBilling,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn(),
      refreshCredentials
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups },
    scheduledTests: { listResults }
  }
}))

vi.mock('@/api/admin/accountQuality', () => ({ listQualityPlans }))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showWarning, showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', isSimpleMode: false })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const DataTableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-account-name="row.name">
        <slot name="cell-groups" :row="row" />
        <slot name="cell-capacity" :row="row" />
        <slot name="cell-upstream_billing_rate" :row="row" />
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `
})

const AccountGroupsCellStub = defineComponent({
  props: { groups: { type: Array, default: () => [] } },
  template: '<span data-test="account-groups">{{ groups.map(group => group.name).join(",") }}</span>'
})

const EditAccountModalStub = defineComponent({
  props: { show: Boolean, account: { type: Object, default: null } },
  template: '<div data-test="edit-account">{{ show ? account?.name : "" }}</div>'
})

const AccountTestModalStub = defineComponent({
  props: { show: Boolean, account: { type: Object, default: null } },
  template: '<div data-test="test-account">{{ show ? account?.name : "" }}</div>'
})

const AccountStatsModalStub = defineComponent({
  props: { show: Boolean, account: { type: Object, default: null } },
  template: '<div data-test="stats-account">{{ show ? account?.name : "" }}</div>'
})

function mountView(stubActionMenu = true) {
  return mount(AccountsView, {
    attachTo: document.body,
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: DataTableStub,
        AccountTableActions: { template: '<div><slot name="after" /></div>' },
        AccountTableFilters: true,
        AccountBulkActionsBar: true,
        Pagination: true,
        ConfirmDialog: true,
        AccountActionMenu: stubActionMenu,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: AccountTestModalStub,
        AccountStatsModal: AccountStatsModalStub,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: EditAccountModalStub,
        BulkEditAccountModal: true,
        PlatformTypeBadge: true,
        AccountCapacityCell: false,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: AccountGroupsCellStub,
        AccountUsageCell: true,
        UpstreamBillingRateCell: true,
        HelpTooltip: true,
        Icon: true,
        Teleport: stubActionMenu
      }
    }
  })
}

const row = (id: number) => ({
  id,
  name: `account ${id}`,
  platform: 'openai',
  type: 'oauth',
  status: 'active',
  schedulable: true,
  concurrency: 2,
  priority: 1,
  group_ids: [7],
  extra: {},
  credentials: {}
})

const candyPlan = (accountID: number) => ({
  id: 100 + accountID,
  account_id: accountID,
  model_id: 'gpt-6-astra',
  cron_expression: '@random 10-15',
  enabled: true,
  max_results: 100,
  auto_recover: false,
  pelican_config: { question_kind: 'candy', model_ids: ['gpt-6-astra', 'gpt-6.1-sol'] }
})

const requestedPlanIDs = () => listResults.mock.calls.map(call => call[0])

describe('admin AccountsView quality strip', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset().mockImplementation(async (page: number) => (
      page === 2
        ? { items: [row(2)], total: 2, page: 2, page_size: 1, pages: 2 }
        : { items: [row(1)], total: 2, page: 1, page_size: 1, pages: 2 }
    ))
    listWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: 'strip-etag', data: null })
    getById.mockReset()
    getBatchTodayStats.mockReset().mockResolvedValue({ stats: {} })
    getManagementCapabilities.mockReset().mockResolvedValue({ concurrency_upgrade_enabled: false })
    getUpstreamBillingProbeSettings.mockReset().mockResolvedValue({ enabled: true })
    getAllProxies.mockReset().mockResolvedValue([])
    getAllGroups.mockReset().mockResolvedValue([{ id: 7, name: 'codex', platform: 'openai' }])
    getUpstreamBillingRatesWithEtag.mockReset().mockResolvedValue({ notModified: true, data: null })
    probeUpstreamBilling.mockReset()
    refreshCredentials.mockReset()
    showError.mockReset()
    showWarning.mockReset()
    listQualityPlans.mockReset().mockResolvedValue([candyPlan(1), candyPlan(2)])
    listResults.mockReset().mockResolvedValue([])
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('loads strips for the accounts shown after a page change', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(requestedPlanIDs()).toEqual([101])

    wrapper.findComponent({ name: 'Pagination' }).vm.$emit('update:page', 2)
    await flushPromises()

    expect(listAccounts).toHaveBeenLastCalledWith(2, expect.any(Number), expect.anything(), expect.anything())
    expect(requestedPlanIDs()).toContain(102)
    wrapper.unmount()
  })

  it('loads strips for the accounts shown after a filter change', async () => {
    const wrapper = mountView()
    await flushPromises()
    listAccounts.mockResolvedValue({ items: [row(2)], total: 1, page: 1, page_size: 1, pages: 1 })

    wrapper.findComponent({ name: 'AccountTableFilters' }).vm.$emit('change')
    await new Promise(resolve => setTimeout(resolve, 400))
    await flushPromises()

    expect(requestedPlanIDs()).toContain(102)
    wrapper.unmount()
  })
})
