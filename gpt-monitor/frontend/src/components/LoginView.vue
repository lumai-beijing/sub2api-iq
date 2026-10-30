<template>
  <main class="login-page">
    <section class="login-card">
      <div class="brand-mark">GP</div>
      <p class="eyebrow">Operations Console</p>
      <h1>GPT-Pro 账号监控</h1>
      <p class="muted">只读展示套餐到期、账号状态与用量窗口。</p>
      <form @submit.prevent="submit">
        <label>
          <span>管理员账号</span>
          <input v-model="username" autocomplete="username" required autofocus />
        </label>
        <label>
          <span>密码</span>
          <input v-model="password" type="password" autocomplete="current-password" required />
        </label>
        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
        <button class="primary-button" type="submit" :disabled="loading">
          <span v-if="loading" class="spinner small"></span>
          {{ loading ? '登录中…' : '登录' }}
        </button>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { ref } from 'vue'

defineProps<{ loading: boolean; error: string }>()
const emit = defineEmits<{ submit: [username: string, password: string] }>()
const username = ref('')
const password = ref('')

function submit() {
  emit('submit', username.value, password.value)
}
</script>
