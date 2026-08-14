<script setup lang="ts">
import Sparkline from '../charts/Sparkline.vue'

withDefaults(defineProps<{
  label: string
  value: string | number
  icon?: string
  iconColor?: 'violet' | 'green' | 'yellow' | 'pink'
  sparkData?: number[]
  showArrow?: boolean
  foot?: string
}>(), {
  iconColor: 'violet',
  showArrow: true,
})

defineEmits<{ (e: 'arrow'): void }>()
</script>

<template>
  <div class="stat-card-glow">
    <div class="stat-card-inner">
      <div class="stat-card-head">
        <div class="stat-card-icon" :class="iconColor">
          <span>{{ icon }}</span>
        </div>
        <button v-if="showArrow" class="stat-card-arrow" @click="$emit('arrow')">→</button>
      </div>
      <div class="stat-card-label">{{ label }}</div>
      <div class="stat-card-value">{{ value }}</div>
      <div v-if="foot" class="stat-card-foot">{{ foot }}</div>
      <div v-if="sparkData && sparkData.length > 1" class="spark-bg">
        <Sparkline :data="sparkData" :width="160" :height="48" :stroke="iconColor === 'green' ? '#34d399' : iconColor === 'yellow' ? '#fbbf24' : iconColor === 'pink' ? '#f472b6' : '#a78bfa'" :fill="iconColor === 'green' ? '#10b981' : iconColor === 'yellow' ? '#f59e0b' : iconColor === 'pink' ? '#ec4899' : '#8b5cf6'" :show-last-dot="true" :fill-opacity="0.25" />
      </div>
    </div>
  </div>
</template>
