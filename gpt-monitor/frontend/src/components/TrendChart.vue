<template>
  <figure class="chart" :class="{ empty: points.length === 0 }">
    <figcaption v-if="caption" class="chart-caption">
      <span>{{ caption }}</span>
      <span v-if="legend" class="chart-legend">
        <i class="swatch bar" aria-hidden="true"></i>{{ legend.bar }}
        <i class="swatch line" aria-hidden="true"></i>{{ legend.line }}
      </span>
    </figcaption>

    <p v-if="points.length === 0" class="chart-empty">暂无数据</p>

    <svg v-else :viewBox="`0 0 ${width} ${height}`" preserveAspectRatio="none" role="img" :aria-label="caption">
      <line
        v-for="tick in gridTicks"
        :key="`g${tick}`"
        class="grid"
        :x1="0" :x2="width"
        :y1="plotY(tick)" :y2="plotY(tick)"
      />
      <g v-if="mode === 'bar'">
        <rect
          v-for="(point, index) in points"
          :key="`b${index}`"
          class="bar"
          :x="barX(index)"
          :y="plotY(point.value)"
          :width="barWidth"
          :height="Math.max(height - padBottom - plotY(point.value), 0)"
        >
          <title>{{ point.label }}：{{ point.value }}</title>
        </rect>
      </g>
      <polyline v-if="linePoints" class="line" :points="linePoints" />
      <g v-if="linePoints">
        <circle
          v-for="(point, index) in points"
          :key="`d${index}`"
          class="dot"
          :cx="barX(index) + barWidth / 2"
          :cy="lineY(point.secondary ?? 0)"
          r="1.6"
        >
          <title>{{ point.label }}：{{ formatSecondary(point.secondary ?? 0) }}</title>
        </circle>
      </g>
    </svg>

    <div v-if="points.length" class="chart-axis">
      <span>{{ points[0].label }}</span>
      <span v-if="points.length > 2">{{ points[Math.floor(points.length / 2)].label }}</span>
      <span>{{ points[points.length - 1].label }}</span>
    </div>
  </figure>
</template>

<script setup lang="ts">
import { computed } from 'vue'

export interface ChartPoint {
  label: string
  value: number
  secondary?: number
}

const props = withDefaults(
  defineProps<{
    points: ChartPoint[]
    caption?: string
    mode?: 'bar' | 'line'
    secondaryUnit?: string
    secondaryMax?: number
    legend?: { bar: string; line: string }
  }>(),
  { mode: 'bar', secondaryUnit: '%' }
)

const width = 100
const height = 34
const padTop = 2
const padBottom = 2

const maxValue = computed(() => Math.max(1, ...props.points.map((point) => point.value)))
const maxSecondary = computed(() => {
  if (props.secondaryMax !== undefined) return Math.max(props.secondaryMax, 0.0001)
  return Math.max(1, ...props.points.map((point) => point.secondary ?? 0))
})
const hasSecondary = computed(() => props.points.some((point) => point.secondary !== undefined))

const barWidth = computed(() => {
  if (props.points.length === 0) return 0
  return Math.max((width / props.points.length) * 0.68, 0.6)
})

function barX(index: number): number {
  const slot = width / Math.max(props.points.length, 1)
  return slot * index + (slot - barWidth.value) / 2
}

function plotY(value: number): number {
  const usable = height - padTop - padBottom
  return height - padBottom - (value / maxValue.value) * usable
}

function lineY(value: number): number {
  const usable = height - padTop - padBottom
  return height - padBottom - (value / maxSecondary.value) * usable
}

const gridTicks = computed(() => [maxValue.value * 0.5, maxValue.value])

const linePoints = computed(() => {
  if (!hasSecondary.value || props.mode !== 'bar') return ''
  return props.points
    .map((point, index) => `${barX(index) + barWidth.value / 2},${lineY(point.secondary ?? 0)}`)
    .join(' ')
})

function formatSecondary(value: number): string {
  return `${value.toFixed(2)}${props.secondaryUnit}`
}
</script>
