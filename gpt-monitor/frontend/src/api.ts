import type {
  CodexDailyTrend, CodexOverview, Features, IQAccountsView, IQBoard, IQBoardWindow, IQCardView, IQGroupConfig, IQGroupsView,
  IQOverview, IQProbesView, IQProvider, IQProviderInput, IQProvidersView, IQProviderTestResult, IQScheduleConfig, IQScope,
  IQSettingsView, IQWindow, Session, Snapshot
} from './types'

export class APIError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'APIError'
    this.status = status
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    credentials: 'include',
    ...options,
    headers: {
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...options.headers
    }
  })
  if (response.status === 204) return undefined as T
  const contentType = response.headers.get('content-type') ?? ''
  const payload = contentType.includes('application/json') ? await response.json() : null
  if (!response.ok) {
    throw new APIError(response.status, payload?.error || `请求失败 (${response.status})`)
  }
  return payload as T
}

export const api = {
  session: () => request<Session>('/api/session'),
  login: (username: string, password: string) =>
    request<Session>('/api/login', {
      method: 'POST',
      body: JSON.stringify({ username, password })
    }),
  logout: () => request<void>('/api/logout', { method: 'POST' }),
  snapshot: () => request<Snapshot>('/api/v1/gpt-pro/accounts'),
  features: () => request<Features>('/api/v1/features'),
  codexOverview: () => request<CodexOverview>('/api/v1/codex-ticket/overview'),
  codexDailyTrend: () => request<CodexDailyTrend>('/api/v1/codex-ticket/daily-trend'),
  iqOverview: () => request<IQOverview>('/api/v1/iq/overview'),
  iqBoard: (window: IQBoardWindow, scope: IQScope) =>
    request<IQBoard>(`/api/v1/iq/board?window=${encodeURIComponent(window)}&scope=${encodeURIComponent(scope)}`),
  iqCard: (source: string, model: string, scope: IQScope) =>
    request<IQCardView>(`/api/v1/iq/card?${new URLSearchParams({ source, model, scope }).toString()}`),
  iqGroups: (window: IQWindow) => request<IQGroupsView>(`/api/v1/iq/groups?window=${encodeURIComponent(window)}`),
  iqAccounts: (window: IQWindow, groupId = 0) =>
    request<IQAccountsView>(`/api/v1/iq/accounts?window=${encodeURIComponent(window)}${groupId ? `&group_id=${groupId}` : ''}`),
  iqProbes: (query: { source?: string; accountId?: number; model?: string; outcome?: string; scope?: IQScope; limit?: number }) => {
    const params = new URLSearchParams()
    if (query.source) params.set('source', query.source)
    if (query.accountId) params.set('account_id', String(query.accountId))
    if (query.model) params.set('model', query.model)
    if (query.outcome) params.set('outcome', query.outcome)
    if (query.scope && query.scope !== 'all') params.set('scope', query.scope)
    if (query.limit) params.set('limit', String(query.limit))
    return request<IQProbesView>(`/api/v1/iq/probes?${params.toString()}`)
  },
  iqRun: (body: { account_id?: number; group_id?: number; model?: string } = {}) =>
    request<{ run_id: string; kind: string; targets: number }>('/api/v1/iq/run', {
      method: 'POST',
      body: JSON.stringify(body)
    }),
  iqUpdateSettings: (body: { models?: string; schedule?: Partial<IQScheduleConfig>; groups?: Partial<IQGroupConfig> }) =>
    request<IQSettingsView>('/api/v1/iq/settings', { method: 'PUT', body: JSON.stringify(body) }),
  iqProviders: () => request<IQProvidersView>('/api/v1/iq/providers'),
  iqCreateProvider: (input: IQProviderInput) =>
    request<IQProvider>('/api/v1/iq/providers', { method: 'POST', body: JSON.stringify(input) }),
  iqUpdateProvider: (id: number, input: IQProviderInput) =>
    request<IQProvider>(`/api/v1/iq/providers/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  iqDeleteProvider: (id: number, keepProbes: boolean) =>
    request<void>(`/api/v1/iq/providers/${id}?keep_probes=${keepProbes}`, { method: 'DELETE' }),
  iqTestProvider: (body: { provider_id?: number; base_url: string; api_format: string; api_key: string; models: string }) =>
    request<IQProviderTestResult>('/api/v1/iq/providers/test', { method: 'POST', body: JSON.stringify(body) }),
  iqRunProvider: (id: number, model?: string) =>
    request<{ kind: string; targets: number }>(`/api/v1/iq/providers/${id}/run`, {
      method: 'POST',
      body: JSON.stringify(model ? { model } : {})
    })
}
