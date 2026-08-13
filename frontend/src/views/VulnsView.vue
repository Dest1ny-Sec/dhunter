<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import SeverityBadge from '../components/SeverityBadge.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiModal from '../components/ui/UiModal.vue'
import UiEmpty from '../components/ui/UiEmpty.vue'

interface Vuln {
  id: string; run_id?: string; target?: string; url?: string; title?: string; name?: string; severity: string;
  status?: string; evidence?: any; description?: string; impact?: string; recommendation?: string;
}

const vulns = ref<Vuln[]>([])
const runs = ref<any[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const filterSeverity = ref('')
const filterRun = ref('')
const filterStatus = ref('')
const detail = ref<Vuln | null>(null)

const severities = ['critical', 'high', 'medium', 'low', 'info']
const statuses = ['confirmed', 'dismissed', 'pending', 'open']

const filtered = computed(() => vulns.value.filter((v) => {
  if (filterSeverity.value && v.severity?.toLowerCase() !== filterSeverity.value) return false
  if (filterRun.value && v.run_id !== filterRun.value) return false
  if (filterStatus.value && v.status !== filterStatus.value) return false
  return true
}))
const confirmedCount = computed(() => vulns.value.filter((v) => v.status === 'confirmed').length)

function fmtEvidence(e: any): string {
  if (e == null) return ''
  if (typeof e === 'string') return e
  try { return JSON.stringify(e, null, 2) } catch { return String(e) }
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const [vRes, rRes] = await Promise.all([
      api.get('/vulnerabilities'), api.get('/runs'),
    ])
    vulns.value = Array.isArray(vRes.data) ? vRes.data : vRes.data?.vulnerabilities || []
    runs.value = Array.isArray(rRes.data) ? rRes.data : rRes.data?.runs || []
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || 'Failed to load'
  } finally { loading.value = false }
}

onMounted(load)
</script>

<template>
  <div class="col">
    <div class="vulns-head">
      <div>
        <h2 style="font-size: 20px; font-weight: 600; margin: 0">Vulnerabilities</h2>
        <div class="muted" style="font-size: 13px; margin-top: 2px">{{ confirmedCount }} confirmed across all engagements</div>
      </div>
      <button @click="load">↻ Refresh</button>
    </div>

    <div class="card row filters">
      <select v-model="filterSeverity" style="min-width: 130px">
        <option value="">All severities</option>
        <option v-for="s in severities" :key="s" :value="s">{{ s }}</option>
      </select>
      <select v-model="filterStatus" style="min-width: 130px">
        <option value="">All statuses</option>
        <option v-for="s in statuses" :key="s" :value="s">{{ s }}</option>
      </select>
      <select v-model="filterRun" style="min-width: 200px">
        <option value="">All runs</option>
        <option v-for="r in runs" :key="r.id" :value="r.id">{{ r.id.slice(0, 8) }}{{ r.target_value ? ` — ${r.target_value}` : '' }}</option>
      </select>
      <div class="spacer" />
      <span class="muted" style="font-size: 12px">{{ filtered.length }} / {{ vulns.length }}</span>
    </div>

    <div v-if="error" style="color: var(--danger)">{{ error }}</div>
    <div v-else-if="filtered.length" class="card" style="padding: 0">
      <table>
        <thead>
          <tr><th>Severity</th><th>Title</th><th>Target</th><th>Status</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="v in filtered" :key="v.id">
            <td><SeverityBadge :severity="v.severity || 'info'" /></td>
            <td>
              <div style="font-size: 13px">{{ v.title || v.name || v.id }}</div>
              <div v-if="v.description" class="muted" style="font-size: 11px; margin-top: 2px">{{ v.description.slice(0, 140) }}{{ v.description.length > 140 ? '…' : '' }}</div>
            </td>
            <td class="muted"><code style="font-size: 11px">{{ v.target || v.url || '—' }}</code></td>
            <td><UiBadge kind="status" :value="v.status || 'open'" /></td>
            <td><button style="min-height: 28px; padding: 0 10px; font-size: 12px" @click="detail = v">Details</button></td>
          </tr>
        </tbody>
      </table>
    </div>
    <UiEmpty v-else-if="!loading" icon="⚑" message="No vulnerabilities match the current filters" />

    <UiModal :open="!!detail" title="Finding details" @close="detail = null">
      <div v-if="detail" class="detail">
        <div class="row">
          <SeverityBadge :severity="detail.severity || 'info'" />
          <UiBadge kind="status" :value="detail.status || 'open'" />
        </div>
        <h3 style="font-size: 15px; margin: 12px 0 6px">{{ detail.title }}</h3>
        <div class="muted" style="font-size: 12px; margin-bottom: 12px"><code>{{ detail.target || detail.url || detail.id }}</code></div>
        <template v-if="detail.description"><h4 class="sec">Description</h4><p>{{ detail.description }}</p></template>
        <template v-if="detail.impact"><h4 class="sec">Impact</h4><p>{{ detail.impact }}</p></template>
        <template v-if="detail.recommendation"><h4 class="sec">Recommendation</h4><p>{{ detail.recommendation }}</p></template>
        <template v-if="detail.evidence">
          <h4 class="sec">Evidence</h4>
          <pre style="max-height: 260px; overflow: auto"><code>{{ fmtEvidence(detail.evidence) }}</code></pre>
        </template>
      </div>
    </UiModal>
  </div>
</template>

<style scoped>
.vulns-head { display: flex; justify-content: space-between; align-items: flex-start; }
.filters { flex-wrap: wrap; }
.detail .sec { font-size: 12px; color: var(--text-dim); text-transform: uppercase; letter-spacing: 0.05em; margin: 14px 0 6px; }
.detail p { font-size: 13px; line-height: 1.6; }
</style>
