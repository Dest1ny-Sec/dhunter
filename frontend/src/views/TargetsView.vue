<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import UiButton from '../components/ui/UiButton.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiEmpty from '../components/ui/UiEmpty.vue'

const router = useRouter()

const target = ref('')
const targetType = ref<'auto' | 'company' | 'domain' | 'url' | 'ip'>('auto')
const error = ref<string | null>(null)
const loading = ref(false)
const targets = ref<any[]>([])
const runs = ref<any[]>([])
const vulns = ref<any[]>([])
const targetsLoading = ref(false)
const showForm = ref(false)
const objective = ref<string>('寻找 SQL 注入、XSS、鉴权绕过、IDOR 以及任何真实可复现的漏洞，使用 write_finding 工具上报，每条都给出 curl 复现命令。')

// auth section
const authCookies = ref('')
const authHeaders = ref('')
const authNote = ref('')
// custom guardrails the AI must always follow
const redLines = ref('')

const placeholders: Record<string, string> = {
  auto: '例如 acme.com、https://acme.com、10.0.0.1，或"某科技公司"',
  company: '例如 某科技公司',
  domain: '例如 acme.com',
  url: '例如 https://acme.com/login',
  ip: '例如 10.0.0.1',
}

const runCounts = computed(() => {
  const m: Record<string, number> = {}
  for (const r of runs.value) m[r.target_id] = (m[r.target_id] || 0) + 1
  return m
})
const lastRun = computed(() => {
  const m: Record<string, any> = {}
  for (const r of runs.value) {
    const cur = m[r.target_id]
    if (!cur || new Date(r.started_at || 0) > new Date(cur.started_at || 0)) m[r.target_id] = r
  }
  return m
})
const sevByTarget = computed(() => {
  const m: Record<string, Record<string, number>> = {}
  for (const v of vulns.value) {
    if (v.status === 'dismissed') continue
    const key = v.target_id || ''
    const t = (m[key] = m[key] || {})
    t[v.severity?.toLowerCase() || 'info'] = (t[v.severity?.toLowerCase() || 'info'] || 0) + 1
  }
  return m
})

async function loadTargets() {
  targetsLoading.value = true
  try {
    const [tRes, rRes, vRes] = await Promise.all([
      api.get('/targets'), api.get('/runs'), api.get('/vulnerabilities'),
    ])
    targets.value = tRes.data?.targets || tRes.data || []
    runs.value = rRes.data?.runs || rRes.data || []
    vulns.value = vRes.data?.vulnerabilities || vRes.data || []
  } finally {
    targetsLoading.value = false
  }
}

async function start() {
  if (!target.value.trim()) {
    error.value = '请输入目标'
    return
  }
  error.value = null
  loading.value = true
  try {
    const tRes = await api.post('/targets', { input: target.value.trim(), type: targetType.value })
    const targetId = tRes.data?.id
    if (!targetId) throw new Error('No target id returned')

    const cookies = authCookies.value.trim()
    const hasHeaders = authHeaders.value.trim().length > 0
    if (cookies || hasHeaders) {
      await api.patch(`/targets/${targetId}/auth`, {
        cookies, headers: parseHeaders(authHeaders.value), note: authNote.value.trim(),
      })
    }
    const reds = redLines.value.trim()
    if (reds) {
      await api.patch(`/targets/${targetId}/redlines`, { red_lines: reds })
    }
    const rRes = await api.post('/runs', { target_id: targetId, objective: objective.value })
    const runId = rRes.data?.id || rRes.data?.run_id
    if (!runId) throw new Error('No run id returned')

    resetForm()
    await loadTargets()
    router.push(`/runs/${runId}`)
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || 'Failed to start'
  } finally {
    loading.value = false
  }
}

function resetForm() {
  target.value = ''
  authCookies.value = ''
  authHeaders.value = ''
  authNote.value = ''
  redLines.value = ''
  showForm.value = false
  error.value = null
}

function parseHeaders(raw: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of raw.split('\n')) {
    const idx = line.indexOf(':')
    if (idx <= 0) continue
    out[line.slice(0, idx).trim()] = line.slice(idx + 1).trim()
  }
  return out
}

function hasAuth(t: any): boolean {
  return !!(t?.auth_context && t.auth_context !== '' && t.auth_context !== '{}')
}

function newRun(t: any) {
  router.push(`/targets/${t.id}/runs`)
}
function viewRuns(t: any) {
  router.push(`/targets/${t.id}/runs`)
}

onMounted(loadTargets)
</script>

<template>
  <div class="col">
    <div class="eng-head">
      <div>
        <h2 style="font-size: 20px; font-weight: 600; margin: 0">授权目标</h2>
        <div class="muted" style="font-size: 13px; margin-top: 2px">已授权 AI 进行侦察和测试的目标资产</div>
      </div>
      <UiButton variant="primary" size="md" @click="showForm = !showForm">{{ showForm ? '收起' : '＋ 新建目标' }}</UiButton>
    </div>

    <div v-if="showForm" class="card create-form">
      <div class="form-grid">
        <div>
          <label class="field-label">目标</label>
          <input v-model="target" :placeholder="placeholders[targetType]" style="width: 100%" @keyup.enter="start" />
        </div>
        <div>
          <label class="field-label">类型</label>
          <select v-model="targetType" style="width: 100%">
            <option value="auto">自动识别</option>
            <option value="company">公司</option>
            <option value="domain">域名</option>
            <option value="url">URL</option>
            <option value="ip">IP</option>
          </select>
        </div>
      </div>
      <div>
        <label class="field-label">目标说明（告诉 AI 重点找什么）</label>
        <textarea v-model="objective" rows="2" style="width: 100%" />
      </div>
      <details class="auth-details">
        <summary>🔐 身份会话（可选）— 填写登录信息以测试鉴权后接口</summary>
        <div class="auth-fields">
          <div>
            <label class="field-label">Cookie（粘贴 Cookie 头，例如 <code>sessionid=abc; uid=1</code>）</label>
            <textarea v-model="authCookies" rows="2" style="width: 100%; font-family: monospace; font-size: 12px" placeholder="sessionid=...; " />
          </div>
          <div>
            <label class="field-label">自定义请求头（每行 <code>Key: value</code>）</label>
            <textarea v-model="authHeaders" rows="2" style="width: 100%; font-family: monospace; font-size: 12px" placeholder="Authorization: Bearer ..." />
          </div>
          <div>
            <label class="field-label">备注（这个账号是什么身份？）</label>
            <input v-model="authNote" style="width: 100%" placeholder="例如：普通注册用户，role=user" />
          </div>
        </div>
      </details>
      <div>
        <label class="field-label">🚫 红线 / 自定义要求（AI 每一轮都必须遵守，每行一条）</label>
        <textarea v-model="redLines" rows="2" style="width: 100%; font-size: 12.5px"
          placeholder="例如：禁止爆破/高频请求&#10;只在授权范围测试&#10;不测试支付/资金相关接口&#10;发现任何涉及用户数据的问题立即停止并上报" />
      </div>
      <div v-if="error" style="color: var(--danger); font-size: 13px">{{ error }}</div>
      <div class="row">
        <UiButton variant="primary" size="lg" :disabled="loading" @click="start">
          {{ loading ? '启动中…' : '启动评估' }}
        </UiButton>
      </div>
    </div>

    <div v-if="targetsLoading" class="muted">Loading…</div>
    <div v-else-if="targets.length" class="eng-grid">
      <div v-for="t in targets" :key="t.id" class="eng-card card">
        <div class="eng-card-head">
          <span class="pill">{{ t.type || 'auto' }}</span>
          <span v-if="hasAuth(t)" class="pill confirmed" title="authenticated session configured">🔐 auth</span>
          <span class="spacer" />
          <span class="muted" style="font-size: 11px">{{ t.created_at ? new Date(t.created_at).toLocaleDateString() : '' }}</span>
        </div>
        <div class="eng-card-value" @click="viewRuns(t)">{{ t.value || t.normalized || t.id }}</div>
        <div class="eng-card-meta muted">{{ (sevByTarget[t.id] ? Object.entries(sevByTarget[t.id]).map(([s, c]) => `${s} ${c}`).join(' · ') : 'no findings') }}</div>
        <div class="eng-card-foot">
          <UiBadge v-if="lastRun[t.id]" kind="status" :value="lastRun[t.id].status" :dot="true" />
          <span v-else class="muted" style="font-size: 11px">暂无运行</span>
          <span class="muted" style="font-size: 11px">{{ runCounts[t.id] || 0 }} 次运行</span>
          <span class="spacer" />
          <button class="ghost" style="min-height: 28px; padding: 0 10px; font-size: 12px" @click="newRun(t)">新建运行</button>
          <button style="min-height: 28px; padding: 0 10px; font-size: 12px" @click="viewRuns(t)">历史</button>
        </div>
      </div>
    </div>
    <UiEmpty v-else-if="!targetsLoading" icon="◈" message="暂无授权目标，点击右上角新建第一个" />
  </div>
</template>

<style scoped>
.eng-head { display: flex; justify-content: space-between; align-items: flex-start; }
.create-form { display: flex; flex-direction: column; gap: 14px; }
.form-grid { display: grid; grid-template-columns: 1fr 220px; gap: 12px; }
.field-label { font-size: 12px; color: var(--text-dim); margin-bottom: 4px; display: block; }
.auth-details summary { cursor: pointer; font-size: 13px; color: var(--accent); }
.auth-fields { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 12px; margin-top: 12px; }
.eng-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 14px; }
.eng-card { display: flex; flex-direction: column; gap: 8px; transition: border-color 0.15s, transform 0.15s; }
.eng-card:hover { border-color: var(--text-faint); transform: translateY(-1px); }
.eng-card-head { display: flex; align-items: center; gap: 8px; }
.eng-card-value { font-size: 14px; font-weight: 600; cursor: pointer; word-break: break-all; }
.eng-card-value:hover { color: var(--accent); }
.eng-card-meta { font-size: 12px; }
.eng-card-foot { display: flex; align-items: center; gap: 10px; padding-top: 8px; border-top: 1px solid var(--border-soft); }
@media (max-width: 900px) { .form-grid, .auth-fields { grid-template-columns: 1fr; } }
</style>
