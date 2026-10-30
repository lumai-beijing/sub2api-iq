<template>
  <div class="app-shell">
    <header class="topbar">
      <div class="topbar-brand">
        <div class="brand-mark compact">GP</div>
        <div>
          <p class="eyebrow">Operations Console</p>
          <h1>GPT-Pro 账号监控</h1>
        </div>
      </div>
      <div class="topbar-actions">
        <div class="updated-block">
          <span>{{ updatedLabel }}</span>
          <strong>{{ updatedAt }}</strong>
        </div>
        <button class="secondary-button" type="button" :disabled="loading" @click="$emit('refresh')">
          <span v-if="loading" class="spinner small"></span>
          {{ loading ? '刷新中…' : '立即刷新' }}
        </button>
        <button class="ghost-button" type="button" @click="$emit('logout')">退出</button>
      </div>
    </header>

    <nav class="tab-bar" v-if="tabs.length > 1" aria-label="看板切换">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        type="button"
        class="tab"
        :class="{ active: tab.key === active }"
        :aria-current="tab.key === active ? 'page' : undefined"
        @click="$emit('select', tab.key)"
      >
        {{ tab.label }}
      </button>
      <span v-if="username" class="operator-pill small">{{ username }}</span>
    </nav>

    <main class="content">
      <slot></slot>
    </main>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  tabs: { key: string; label: string }[]
  active: string
  updatedAt: string
  updatedLabel: string
  loading: boolean
  username?: string
}>()

defineEmits<{ refresh: []; logout: []; select: [key: string] }>()
</script>
