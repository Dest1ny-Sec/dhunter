<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import UiStat from '../components/ui/UiStat.vue'
import UiCard from '../components/ui/UiCard.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiEmpty from '../components/ui/UiEmpty.vue'

const router = useRouter()
const targets = ref<any[]>([])
const runs = ref<any[]>([])
const vulns = ref<any[]>([])
const loading = ref(true)

const severityOrder = ['critical', 'high', 'medium', 'low', 'info']

const totalTokens = computed(() => runs.value.reduce((s, r) => s + (r.input_tokens || 0) + (r.output_tokens || 0) + (r.cache_read_input_tokens || 0), 0))
const sevCounts = computed(() => {
  const m: Record<string, number> = {}
  for (const s of severityOrder) m[s] = 0
  for (const v of vulns.value) if (v.status !== 'dismissed') m[v.severity?.toLowerCase() || 'info'] = (m[v.severity?.toLowerCase() || 'info'] || 0) + 1
  return m
})
const confirmedCount = computed(() => vulns.value.filter((v) => v.status === 'confirmed').length)
const runningRuns = computed(() => runs.value.filter((r) => r.status === 'running' || r.status === 'pending').length)

function fmtN(n?: number): string {
  if (n == null) return '—'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}
function fmtTime(s?: string) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString() } catch { return s }
}

onMounted(async () => {
  try {
    const [t, r, v] = await Promise.all([
      api.get('/targets'), api.get('/runs'), api.get('/vulnerabilities'),
    ])
    targets.value = t.data?.targets || t.data || []
    runs.value = r.data?.runs || r.data || []
    vulns.value = v.data?.vulnerabilities || v.data || []
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="col">
    <div class="stat-grid">
      <UiStat label="Engagements" :value="targets.length" icon="◈" tone="accent" />
      <UiStat label="Total runs" :value="runs.length" icon="⏵" />
      <UiStat label="Running now" :value="runningRuns" icon="⟳" tone="warn" />
      <UiStat label="Confirmed vulns" :value="confirmedCount" icon="⚑" :tone="confirmedCount > 0 ? 'danger' : 'default'" />
    </div>

    <div class="stat-grid sev-row">
      <div v-for="s in severityOrder" :key="s" class="sev-cell" :class="`sev-${s}`">
        <span class="sev-dot" />
        <span class="sev-label">{{ s }}</span>
        <span class="sev-count">{{ sevCounts[s] || 0 }}</span>
      </div>
    </div>

    <div class="dash-grid">
      <UiCard title="Recent runs">
        <template v-if="runs.length">
          <div v-for="r in runs.slice(0, 8)" :key="r.id" class="dash-row" @click="router.push(`/runs/${r.id}`)">
            <UiBadge kind="status" :value="r.status" :dot="true" />
            <span class="dash-row-id"><code>{{ r.id.slice(0, 8) }}</code></span>
            <span class="dash-row-tgt muted">{{ r.target_value || r.target_id || '—' }}</span>
            <span class="spacer" />
            <span class="muted" style="font-size: 11px">{{ fmtN(r.input_tokens) }} tok · {{ fmtTime(r.started_at) }}</span>
          </div>
        </template>
        <UiEmpty v-else icon="⏵" message="No runs yet — start from Engagements" />
      </UiCard>

      <UiCard title="Latest findings">
        <template v-if="vulns.length">
          <div v-for="v in vulns.slice(0, 8)" :key="v.id" class="dash-row">
            <UiBadge kind="severity" :value="v.severity" />
            <span class="dash-row-title">{{ v.title || v.name || v.id }}</span>
            <span class="spacer" />
            <span class="muted" style="font-size: 11px">{{ v.status }}</span>
          </div>
        </template>
        <UiEmpty v-else icon="⚑" message="No findings yet" />
      </UiCard>
    </div>
  </div>
</template>

<style scoped>
.stat-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 14px; }
.sev-row { display: grid; grid-template-columns: repeat(5, 1fr); gap: 14px; }
.sev-cell {
  display: flex; align-items: center; gap: 8px;
  background: var(--bg-elev); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 14px 16px;
}
.sev-dot { width: 10px; height: 10px; border-radius: 50%; }
.sev-critical .sev-dot { background: var(--sev-critical); }
.sev-high .sev-dot { background: var(--sev-high); }
.sev-medium .sev-dot { background: var(--sev-medium); }
.sev-low .sev-dot { background: var(--sev-low); }
.sev-info .sev-dot { background: var(--sev-info); }
.sev-label { font-size: 12px; color: var(--text-dim); text-transform: capitalize; }
.sev-count { margin-left: auto; font-weight: 700; font-size: 16px; }
.dash-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.dash-row {
  display: flex; align-items: center; gap: 10px;
  padding: 9px 6px; border-radius: var(--radius-sm);
  cursor: pointer;
}
.dash-row:hover { background: var(--bg-elev-2); }
.dash-row-title { font-size: 12.5px; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dash-row-tgt { font-size: 12px; }
.dash-row-id { min-width: 60px; }
@media (max-width: 1100px) { .dash-grid { grid-template-columns: 1fr; } .sev-row { grid-template-columns: repeat(3, 1fr); } }
</style>
