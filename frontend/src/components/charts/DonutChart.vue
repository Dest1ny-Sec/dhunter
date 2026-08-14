<script setup lang="ts">
import { computed } from 'vue'

interface Slice { label: string; value: number; color: string; short?: string }

const props = withDefaults(defineProps<{
  data: Slice[]
  size?: number
  thickness?: number
  centerLabel?: string
  centerSub?: string
  showCenter?: boolean
}>(), { size: 180, thickness: 26, showCenter: true })

const total = computed(() => props.data.reduce((s, d) => s + d.value, 0) || 1)

interface Arc { d: string; color: string; from: number; to: number }
const arcs = computed<Arc[]>(() => {
  const r = props.size / 2
  const inner = r - props.thickness
  const cx = r, cy = r
  let acc = 0
  return props.data.map((s) => {
    const from = acc / total.value
    acc += s.value
    const to = acc / total.value
    const a0 = from * Math.PI * 2 - Math.PI / 2
    const a1 = to * Math.PI * 2 - Math.PI / 2
    const large = to - from > 0.5 ? 1 : 0
    const x0o = cx + r * Math.cos(a0), y0o = cy + r * Math.sin(a0)
    const x1o = cx + r * Math.cos(a1), y1o = cy + r * Math.sin(a1)
    const x0i = cx + inner * Math.cos(a0), y0i = cy + inner * Math.sin(a0)
    const x1i = cx + inner * Math.cos(a1), y1i = cy + inner * Math.sin(a1)
    const d = `M ${x0o.toFixed(2)} ${y0o.toFixed(2)} A ${r} ${r} 0 ${large} 1 ${x1o.toFixed(2)} ${y1o.toFixed(2)} L ${x1i.toFixed(2)} ${y1i.toFixed(2)} A ${inner} ${inner} 0 ${large} 0 ${x0i.toFixed(2)} ${y0i.toFixed(2)} Z`
    return { d, color: s.color, from, to }
  })
})

const dominantPct = computed(() => {
  if (!total.value) return 0
  const top = props.data.reduce((m, s) => s.value > m.value ? s : m, props.data[0] || { value: 0 })
  return Math.round((top.value / total.value) * 100)
})
</script>

<template>
  <div class="donut-wrap" :style="{ width: size + 'px' }">
    <svg :width="size" :height="size" :viewBox="`0 0 ${size} ${size}`" class="chart-svg">
      <path v-for="(a, i) in arcs" :key="i" :d="a.d" :fill="a.color" />
    </svg>
    <div v-if="showCenter" class="donut-center" :style="{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none' }">
      <div class="pct">{{ dominantPct }}%</div>
      <div class="sub">{{ centerSub || '总占比' }}</div>
    </div>
  </div>
</template>

<style scoped>
.donut-wrap { position: relative; display: inline-block; }
</style>
