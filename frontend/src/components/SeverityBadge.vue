<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  severity: string
}>()

const colorMap: Record<string, { bg: string; fg: string; border: string }> = {
  critical: { bg: 'rgba(248, 81, 73, 0.15)', fg: 'var(--critical)', border: 'var(--critical)' },
  high: { bg: 'rgba(210, 153, 34, 0.15)', fg: 'var(--high)', border: 'var(--high)' },
  medium: { bg: 'rgba(210, 153, 34, 0.10)', fg: 'var(--medium)', border: 'var(--medium)' },
  low: { bg: 'rgba(88, 166, 255, 0.15)', fg: 'var(--low)', border: 'var(--low)' },
  info: { bg: 'rgba(110, 118, 129, 0.15)', fg: 'var(--info)', border: 'var(--info)' },
}

const style = computed(() => {
  const c = colorMap[props.severity?.toLowerCase()] || colorMap.info
  return {
    background: c.bg,
    color: c.fg,
    border: `1px solid ${c.border}`,
  }
})
</script>

<template>
  <span class="severity-badge" :style="style">{{ severity || 'unknown' }}</span>
</template>

<style scoped>
.severity-badge {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 10px;
  font-size: 11px;
  text-transform: uppercase;
  font-weight: 500;
  letter-spacing: 0.03em;
}
</style>
