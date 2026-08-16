<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import { hasUsableAuth } from '../utils/authContext'
import UiBadge from '../components/ui/UiBadge.vue'
import UiEmpty from '../components/ui/UiEmpty.vue'
import UiButton from '../components/ui/UiButton.vue'

const route = useRoute()
const router = useRouter()
const targetId = computed(() => route.params.id as string)

const target = ref<any>(null)
const runs = ref<any[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

const vulnsByRun = ref<Record<string, number>>({})

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
function duration(r: any): string {
  if (!r.started_at) return '—'
  const s = new Date(r.started_at).getTime()
  const e = r.ended_at ? new Date(r.ended_at).getTime() : Date.now()
  const sec = Math.max(0, Math.round((e - s) / 1000))
  if (sec < 60) return `${sec}秒`
  if (sec < 3600) return `${Math.floor(sec / 60)}分${sec % 60}秒`
  return `${Math.floor(sec / 3600)}时${Math.floor((sec % 3600) / 60)}分`
}
/** True only when the stored auth_context actually carries a usable session
 *  (shared logic in utils/authContext). */
function hasAuth(): boolean {
  return hasUsableAuth(target.value?.auth_context)
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const [tRes, rRes, vRes] = await Promise.all([
      api.get(`/targets/${targetId.value}`),
      api.get(`/targets/${targetId.value}/runs`),
      api.get(`/vulnerabilities?target_id=${targetId.value}`),
    ])
    target.value = tRes.data
    runs.value = rRes.data?.runs || rRes.data || []
    const vs = vRes.data?.vulnerabilities || vRes.data || []
    const m: Record<string, number> = {}
    for (const v of vs) if (v.status === 'confirmed') m[v.run_id] = (m[v.run_id] || 0) + 1
    vulnsByRun.value = m
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally { loading.value = false }
}

function startNewRun() {
  const v = target.value?.value || target.value?.normalized || ''
  router.push(`/targets?new=1${v ? '&target=' + encodeURIComponent(v) : ''}`)
}
function openRun(r: any) {
  router.push(`/runs/${r.id}`)
}

onMounted(load)
</script>

<template>
  <div class="col">
    <div class="proj-head">
      <div>
        <div class="muted" style="font-size: 12px">项目会话</div>
        <h2 style="font-size: 20px; font-weight: 600; margin: 2px 0 0">{{ target?.value || target?.normalized || targetId }}</h2>
        <div class="muted" style="font-size: 13px; margin-top: 2px">
          <span class="pill" style="margin-right: 6px">{{ target?.type || 'auto' }}</span>
          <span v-if="hasAuth()" class="pill confirmed">🔐 已配置会话</span>
          <span style="margin-left: 8px">{{ runs.length }} 次评估</span>
        </div>
      </div>
      <UiButton variant="primary" @click="startNewRun">＋ 新建评估</UiButton>
    </div>

    <div v-if="error" style="color: var(--danger)">{{ error }}</div>
    <div v-else-if="runs.length" class="session-list">
      <div v-for="r in runs" :key="r.id" class="session-item card" @click="openRun(r)">
        <div class="session-left">
          <UiBadge kind="status" :value="r.status" :dot="true" />
          <div class="session-meta">
            <div class="session-id">会话 <code>{{ r.id.slice(0, 8) }}</code> · {{ fmtTime(r.started_at) }}</div>
            <div class="session-summary muted">{{ r.objective || r.summary || '—' }}</div>
          </div>
        </div>
        <div class="session-right">
          <span v-if="vulnsByRun[r.id]" class="pill confirmed">⚑ {{ vulnsByRun[r.id] }} 已确认</span>
          <span class="muted" style="font-size: 12px">{{ duration(r) }} · {{ fmtN((r.input_tokens||0)+(r.output_tokens||0)) }} tok</span>
          <UiButton size="sm" variant="secondary" @click.stop="openRun(r)">查看攻击链 →</UiButton>
        </div>
      </div>
    </div>
    <UiEmpty v-else-if="!loading" icon="◇" message="该项目还没有评估记录，点击右上角发起一次" />
  </div>
</template>

<style scoped>
.proj-head { display: flex; justify-content: space-between; align-items: flex-start; }
.session-list { display: flex; flex-direction: column; gap: 10px; }
.session-item {
  display: flex; justify-content: space-between; align-items: center; gap: 16px;
  cursor: pointer;
  transition: border-color 0.15s, transform 0.15s;
}
.session-item:hover { border-color: var(--accent); transform: translateY(-1px); }
.session-left { display: flex; align-items: flex-start; gap: 12px; min-width: 0; flex: 1; }
.session-meta { min-width: 0; }
.session-id { font-size: 13px; font-weight: 500; }
.session-summary { font-size: 12px; margin-top: 3px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 560px; }
.session-right { display: flex; align-items: center; gap: 14px; flex-shrink: 0; }
</style>
