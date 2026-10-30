<template>
  <figure class="chart">
    <figcaption class="chart-caption">
      <span>{{ caption }}</span>
      <span class="chart-legend">
        <template v-for="(m, index) in models" :key="m">
          <i class="swatch line" :style="{ background: color(index) }" aria-hidden="true"></i>{{ m }}
        </template>
        <i class="swatch area" aria-hidden="true"></i>可调度
      </span>
    </figcaption>
    <p v-if="samples.length < 2" class="chart-empty">
      采样点不足。趋势由监控进程自己采集（票在账号里是覆盖写的，数据库没有历史），重启后需要重新累积。
    </p>
    <template v-else>
      <svg viewBox="0 0 100 34" preserveAspectRatio="none" role="img" :aria-label="caption">
        <line v-for="tick in [0.5, 1]" :key="tick" class="grid" x1="0" x2="100" :y1="y(maxValue * tick)" :y2="y(maxValue * tick)" />
        <polygon class="area" :points="eligibleArea" />
        <polyline v-for="(m, index) in models" :key="m" class="line" :style="{ stroke: color(index) }" :points="linePoints(m)" />
      </svg>
      <div class="chart-axis">
        <span>{{ formatClock(samples[0].at) }}</span>
        <span>{{ formatClock(samples[samples.length - 1].at) }}</span>
      </div>
    </template>
  </figure>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { CodexTrendSample } from '../types'

const props = defineProps<{ samples: CodexTrendSample[]; models: string[]; caption: string }>()

const palette = ['var(--primary)', 'var(--warning)', 'var(--attention)', 'var(--success)']
function color(index: number): string { return palette[index % palette.length] }

const maxValue = computed(() => {
  let max = 1
  for (const s of props.samples) {
    max = Math.max(max, s.eligible)
    for (const v of Object.values(s.holders ?? {})) max = Math.max(max, v)
  }
  return max
})
function x(index: number): number { return props.samples.length <= 1 ? 0 : (index / (props.samples.length - 1)) * 100 }
function y(value: number): number { return 32 - (value / maxValue.value) * 30 }

const eligibleArea = computed(() => {
  const top = props.samples.map((s, i) => `${x(i)},${y(s.eligible)}`).join(' ')
  return `0,32 ${top} 100,32`
})
function linePoints(model: string): string {
  return props.samples.map((s, i) => `${x(i)},${y(s.holders?.[model] ?? 0)}`).join(' ')
}
function formatClock(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false }).format(date)
}
</script>
