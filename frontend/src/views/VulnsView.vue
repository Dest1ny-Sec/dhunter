<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import SeverityBadge from '../components/SeverityBadge.vue'

interface Vuln {
  id: string
  run_id?: string
  target?: string
  title?: string
  name?: string
  severity: string
  status?: string
  evidence?: any
  description?: string
  url?: string
  affected?: string
}

const vulns = ref<Vuln[]>([])
const runs = ref<any[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

const filterSeverity = ref<string>('')
const filterRun = ref<string>('')
const expanded = ref<Record<string, boolean>>({})

const severities = ['critical', 'high', 'medium', 'low', 'info']

const filtered = computed(() => {
  return vulns.value.filter((v) => {
    if (filterSeverity.value && v.severity?.toLowerCase() !== filterSeverity.value) return false
    if (filterRun.value && v.run_id !== filterRun.value) return false
    return true
  })
})

async function load() {
  loading.value = true
  error.value = null
  try {
    const [vRes, rRes] = await Promise.all([
      api.get('/vulnerabilities').catch(() => ({ data: { vulnerabilities: [] } })),
      api.get('/runs').catch(() => ({ data: { runs: [] } })),
    ])
    vulns.value = Array.isArray(vRes.data) ? vRes.data : vRes.data?.vulnerabilities || vRes.data?.items || []
    runs.value = Array.isArray(rRes.data) ? rRes.data : rRes.data?.runs || rRes.data?.items || []
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || 'Failed to load'
  } finally {
    loading.value = false
  }
}

function fmtEvidence(e: any): string {
  if (e == null) return ''
  if (typeof e === 'string') return e
  try {
    return JSON.stringify(e, null, 2)
  } catch {
    return String(e)
  }
}

function toggle(id: string) {
  expanded.value[id] = !expanded.value[id]
}

onMounted(load)
</script>

<template>
  <div class="col">
    <div class="row">
      <h2 style="font-size: 18px; font-weight: 500">Vulnerabilities</h2>
      <div class="spacer" />
      <button @click="load">Refresh</button>
    </div>

    <div class="card row" style="gap: 16px">
      <label class="row" style="gap: 8px">
        <span class="muted" style="font-size: 12px">Severity:</span>
        <select v-model="filterSeverity" style="min-width: 120px">
          <option value="">All</option>
          <option v-for="s in severities" :key="s" :value="s">{{ s }}</option>
        </select>
      </label>
      <label class="row" style="gap: 8px">
        <span class="muted" style="font-size: 12px">Run:</span>
        <select v-model="filterRun" style="min-width: 200px">
          <option value="">All</option>
          <option v-for="r in runs" :key="r.id" :value="r.id">
            {{ r.id.slice(0, 8) }}{{ r.target_value ? ` — ${r.target_value}` : '' }}
          </option>
        </select>
      </label>
      <div class="spacer" />
      <span class="muted" style="font-size: 12px">{{ filtered.length }} / {{ vulns.length }}</span>
    </div>

    <div v-if="error" style="color: var(--red)">{{ error }}</div>
    <div v-else-if="loading" class="muted">Loading...</div>
    <div v-else-if="filtered.length === 0" class="muted">No vulnerabilities match the current filters.</div>
    <table v-else class="card" style="padding: 0">
      <thead>
        <tr>
          <th style="width: 100px">Severity</th>
          <th>Title</th>
          <th>Target</th>
          <th>Status</th>
          <th style="width: 80px">Evidence</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="v in filtered" :key="v.id">
          <tr>
            <td><SeverityBadge :severity="v.severity || 'info'" /></td>
            <td>
              <div>{{ v.title || v.name || v.id }}</div>
              <div v-if="v.description" class="muted" style="font-size: 12px; margin-top: 2px">
                {{ v.description.slice(0, 200) }}{{ v.description.length > 200 ? '...' : '' }}
              </div>
            </td>
            <td class="muted">
              <code style="font-size: 11px">{{ v.target || v.url || v.affected || '—' }}</code>
            </td>
            <td>
              <span class="pill" :class="v.status || 'pending'">{{ v.status || 'open' }}</span>
            </td>
            <td>
              <button
                v-if="v.evidence"
                style="font-size: 11px; padding: 2px 8px"
                @click="toggle(v.id)"
              >
                {{ expanded[v.id] ? 'Hide' : 'Show' }}
              </button>
            </td>
          </tr>
          <tr v-if="expanded[v.id]">
            <td colspan="5" style="background: var(--bg-elev-2); padding: 0">
              <pre style="margin: 0; border: 0; border-radius: 0; max-height: 300px; overflow: auto">
                <code>{{ fmtEvidence(v.evidence) }}</code>
              </pre>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>
