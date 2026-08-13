<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'

interface Run {
  id: string
  target_id?: string
  target_value?: string
  status: string
  created_at?: string
  updated_at?: string
  started_at?: string
  ended_at?: string
  summary?: string
  input_tokens?: number
  output_tokens?: number
  cache_creation_input_tokens?: number
  cache_read_input_tokens?: number
}

const runs = ref<Run[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const router = useRouter()

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await api.get('/runs')
    runs.value = Array.isArray(res.data) ? res.data : res.data?.runs || res.data?.items || []
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || 'Failed to load runs'
  } finally {
    loading.value = false
  }
}

function fmtTime(s?: string) {
  if (!s) return '—'
  try {
    return new Date(s).toLocaleString()
  } catch {
    return s
  }
}

function fmtN(n?: number): string {
  if (n == null) return '—'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}

onMounted(load)
</script>

<template>
  <div class="col">
    <div class="row">
      <h2 style="font-size: 18px; font-weight: 500">Runs</h2>
      <div class="spacer" />
      <button @click="load">Refresh</button>
    </div>
    <div v-if="error" style="color: var(--red)">{{ error }}</div>
    <div v-else-if="loading" class="muted">Loading...</div>
    <div v-else-if="runs.length === 0" class="muted">No runs yet. Start one from the Targets page.</div>
    <table v-else class="card" style="padding: 0">
      <thead>
        <tr>
          <th>ID</th>
          <th>Target</th>
          <th>Status</th>
          <th>Tokens (in / out / cache)</th>
          <th>Started</th>
          <th>Ended</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="r in runs"
          :key="r.id"
          style="cursor: pointer"
          @click="router.push(`/runs/${r.id}`)"
        >
          <td><code>{{ r.id.slice(0, 8) }}</code></td>
          <td>{{ r.target_value || r.target_id || '—' }}</td>
          <td><span class="pill" :class="r.status">{{ r.status }}</span></td>
          <td class="muted" style="font-size: 12px">
            {{ fmtN(r.input_tokens) }} / {{ fmtN(r.output_tokens) }} / {{ fmtN(r.cache_read_input_tokens) }}
          </td>
          <td class="muted">{{ fmtTime(r.started_at) }}</td>
          <td class="muted">{{ fmtTime(r.ended_at) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
