<script setup lang="ts">
defineProps<{ label: string; value: string | number; icon?: string; tone?: 'default' | 'accent' | 'ok' | 'danger' | 'warn' }>()
</script>

<template>
  <div class="ui-stat" :class="tone || 'default'">
    <div class="stat-icon" v-if="icon">{{ icon }}</div>
    <div class="stat-info">
      <div class="stat-value">{{ value }}</div>
      <div class="stat-label">{{ label }}</div>
    </div>
  </div>
</template>

<style scoped>
.ui-stat {
  display: flex; align-items: center; gap: 14px;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 16px 18px;
  transition: border-color 0.15s;
}
.ui-stat:hover { border-color: var(--text-faint); }
.stat-icon {
  width: 40px; height: 40px; border-radius: 10px;
  display: flex; align-items: center; justify-content: center;
  font-size: 18px;
  background: var(--bg-elev-3);
  color: var(--text-dim);
}
.stat-value { font-size: 26px; font-weight: 700; line-height: 1.1; letter-spacing: -0.02em; }
.stat-label { font-size: 12px; color: var(--text-dim); margin-top: 2px; }
.accent .stat-value { color: var(--accent); }
.accent .stat-icon { color: var(--accent); background: rgba(124,102,255,0.12); }
.ok .stat-value { color: var(--ok); }
.ok .stat-icon { color: var(--ok); background: rgba(16,185,129,0.12); }
.danger .stat-value { color: var(--danger); }
.danger .stat-icon { color: var(--danger); background: rgba(239,68,68,0.12); }
.warn .stat-value { color: var(--warn); }
.warn .stat-icon { color: var(--warn); background: rgba(245,158,11,0.12); }
</style>
