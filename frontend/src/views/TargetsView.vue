<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'

const router = useRouter()

const target = ref('')
const targetType = ref<'auto' | 'company' | 'domain' | 'url' | 'ip'>('auto')
const error = ref<string | null>(null)
const loading = ref(false)
const targets = ref<any[]>([])
const targetsLoading = ref(false)
const objective = ref<string>('Find SQLi, XSS, auth bypass, IDOR, and any real reproducible vulnerability. Report each with write_finding and a curl PoC.')

const placeholders: Record<string, string> = {
  auto: 'e.g. acme.com, https://acme.com, 10.0.0.1, or "Acme Corp"',
  company: 'e.g. Acme Corp',
  domain: 'e.g. acme.com',
  url: 'e.g. https://acme.com/login',
  ip: 'e.g. 10.0.0.1',
}

async function loadTargets() {
  targetsLoading.value = true
  try {
    const res = await api.get('/targets')
    targets.value = Array.isArray(res.data) ? res.data : res.data?.targets || res.data?.items || []
  } catch (e) {
    console.warn('loadTargets', e)
  } finally {
    targetsLoading.value = false
  }
}

async function start() {
  if (!target.value.trim()) {
    error.value = 'Please enter a target'
    return
  }
  error.value = null
  loading.value = true
  try {
    // 1. Create target (server expects {input, type})
    const tRes = await api.post('/targets', {
      input: target.value.trim(),
      type: targetType.value,
    })
    const targetId = tRes.data?.id
    if (!targetId) throw new Error('No target id returned')

    // 2. Start run
    const rRes = await api.post('/runs', { target_id: targetId, objective: objective.value })
    const runId = rRes.data?.id || rRes.data?.run_id
    if (!runId) throw new Error('No run id returned')

    await loadTargets()
    router.push(`/runs/${runId}`)
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || 'Failed to start'
  } finally {
    loading.value = false
  }
}

function viewRuns(t: any) {
  router.push(`/targets/${t.id}/runs`)
}

onMounted(loadTargets)
</script>

<template>
  <div class="col" style="max-width: 720px">
    <h2 style="font-size: 18px; font-weight: 500">New Target</h2>
    <div class="muted" style="font-size: 13px">
      Enter a company name, domain, URL, or IP to begin an AI-driven reconnaissance and assessment.
    </div>

    <div class="card col" style="gap: 14px">
      <div>
        <div class="muted" style="font-size: 12px; margin-bottom: 4px">Target</div>
        <input
          v-model="target"
          :placeholder="placeholders[targetType]"
          style="width: 100%"
          @keyup.enter="start"
        />
      </div>
      <div>
        <div class="muted" style="font-size: 12px; margin-bottom: 4px">Type</div>
        <select v-model="targetType" style="width: 200px">
          <option value="auto">Auto-detect</option>
          <option value="company">Company</option>
          <option value="domain">Domain</option>
          <option value="url">URL</option>
          <option value="ip">IP</option>
        </select>
      </div>
      <div>
        <div class="muted" style="font-size: 12px; margin-bottom: 4px">Objective (what the AI should look for)</div>
        <textarea
          v-model="objective"
          rows="3"
          style="width: 100%; font-family: inherit; font-size: 13px"
        />
      </div>
      <div v-if="error" style="color: var(--red); font-size: 13px">{{ error }}</div>
      <div class="row">
        <button class="primary" :disabled="loading" @click="start">
          {{ loading ? 'Starting...' : 'Start' }}
        </button>
        <span class="muted" style="font-size: 12px">Or press Enter</span>
      </div>
    </div>

    <div class="card" style="font-size: 13px">
      <div style="font-weight: 500; margin-bottom: 8px">What happens next</div>
      <ol style="padding-left: 20px; color: var(--text-dim); line-height: 1.8">
        <li>Target is registered and a new run is created</li>
        <li>AI agent performs reconnaissance (subdomains, ports, tech stack)</li>
        <li>Selected attack surfaces are probed for vulnerabilities</li>
        <li>Report and findings are streamed back in real time</li>
      </ol>
    </div>

    <div class="row" style="margin-top: 8px">
      <h2 style="font-size: 16px; font-weight: 500">Recent targets</h2>
      <span class="muted" style="font-size: 12px">· {{ targets.length }} total</span>
    </div>
    <div v-if="targetsLoading" class="muted">Loading...</div>
    <table v-else-if="targets.length" class="card" style="padding: 0">
      <thead>
        <tr>
          <th>Type</th>
          <th>Value</th>
          <th>Created</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="t in targets" :key="t.id">
          <td><span class="pill">{{ t.type || 'auto' }}</span></td>
          <td><code style="font-size: 12px">{{ t.value || t.normalized || t.id }}</code></td>
          <td class="muted" style="font-size: 12px">
            {{ t.created_at ? new Date(t.created_at).toLocaleString() : '—' }}
          </td>
          <td>
            <button style="font-size: 11px; padding: 2px 8px" @click="viewRuns(t)">View runs</button>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="muted">No targets yet.</div>
  </div>
</template>
