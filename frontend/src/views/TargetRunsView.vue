<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import { hasUsableAuth } from '../utils/authContext'
import SeverityBadge from '../components/SeverityBadge.vue'
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
    const [tRes, rRes, vRes, aRes] = await Promise.all([
      api.get(`/targets/${targetId.value}`),
      api.get(`/targets/${targetId.value}/runs`),
      api.get(`/vulnerabilities?target_id=${targetId.value}`),
      api.get(`/targets/${targetId.value}/assets`),
    ])
    target.value = tRes.data
    runs.value = rRes.data?.runs || rRes.data || []
    assets.value = aRes.data?.assets || []
    const vs = vRes.data?.vulnerabilities || vRes.data || []
    const m: Record<string, number> = {}
    for (const v of vs) if (v.status === 'confirmed') m[v.run_id] = (m[v.run_id] || 0) + 1
    vulnsByRun.value = m
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally { loading.value = false }
}

// 资产清单（结构化发现：子域/端点/服务...），树优先展示。
const assets = ref<any[]>([])
const assetTypeLabel: Record<string, string> = {
  'root-domain': '根域', subdomain: '子域', ip: 'IP', service: '服务', app: '应用', endpoint: '端点',
}
function assetDepth(a: any, depth = 0, seen = new Set()): number {
  if (!a.parent_id || seen.has(a.id)) return depth
  const p = assets.value.find((x) => x.id === a.parent_id)
  if (!p) return depth
  seen.add(a.id)
  return assetDepth(p, depth + 1, seen)
}

function startNewRun() {
  const v = target.value?.value || target.value?.normalized || ''
  router.push(`/targets?new=1${v ? '&target=' + encodeURIComponent(v) : ''}`)
}
function openRun(r: any) {
  router.push(`/runs/${r.id}`)
}

// --- 报告版本对比：选两个 run，看漏洞差集（新增 / 消失 / 相同） ---
const compareIds = ref<string[]>([])
const comparing = ref(false)
const compareResult = ref<{
  a: { id: string; started_at?: string }
  b: { id: string; started_at?: string }
  added: any[]
  removed: any[]
  same: any[]
} | null>(null)

function toggleCompare(r: any) {
  if (compareIds.value.includes(r.id)) {
    compareIds.value = compareIds.value.filter((id) => id !== r.id)
  } else if (compareIds.value.length < 2) {
    compareIds.value = [...compareIds.value, r.id]
  }
  compareResult.value = null
}

function vulnKey(v: any): string {
  return `${(v.title || '').trim().toLowerCase()}::${(v.target || '').trim().toLowerCase()}`
}

async function runCompare() {
  if (compareIds.value.length !== 2) return
  comparing.value = true
  compareResult.value = null
  try {
    const [r1, r2] = await Promise.all(
      compareIds.value.map((id) => api.get(`/runs/${id}/vulnerabilities`)),
    )
    const va = ((r1.data?.vulnerabilities || []) as any[]).filter((v) => v.status !== 'dismissed')
    const vb = ((r2.data?.vulnerabilities || []) as any[]).filter((v) => v.status !== 'dismissed')
    const ka = new Set(va.map(vulnKey))
    const kb = new Set(vb.map(vulnKey))
    compareResult.value = {
      a: { id: compareIds.value[0], started_at: runs.value.find((r) => r.id === compareIds.value[0])?.started_at },
      b: { id: compareIds.value[1], started_at: runs.value.find((r) => r.id === compareIds.value[1])?.started_at },
      added: vb.filter((v) => !ka.has(vulnKey(v))),
      removed: va.filter((v) => !kb.has(vulnKey(v))),
      same: vb.filter((v) => ka.has(vulnKey(v))),
    }
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || '对比失败'
  } finally {
    comparing.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="col">
    <div class="proj-head">
      <div>
        <div class="muted" style="font-size: 12px">项目会话</div>
        <h2 class="page-title" style="font-size: 22px; margin: 2px 0 0">{{ target?.value || target?.normalized || targetId }}</h2>
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
      <div v-for="r in runs" :key="r.id" class="session-item card" :class="{ 'cmp-selected': compareIds.includes(r.id) }" @click="openRun(r)">
        <div class="session-left">
          <label class="cmp-check" @click.stop title="选择用于对比（最多 2 个）">
            <input type="checkbox" :checked="compareIds.includes(r.id)" :disabled="!compareIds.includes(r.id) && compareIds.length >= 2" @change="toggleCompare(r)" />
          </label>
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
      <div v-if="compareIds.length" class="row" style="gap: 10px">
        <UiButton variant="primary" :disabled="compareIds.length !== 2 || comparing" @click="runCompare">
          {{ comparing ? '对比中…' : `对比选中的 ${compareIds.length}/2 次评估` }}
        </UiButton>
        <button class="ghost" @click="compareIds = []; compareResult = null">取消</button>
      </div>
    </div>

    <!-- 版本对比结果：两次评估的漏洞差集 -->
    <div v-if="compareResult" class="card cmp-result">
      <h3 style="font-size: 14px; font-weight: 600; margin: 0 0 10px">版本对比</h3>
      <div class="cmp-line added">🆕 本次新增（{{ compareResult.added.length }}）</div>
      <div v-if="compareResult.added.length" class="cmp-list">
        <div v-for="(v, i) in compareResult.added" :key="i" class="cmp-row">
          <SeverityBadge :severity="v.severity || 'info'" />
          <span class="cmp-title">{{ v.title }}</span>
          <code class="muted" style="font-size: 11px">{{ v.target || '' }}</code>
        </div>
      </div>
      <div v-else class="muted" style="font-size: 12px; margin: 4px 0 10px">无</div>
      <div class="cmp-line removed">🗑 已消失（{{ compareResult.removed.length }}）</div>
      <div v-if="compareResult.removed.length" class="cmp-list">
        <div v-for="(v, i) in compareResult.removed" :key="i" class="cmp-row">
          <SeverityBadge :severity="v.severity || 'info'" />
          <span class="cmp-title">{{ v.title }}</span>
          <code class="muted" style="font-size: 11px">{{ v.target || '' }}</code>
        </div>
      </div>
      <div v-else class="muted" style="font-size: 12px; margin: 4px 0 10px">无</div>
      <div class="cmp-line same">＝ 两次均有（{{ compareResult.same.length }}）</div>
      <div v-if="compareResult.same.length" class="cmp-list">
        <div v-for="(v, i) in compareResult.same" :key="i" class="cmp-row">
          <SeverityBadge :severity="v.severity || 'info'" />
          <span class="cmp-title">{{ v.title }}</span>
          <code class="muted" style="font-size: 11px">{{ v.target || '' }}</code>
        </div>
      </div>
      <div v-else class="muted" style="font-size: 12px; margin: 4px 0 10px">无</div>
      <div class="muted" style="font-size: 11px; margin-top: 8px">
        对比 <code>{{ compareResult.a.id.slice(0, 8) }}</code>（{{ compareResult.a.started_at ? fmtTime(compareResult.a.started_at) : '—' }}）→
        <code>{{ compareResult.b.id.slice(0, 8) }}</code>（{{ compareResult.b.started_at ? fmtTime(compareResult.b.started_at) : '—' }}），已忽略 dismissed 记录
      </div>
    </div>
    <UiEmpty v-else-if="!loading" icon="◇" message="该项目还没有评估记录，点击右上角发起一次" />

    <!-- 资产清单：agent 结构化发现（子域/端点/服务），parent 缩进成树 -->
    <div v-if="assets.length" class="card" style="margin-top: 14px">
      <div class="row" style="margin-bottom: 8px">
        <h3 style="font-size: 14px; font-weight: 600; margin: 0">资产清单（{{ assets.length }}）</h3>
        <span class="spacer" />
        <span class="muted" style="font-size: 11px">agent 侦察过程中自动沉淀，跨运行累积</span>
      </div>
      <div class="asset-list">
        <div v-for="a in assets" :key="a.id" class="asset-row" :style="{ paddingLeft: (assetDepth(a) * 18 + 4) + 'px' }">
          <span class="pill" style="font-size: 10px">{{ assetTypeLabel[a.type] || a.type }}</span>
          <code style="font-size: 12px; word-break: break-all">{{ a.value }}</code>
          <span v-if="a.meta" class="muted" style="font-size: 11px; margin-left: 8px">{{ a.meta }}</span>
        </div>
      </div>
    </div>
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
.cmp-check { display: inline-flex; align-items: center; cursor: pointer; }
.cmp-check input { accent-color: var(--accent); width: 14px; height: 14px; cursor: pointer; }
.session-item.cmp-selected { border-color: var(--accent); }
.cmp-result { margin-top: 14px; }
.cmp-line { font-size: 13px; font-weight: 600; margin: 8px 0 4px; }
.cmp-line.added { color: var(--ok); }
.cmp-line.removed { color: var(--danger); }
.cmp-line.same { color: var(--text-dim); }
.cmp-list { display: flex; flex-direction: column; gap: 4px; margin-bottom: 6px; }
.cmp-row { display: flex; align-items: center; gap: 8px; font-size: 12.5px; }
.cmp-title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.asset-list { display: flex; flex-direction: column; }
.asset-row { display: flex; align-items: baseline; gap: 8px; padding: 4px 6px; border-bottom: 1px solid var(--border-soft); font-size: 12.5px; }
.asset-row:last-child { border-bottom: none; }
</style>
