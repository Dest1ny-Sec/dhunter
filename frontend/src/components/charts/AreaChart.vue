<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  data: number[]
  labels?: string[]
  width?: number
  height?: number
  showGrid?: boolean
  yMax?: number
  ySteps?: number
  strokeColor?: string
  fillColor?: string
  showLastDot?: boolean
}>(), {
  width: 540, height: 220, showGrid: true, ySteps: 4,
  strokeColor: '#a78bfa', fillColor: '#8b5cf6',
})

const yMax = computed(() => props.yMax ?? (Math.max(...(props.data.length ? props.data : [0])) * 1.15))
const padX = 36, padY = 24

const points = computed(() => {
  const d = props.data.length ? props.data : [0]
  const stepX = (props.width - padX * 2) / Math.max(d.length - 1, 1)
  return d.map((v, i) => ({
    x: padX + i * stepX,
    y: padY + (props.height - padY * 2) * (1 - v / yMax.value),
    v,
  }))
})

const linePath = computed(() => {
  const ps = points.value
  if (ps.length < 2) return ''
  let out = `M${ps[0].x.toFixed(2)},${ps[0].y.toFixed(2)}`
  for (let i = 1; i < ps.length; i++) {
    const p0 = ps[i - 1], p1 = ps[i]
    const cx = (p0.x + p1.x) / 2
    out += ` C${cx.toFixed(2)},${p0.y.toFixed(2)} ${cx.toFixed(2)},${p1.y.toFixed(2)} ${p1.x.toFixed(2)},${p1.y.toFixed(2)}`
  }
  return out
})

const areaPath = computed(() => {
  if (!linePath.value) return ''
  const last = points.value[points.value.length - 1]
  return `${linePath.value} L${last.x.toFixed(2)},${props.height - padY} L${padX},${props.height - padY} Z`
})

const yLabels = computed(() => {
  const arr: { y: number; v: number }[] = []
  for (let i = 0; i <= props.ySteps; i++) {
    const v = Math.round((yMax.value / props.ySteps) * (props.ySteps - i))
    const y = padY + (props.height - padY * 2) * (i / props.ySteps)
    arr.push({ y, v })
  }
  return arr
})

const xLabels = computed(() => {
  if (!props.labels) return [] as { x: number; text: string }[]
  const ps = points.value
  return ps.map((p, i) => ({ x: p.x, text: props.labels?.[i] ?? '' }))
})
</script>

<template>
  <svg :viewBox="`0 0 ${width} ${height}`" :width="width" :height="height" class="chart-svg" preserveAspectRatio="none">
    <defs>
      <linearGradient id="area-fill-grad" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" :stop-color="fillColor" stop-opacity="0.55" />
        <stop offset="100%" :stop-color="fillColor" stop-opacity="0" />
      </linearGradient>
      <linearGradient id="area-stroke-grad" x1="0" y1="0" x2="1" y2="0">
        <stop offset="0%" :stop-color="strokeColor" stop-opacity="0.4" />
        <stop offset="50%" :stop-color="strokeColor" stop-opacity="0.95" />
        <stop offset="100%" :stop-color="strokeColor" stop-opacity="1" />
      </linearGradient>
    </defs>

    <!-- horizontal grid lines -->
    <g v-if="showGrid">
      <line v-for="(g, i) in yLabels" :key="i" :x1="padX" :x2="width - padX" :y1="g.y" :y2="g.y" class="grid-line" />
    </g>

    <!-- y axis labels -->
    <g>
      <text v-for="(g, i) in yLabels" :key="i" :x="padX - 8" :y="g.y + 3" text-anchor="end" class="axis-label">{{ g.v }}</text>
    </g>

    <!-- x axis labels -->
    <g v-if="xLabels.length">
      <text v-for="(g, i) in xLabels" :key="i" :x="g.x" :y="height - 6" text-anchor="middle" class="axis-label">{{ g.text }}</text>
    </g>

    <!-- area + line -->
    <path :d="areaPath" fill="url(#area-fill-grad)" />
    <path :d="linePath" fill="none" stroke="url(#area-stroke-grad)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" />

    <!-- last point highlight -->
    <g v-if="showLastDot && points.length">
      <circle :cx="points[points.length - 1].x" :cy="points[points.length - 1].y" r="5" fill="#fff" :stroke="strokeColor" stroke-width="2.5" />
    </g>
  </svg>
</template>
