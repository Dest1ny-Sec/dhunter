<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import UiBadge from '../components/ui/UiBadge.vue'
import UiEmpty from '../components/ui/UiEmpty.vue'

interface Run {
  id: string
  target_id?: string
  target_value?: string
  status: string
  started_at?: string
  ended_at?: string
  summary?: string
  input_tokens?: number
  output_tokens?: number
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
    error.value = e?.response?.data?.error || e?.message || '加载运行记录失败'
  } finally {
    loading.value = false
  }
}

function duration(r: Run): string {
  if (!r.started_at) return '—'
  const s = new Date(r.started_at).getTime()
  const e = r.ended_at ? new Date(r.ended_at).getTime() : Date.now()
  const sec = Math.max(0, Math.round((e - s) / 1000))
  if (sec < 60) return `${sec}秒`
  if (sec < 3600) return `${Math.floor(sec / 60)}分${sec % 60}秒`
  return `${Math.floor(sec / 3600)}时${Math.floor((sec % 3600) / 60)}分`
}

function tokens(r: Run): number {
  return (r.input_tokens || 0) + (r.output_tokens || 0) + (r.cache_read_input_tokens || 0)
}
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

onMounted(load)
</script>

<template>
  <div class="col">
    <div class="runs-head">
      <div>
        <h2 style="font-size: 20px; font-weight: 600; margin: 0">运行记录</h2>
        <div class="muted" style="font-size: 13px; margin-top: 2px">共 {{ runs.length }} 次评估</div>
      </div>
      <button @click="load">↻ 刷新</button>
    </div>

    <div v-if="error" style="color: var(--danger)">{{ error }}</div>
    <div v-else-if="runs.length" class="card" style="padding: 0">
      <table>
        <thead>
          <tr>
            <th>状态</th>
            <th>目标</th>
            <th>耗时</th>
            <th>Tokens</th>
            <th>开始时间</th>
            <th>摘要</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in runs" :key="r.id" style="cursor: pointer" @click="router.push(`/runs/${r.id}`)">
            <td><UiBadge kind="status" :value="r.status" :dot="true" /></td>
            <td>
              <code style="font-size: 12px">{{ r.id.slice(0, 8) }}</code>
              <span class="muted" style="font-size: 11px; margin-left: 6px">{{ r.target_value || r.target_id || '' }}</span>
            </td>
            <td class="muted" style="font-size: 12px">{{ duration(r) }}</td>
            <td class="muted" style="font-size: 12px">{{ fmtN(tokens(r)) }}</td>
            <td class="muted" style="font-size: 12px">{{ fmtTime(r.started_at) }}</td>
            <td class="muted" style="font-size: 12px; max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ r.summary }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <UiEmpty v-else-if="!loading" icon="⏵" message="暂无运行记录，去授权目标发起一次扫描" />
  </div>
</template>

<style scoped>
.runs-head { display: flex; justify-content: space-between; align-items: flex-start; }
</style>
