<script setup lang="ts">
import Sparkline from '../charts/Sparkline.vue'
import Icon from '../icons/Icon.vue'

withDefaults(defineProps<{
  label: string
  value: string | number
  icon?: string
  iconName?: string
  sparkData?: number[]
  foot?: string
  /** optional accent color (hex) for the sparkline & icon glow */
  accent?: string
}>(), { accent: '#a78bfa' })

defineEmits<{ (e: 'arrow'): void }>()
</script>

<template>
  <div class="stat-hero" @click="$emit('arrow')">
    <div v-if="iconName" class="hero-icon">
      <Icon :name="iconName" :size="16" />
    </div>
    <div v-else-if="icon" class="hero-icon">{{ icon }}</div>
    <div class="hero-label">{{ label }}</div>
    <div class="hero-value" :class="{ 'is-zero': value === 0 || value === '0' }">{{ value }}</div>
    <div v-if="foot" class="hero-foot">{{ foot }}</div>
    <div v-if="sparkData && sparkData.length > 1" class="hero-spark">
      <Sparkline
        :data="sparkData"
        :width="220"
        :height="80"
        :stroke="accent"
        :fill="accent"
        :fill-opacity="0.18"
        :show-last-dot="true"
      />
    </div>
  </div>
</template>
