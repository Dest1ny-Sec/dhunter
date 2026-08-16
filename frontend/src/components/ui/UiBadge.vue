<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ kind?: 'severity' | 'status'; value?: string; dot?: boolean }>()

// Every value with a dedicated color rule in the <style> block below.
// Unknown values (e.g. a new backend state the UI doesn't style yet) fall
// back to b-info instead of rendering an unstyled class name.
const KNOWN = new Set([
  'critical', 'high', 'medium', 'low', 'info',
  'running', 'queued', 'claimed', 'open', 'paused',
  'success', 'completed', 'confirmed', 'failed', 'pending',
  'cancelled', 'dismissed', 'concluded',
])

const cls = computed(() => {
  const v = (props.value || 'info').toLowerCase()
  return `b-${KNOWN.has(v) ? v : 'info'}`
})
</script>

<template>
  <span class="ui-badge" :class="[cls, { dot: dot }]">
    <span v-if="dot" class="b-dot" />
    <slot>{{ value }}</slot>
  </span>
</template>

<style scoped>
.ui-badge {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 500;
  border: 1px solid var(--border);
  background: var(--bg-elev-2);
  color: var(--text-dim);
  text-transform: capitalize;
}
.b-dot { width: 6px; height: 6px; border-radius: 50%; background: currentColor; }

/* severity */
.b-critical { color: var(--sev-critical); background: rgba(239,68,68,0.1); border-color: rgba(239,68,68,0.35); }
.b-high { color: var(--sev-high); background: rgba(245,158,11,0.1); border-color: rgba(245,158,11,0.35); }
.b-medium { color: var(--sev-medium); background: rgba(251,191,36,0.08); border-color: rgba(251,191,36,0.3); }
.b-low { color: var(--sev-low); background: rgba(6,182,212,0.08); border-color: rgba(6,182,212,0.3); }
.b-info { color: var(--sev-info); }

/* status */
.b-running, .b-queued, .b-claimed, .b-open { color: var(--accent); background: rgba(124,102,255,0.1); border-color: rgba(124,102,255,0.35); }
.b-paused { color: var(--stellar-bright); background: rgba(147,197,253,0.08); border-color: rgba(147,197,253,0.35); }
.b-success, .b-completed, .b-confirmed { color: var(--ok); background: rgba(16,185,129,0.1); border-color: rgba(16,185,129,0.35); }
.b-failed { color: var(--danger); background: rgba(239,68,68,0.1); border-color: rgba(239,68,68,0.35); }
.b-pending { color: var(--text-dim); }
.b-cancelled { color: var(--warn); background: rgba(245,158,11,0.1); border-color: rgba(245,158,11,0.35); }
.b-dismissed { color: var(--text-faint); }
.b-concluded { color: var(--ok); background: rgba(16,185,129,0.08); }
</style>
