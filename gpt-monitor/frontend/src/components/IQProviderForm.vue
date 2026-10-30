<template>
  <form class="iq-provider-form" @submit.prevent="submit" aria-labelledby="iq-provider-form-title">
    <header class="iq-schedule-head">
      <div>
        <h3 id="iq-provider-form-title">{{ provider ? `编辑：${provider.name}` : '添加第三方供应商' }}</h3>
        <p class="iq-schedule-sub">任意 OpenAI 兼容接口。用和号池同一道题、同一套判分，结果在「对比」里并排看。</p>
      </div>
      <button class="ghost-button" type="button" @click="$emit('cancel')">取消</button>
    </header>

    <div class="iq-provider-grid">
      <label class="search-field">
        <span>名称</span>
        <input v-model.trim="form.name" maxlength="40" placeholder="例如：某某中转" required />
      </label>
      <label class="search-field">
        <span>接口格式</span>
        <select v-model="form.api_format">
          <option value="chat_completions">Chat Completions（/chat/completions）</option>
          <option value="responses">Responses（/responses）</option>
        </select>
      </label>
      <label class="search-field wide">
        <span>Base URL</span>
        <input v-model.trim="form.base_url" placeholder="https://api.example.com/v1" inputmode="url" autocomplete="off" required />
        <small class="iq-field-hint">
          实际发题地址：<code>{{ endpointPreview || '—' }}</code>
          <template v-if="isHttp"> · <span class="warning-text">http 明文传输，API Key 可能被截获</span></template>
        </small>
      </label>
      <label class="search-field wide">
        <span>API Key{{ provider ? `（当前 ${provider.api_key_hint}，留空不修改）` : '' }}</span>
        <input v-model.trim="form.api_key" type="password" autocomplete="new-password" spellcheck="false"
          :placeholder="provider ? '留空保持原来的 Key' : 'sk-...'" :required="!provider" />
        <small v-if="hostChanged" class="iq-field-hint warning-text">改了 Base URL 的主机，需要重新填写 API Key</small>
      </label>
      <label class="search-field wide">
        <span>测试模型（模型@推理强度，逗号分隔，最多 {{ limits.max_models }} 个）</span>
        <input v-model="form.models" placeholder="gpt-6-astra@xhigh, gpt-5.6-sol@max" required />
        <small class="iq-field-hint">
          <button v-if="defaultModels" class="text-button" type="button" @click="form.models = defaultModels">填入号池当前的测试模型</button>
          模型名和推理强度都一样，才能和号池逐行对比。
        </small>
      </label>
    </div>

    <fieldset class="iq-field">
      <legend>检测周期（独立于号池）</legend>
      <div class="iq-inline">
        <label class="iq-switch">
          <input v-model="form.enabled" type="checkbox" />
          <span class="iq-switch-track" aria-hidden="true"><span class="iq-switch-thumb"></span></span>
          <span class="iq-switch-label">{{ form.enabled ? '定时检测开启' : '定时检测关闭' }}</span>
        </label>
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
        <label>
          <span>每个模型每轮测</span>
          <span class="iq-input-unit"><input v-model.number="form.samples_per_model" type="number" min="1" :max="limits.max_samples_per_model" step="1" inputmode="numeric" /><em>次</em></span>
        </label>
      </div>
      <p class="iq-field-hint">
        每轮 {{ modelCount }} 个模型 × {{ form.samples_per_model || 0 }} 次 = <strong>{{ perRound }}</strong> 题，
        约每天 <strong>{{ perDay }}</strong> 题（会消耗这个供应商的额度）。锚点按北京时间对齐周期的整数倍，和号池同一套规则。
      </p>
    </fieldset>

    <p v-if="validationError" class="form-error" role="alert">{{ validationError }}</p>
    <div v-if="testResult" class="iq-test-result" :class="testResult.ok ? (testResult.missing?.length ? 'warn' : 'ok') : 'fail'" role="status">
      <strong>{{ testResult.ok ? '连接成功' : '连接失败' }}</strong>
      <span>{{ testResult.message }}</span>
      <small v-if="testResult.ok">耗时 {{ testResult.latency_ms }}ms<template v-if="testResult.sample?.length"> · 部分模型：{{ testResult.sample.join('、') }}</template></small>
    </div>

    <footer class="iq-settings-actions">
      <button class="secondary-button" type="button" :disabled="testing || saving || !canTest" @click="test">
        <span v-if="testing" class="spinner small"></span>测试连接
      </button>
      <button class="primary-button" type="submit" :disabled="saving || Boolean(validationError)">
        <span v-if="saving" class="spinner small"></span>{{ provider ? '保存修改' : '添加' }}
      </button>
    </footer>
  </form>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '../api'
import type { IQProvider, IQProviderInput, IQProvidersView, IQProviderTestResult } from '../types'

const props = defineProps<{
  provider: IQProvider | null
  limits: IQProvidersView['limits']
  defaultModels: string
  saving: boolean
}>()
const emit = defineEmits<{ save: [input: IQProviderInput]; cancel: [] }>()

const EFFORTS = new Set(['', 'none', 'low', 'medium', 'high', 'xhigh', 'max'])

function initial(): IQProviderInput {
  const p = props.provider
  if (p) {
    return {
      name: p.name, base_url: p.base_url, api_format: p.api_format, api_key: '',
      models: (p.models ?? []).map((m) => m.label).join(', '), ...p.schedule
    }
  }
  return {
    name: '', base_url: '', api_format: 'chat_completions', api_key: '', models: props.defaultModels,
    enabled: true, interval_seconds: 900, jitter_seconds: 60, samples_per_model: 1
  }
}

const form = reactive<IQProviderInput>(initial())
const intervalUnit = ref<'60' | '3600'>(form.interval_seconds % 3600 === 0 ? '3600' : '60')
const intervalValue = ref(form.interval_seconds / Number(intervalUnit.value))
const jitterUnit = ref<'1' | '60'>(form.jitter_seconds >= 60 && form.jitter_seconds % 60 === 0 ? '60' : '1')
const jitterValue = ref(form.jitter_seconds / Number(jitterUnit.value))
watch([intervalValue, intervalUnit], () => { form.interval_seconds = Math.round(Number(intervalValue.value) * Number(intervalUnit.value)) })
watch([jitterValue, jitterUnit], () => { form.jitter_seconds = Math.round(Number(jitterValue.value) * Number(jitterUnit.value)) })

const testing = ref(false)
const testResult = ref<IQProviderTestResult | null>(null)
// 改了地址、Key、模型，上次的测试结果就不再作数
watch(() => [form.base_url, form.api_key, form.api_format, form.models], () => { testResult.value = null })

function parseURL(raw: string): URL | null {
  try {
    const url = new URL(raw.trim())
    return url.protocol === 'http:' || url.protocol === 'https:' ? url : null
  } catch {
    return null
  }
}

// 与后端 NormalizeBaseURL 一致：去掉末尾的 / 和误填的接口路径。
const normalizedBase = computed(() => {
  const url = parseURL(form.base_url)
  if (!url) return ''
  let path = url.pathname.replace(/\/+$/, '')
  for (const suffix of ['/chat/completions', '/responses', '/models', '/completions']) {
    if (path.endsWith(suffix)) path = path.slice(0, -suffix.length)
  }
  return `${url.origin}${path.replace(/\/+$/, '')}`
})
const endpointPreview = computed(() => normalizedBase.value
  ? `${normalizedBase.value}${form.api_format === 'responses' ? '/responses' : '/chat/completions'}` : '')
const isHttp = computed(() => parseURL(form.base_url)?.protocol === 'http:')
const hostChanged = computed(() => {
  if (!props.provider || form.api_key) return false
  const before = parseURL(props.provider.base_url)
  const after = parseURL(form.base_url)
  return Boolean(before && after && before.origin.toLowerCase() !== after.origin.toLowerCase())
})

const modelSpecs = computed(() => form.models.split(',').map((s) => s.trim()).filter(Boolean))
const modelCount = computed(() => new Set(modelSpecs.value.map((s) => s.toLowerCase())).size)
const perRound = computed(() => modelCount.value * (Number(form.samples_per_model) || 0))
const perDay = computed(() => form.enabled && form.interval_seconds > 0
  ? Math.round((86400 / form.interval_seconds) * perRound.value) : 0)

const validationError = computed(() => {
  if (!form.name || form.name.length > 40) return '名称需在 1 到 40 个字之间'
  if (!normalizedBase.value) return 'Base URL 需以 http:// 或 https:// 开头，例如 https://api.example.com/v1'
  const url = parseURL(form.base_url)
  if (url && (url.username || url.password)) return 'Base URL 里不要带用户名密码'
  if (url && (url.search || url.hash)) return 'Base URL 里不要带 ? 或 # 参数'
  if (!props.provider && !form.api_key) return '请填写 API Key'
  if (/\s/.test(form.api_key)) return 'API Key 不能有空格或换行'
  if (hostChanged.value) return '改了 Base URL 的主机，需要重新填写 API Key'
  if (modelCount.value === 0) return '至少配置一个测试模型'
  if (modelCount.value > props.limits.max_models) return `测试模型最多 ${props.limits.max_models} 个`
  for (const spec of modelSpecs.value) {
    const [model, effort = ''] = spec.split('@')
    if (!model.trim() || /[\s"'\\/]/.test(model.trim())) return `模型名无效：${spec}`
    if (!EFFORTS.has(effort.trim().toLowerCase())) return `推理强度无效：${spec}（可选 none/low/medium/high/xhigh/max）`
  }
  if (!Number.isInteger(form.interval_seconds) || form.interval_seconds < 60 || form.interval_seconds > 86400) return '检测周期需在 1 分钟到 24 小时之间'
  if (!Number.isInteger(form.jitter_seconds) || form.jitter_seconds < 0 || form.jitter_seconds * 2 > form.interval_seconds) return '随机范围需在 0 到周期的一半之间'
  if (!Number.isInteger(form.samples_per_model) || form.samples_per_model < 1 || form.samples_per_model > props.limits.max_samples_per_model) {
    return `每个模型每轮测 1 到 ${props.limits.max_samples_per_model} 次`
  }
  return ''
})
const canTest = computed(() => Boolean(normalizedBase.value) && (Boolean(form.api_key) || (Boolean(props.provider) && !hostChanged.value)))

async function test() {
  testing.value = true
  try {
    testResult.value = await api.iqTestProvider({
      provider_id: props.provider?.id, base_url: form.base_url, api_format: form.api_format, api_key: form.api_key, models: form.models
    })
  } catch (error) {
    testResult.value = { ok: false, endpoint: '', message: error instanceof Error ? error.message : String(error), model_count: 0, missing: null, sample: null, latency_ms: 0 }
  } finally {
    testing.value = false
  }
}

function submit() {
  if (validationError.value) return
  emit('save', { ...form })
}
</script>
