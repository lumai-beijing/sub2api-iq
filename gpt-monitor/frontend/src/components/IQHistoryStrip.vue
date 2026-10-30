<template>
  <div class="iq-strip" :class="{ compact, mini }" role="img" :aria-label="ariaLabel">
    <span v-if="padding > 0" v-for="n in padding" :key="`pad${n}`" class="iq-pip pad" aria-hidden="true"></span>
    <span
      v-for="(item, index) in items"
      :key="index"
      class="iq-pip"
      :class="[`outcome-${item.outcome}`, { 'variant-fixed': item.variant === 'fixed' }]"
      :title="item.title"
    ></span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { IQOutcome, IQVariant } from '../types'
import { iqOutcomeLabel } from '../utils'

export interface StripItem {
  outcome: IQOutcome
  title?: string
  // 固定题面（随机化之前）的结果画浅一点，一眼看出改版前后的分界
  variant?: IQVariant
}

const props = withDefaults(defineProps<{
  items: StripItem[]
  // 固定槽位数：不满时左侧补空槽，最新的点总在最右边，和参考图一致
  slots?: number
  compact?: boolean
  // 总览矩阵格子里的迷你条
  mini?: boolean
}>(), { slots: 0, compact: false, mini: false })

const padding = computed(() => Math.max(0, props.slots - props.items.length))
const ariaLabel = computed(() => {
  const counts = props.items.reduce<Record<string, number>>((acc, item) => {
    acc[item.outcome] = (acc[item.outcome] ?? 0) + 1
    return acc
  }, {})
  const parts = Object.entries(counts).map(([outcome, n]) => `${iqOutcomeLabel(outcome)} ${n}`)
  return `最近 ${props.items.length} 次：${parts.join('，') || '暂无数据'}`
})
</script>
