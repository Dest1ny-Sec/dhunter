<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  /** data points (numbers) */
  data: number[]
  width?: number
  height?: number
  stroke?: string
  fill?: string
  fillOpacity?: number
  showDots?: boolean
  showLastDot?: boolean
  smooth?: boolean
}>(), {
  width: 220, height: 56,
  stroke: '#a78bfa', fill: '#8b5cf6', fillOpacity: 0.25,
  showDots: false, showLastDot: true, smooth: true,
})

const path = computed(() => {
  const d = props.data
  if (!d || d.length < 2) return { line: '', area: '', dots: [] as { x: number; y: number; v: number }[] }
  const min = Math.min(...d), max = Math.max(...d)
  const range = max - min || 1
  const stepX = props.width / (d.length - 1)
  const pts = d.map((v, i) => {
    const x = i * stepX
    const y = props.height - ((v - min) / range) * (props.height - 4) - 2
    return { x, y, v }
  })
  const buildSmooth = (ps: { x: number; y: number }[]) => {
    if (!props.smooth) return ps.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(2)},${p.y.toFixed(2)}`).join(' ')
    let out = `M${ps[0].x.toFixed(2)},${ps[0].y.toFixed(2)}`
    for (let i = 1; i < ps.length; i++) {
      const p0 = ps[i - 1], p1 = ps[i]
      const cx = (p0.x + p1.x) / 2
      out += ` C${cx.toFixed(2)},${p0.y.toFixed(2)} ${cx.toFixed(2)},${p1.y.toFixed(2)} ${p1.x.toFixed(2)},${p1.y.toFixed(2)}`
    }
    return out
  }
  const line = buildSmooth(pts)
  const area = `${line} L${pts[pts.length - 1].x.toFixed(2)},${props.height} L0,${props.height} Z`
  return { line, area, dots: pts }
})

const gradId = `spark-${Math.random().toString(36).slice(2, 8)}`
</script>

<template>
  <svg :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" preserveAspectRatio="none" class="chart-svg">
    <defs>
      <linearGradient :id="gradId" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" :stop-color="fill" :stop-opacity="fillOpacity" />
        <stop offset="100%" :stop-color="fill" stop-opacity="0" />
      </linearGradient>
    </defs>
    <path :d="path.area" :fill="`url(#${gradId})`" />
    <path :d="path.line" fill="none" :stroke="stroke" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
    <circle v-if="showDots" v-for="(p, i) in path.dots" :key="i" :cx="p.x" :cy="p.y" r="2" :fill="stroke" />
    <circle v-if="showLastDot && path.dots.length" :cx="path.dots[path.dots.length - 1].x" :cy="path.dots[path.dots.length - 1].y" r="3.5" fill="#fff" :stroke="stroke" stroke-width="2" />
  </svg>
</template>
