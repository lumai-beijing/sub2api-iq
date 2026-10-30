<template>
  <div class="usage-cell">
    <div class="usage-heading">
      <strong>{{ usageText(window.available, window.used_percent) }}</strong>
      <span v-if="window.available && stale" class="stale-label">过期</span>
    </div>
    <div v-if="window.available" class="progress-track" aria-hidden="true">
      <span
        class="progress-fill"
        :class="{ warning: used >= 90, severe: used >= 100 }"
        :style="{ width: `${Math.min(Math.max(used, 0), 100)}%` }"
      ></span>
    </div>
    <small v-if="window.available">{{ formatDateTime(window.resets_at) }} 重置</small>
    <small v-else>当前窗口未提供</small>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { UsageWindow } from '../types'
import { formatDateTime, usageText } from '../utils'

const props = defineProps<{
  window: UsageWindow
  stale: boolean
}>()

const used = computed(() => props.window.used_percent ?? 0)
</script>
