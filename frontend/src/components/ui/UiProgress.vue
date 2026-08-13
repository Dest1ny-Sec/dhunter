<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  value: number
  max?: number
  label?: string
  tone?: 'accent' | 'ok' | 'danger' | 'warn'
}>(), { max: 100, tone: 'accent' })

const pct = computed(() => {
  if (props.max <= 0) return 0
  return Math.min(100, Math.round((props.value / props.max) * 100))
})
</script>

<template>
  <div class="ui-progress">
    <div v-if="label" class="prog-label">{{ label }} <span class="muted">{{ value }}{{ max ? ` / ${max}` : '' }}</span></div>
    <div class="prog-track">
      <div class="prog-fill" :class="tone" :style="{ width: pct + '%' }" />
    </div>
  </div>
</template>

<style scoped>
.prog-label { font-size: 12px; color: var(--text-dim); margin-bottom: 6px; display: flex; justify-content: space-between; }
.prog-track {
  height: 6px; border-radius: 999px;
  background: var(--bg-elev-3);
  overflow: hidden;
}
.prog-fill {
  height: 100%; border-radius: 999px;
  background: linear-gradient(90deg, var(--accent-dim), var(--accent));
  transition: width 0.3s ease;
}
.ok { background: linear-gradient(90deg, #0d9668, var(--ok)); }
.danger { background: linear-gradient(90deg, #b91c1c, var(--danger)); }
.warn { background: linear-gradient(90deg, #d97706, var(--warn)); }
</style>
