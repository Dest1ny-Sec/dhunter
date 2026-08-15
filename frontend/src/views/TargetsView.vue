<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { onEnter } from '../utils/ime'
import { api } from '../api/client'
import UiButton from '../components/ui/UiButton.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiSkeleton from '../components/ui/UiSkeleton.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import Icon from '../components/icons/Icon.vue'

const router = useRouter()
const route = useRoute()

const target = ref('')
const targetType = ref<'auto' | 'company' | 'domain' | 'url' | 'ip'>('auto')
const projName = ref('')
const maxWorkers = ref(0)
const error = ref<string | null>(null)
const delError = ref<string | null>(null)
const loading = ref(false)
const targets = ref<any[]>([])
const runs = ref<any[]>([])
const vulns = ref<any[]>([])
const targetsLoading = ref(false)
const showForm = ref(false)
const objective = ref<string>('寻找 SQL 注入、XSS、鉴权绕过、IDOR 以及任何真实可复现的漏洞，使用 write_finding 工具上报，每条都给出 curl 复现命令。')

// auth section — up to two accounts for A/B IDOR testing
const authA = ref({ username: '', password: '', login_url: '', cookie: '' })
const authB = ref({ username: '', password: '', login_url: '', cookie: '' })
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

/** Validate target by type. Returns an error message or null. */
function validateTarget(input: string, type: string): string | null {
  const v = input.trim()
  if (!v) return '请输入目标'
  if (v.length > 500) return '目标长度不能超过 500 字符'
  switch (type) {
    case 'url': {
      try { new URL(v.startsWith('http') ? v : 'http://' + v) } catch { return '请输入合法的 URL，例如 https://example.com/login' }
      return null
    }
    case 'domain': {
      if (!/^([a-z0-9-]+\.)+[a-z]{2,}$/i.test(v)) return '请输入合法的域名，例如 acme.com'
      return null
    }
    case 'ip': {
      if (!/^(\d{1,3}\.){3}\d{1,3}$/.test(v)) return '请输入合法的 IPv4，例如 10.0.0.1'
      const parts = v.split('.').map(Number)
      if (parts.some((n) => n < 0 || n > 255)) return 'IP 段超出 0-255 范围'
      return null
    }
    case 'company': {
      if (v.length < 2) return '公司名至少 2 个字'
      return null
    }
    case 'auto':
    default: {
      if (v.length < 2) return '请输入至少 2 个字'
      return null
    }
  }
}

async function start() {
  const v = validateTarget(target.value, targetType.value)
  if (v) { error.value = v; return }
  error.value = null
  loading.value = true
  try {
    const tRes = await api.post('/targets', {
      input: target.value.trim(), type: targetType.value,
      name: projName.value.trim(),
      max_workers: Number(maxWorkers.value) || 0,
    })
    const targetId = tRes.data?.id
    if (!targetId) throw new Error('No target id returned')

    const cookies = authCookies.value.trim()
    const hasHeaders = authHeaders.value.trim().length > 0
    const accA = { ...authA.value, username: authA.value.username.trim() }
    const accB = { ...authB.value, username: authB.value.username.trim() }
    const hasAny = cookies || hasHeaders || accA.username || accB.username
    if (hasAny) {
      await api.patch(`/targets/${targetId}/auth`, {
        cookies, headers: parseHeaders(authHeaders.value), note: authNote.value.trim(),
        account_a: accA, account_b: accB,
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
    error.value = e?.response?.data?.error || e?.message || '启动失败'
  } finally {
    loading.value = false
  }
}

function resetForm() {
  target.value = ''
  projName.value = ''
  authCookies.value = ''
  authHeaders.value = ''
  authNote.value = ''
  authA.value = { username: '', password: '', login_url: '', cookie: '' }
  authB.value = { username: '', password: '', login_url: '', cookie: '' }
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

async function removeTarget(t: any) {
  if (!confirm(`确定删除项目「${t.name || t.value || t.id}」？该项目所有扫描记录、漏洞成果和攻击链都会被删除。`)) return
  delError.value = null
  try {
    await api.delete(`/targets/${t.id}`)
    await loadTargets()
  } catch (e: any) {
    delError.value = e?.response?.data?.error || e?.message || '未知错误'
  }
}
function viewRuns(t: any) {
  router.push(`/targets/${t.id}/runs`)
}
function exportReport(t: any) {
  const token = localStorage.getItem('dhunter_token') || ''
  window.open(`/api/targets/${t.id}/report?token=${encodeURIComponent(token)}`, '_blank')
}

onMounted(() => {
  loadTargets()
  if (route.query.new === '1') showForm.value = true
  if (typeof route.query.target === 'string' && route.query.target) target.value = route.query.target
})
</script>

<template>
  <div class="col">
    <div class="eng-head">
      <div>
        <h2 style="font-size: 20px; font-weight: 600; margin: 0">授权目标</h2>
        <div class="muted" style="font-size: 13px; margin-top: 2px">已授权 AI 进行侦察和测试的目标资产</div>
      </div>
      <UiButton variant="primary" size="md" @click="showForm = !showForm">
        <Icon v-if="!showForm" name="plus" :size="14" />
        <Icon v-else name="close" :size="14" />
        <span style="margin-left: 6px">{{ showForm ? '收起' : '新建目标' }}</span>
      </UiButton>
    </div>

    <div v-if="showForm" class="card create-form">
      <div class="form-grid">
        <div>
          <label class="field-label">项目名称（可选）</label>
          <input v-model="projName" placeholder="例如：快手 SRC 渗透" style="width: 100%" />
        </div>
        <div>
          <label class="field-label">目标</label>
          <input v-model="target" :placeholder="placeholders[targetType]" style="width: 100%" @keyup.enter="onEnter(start)" />
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
        <div>
          <label class="field-label">并发 worker 数（可选，0=平台默认）</label>
          <input v-model.number="maxWorkers" type="number" min="0" max="16" placeholder="0" style="width: 100%" />
        </div>
      </div>
      <div>
        <label class="field-label">目标说明（告诉 AI 重点找什么）</label>
        <textarea v-model="objective" rows="2" style="width: 100%" />
      </div>
      <details class="auth-details">
        <summary>身份会话（可选）— 填写登录信息以测试鉴权后接口</summary>
        <div class="auth-fields">
          <div>
            <label class="field-label">Cookie（粘贴 Cookie 头，例如 <code>sessionid=abc; uid=1</code>）</label>
            <textarea v-model="authCookies" rows="2" style="width: 100%; font-family: monospace; font-size: 12px" placeholder="sessionid=...; " />
          </div>
          <div class="acct-group">
            <div class="acct-title">账号 A（IDOR 测试主力账号）</div>
            <input v-model="authA.username" style="width: 100%" placeholder="账号 A 用户名/邮箱" />
            <input v-model="authA.password" type="password" style="width: 100%" placeholder="账号 A 密码" />
            <input v-model="authA.login_url" style="width: 100%" placeholder="登录地址（可选）" />
            <input v-model="authA.cookie" style="width: 100%; font-family: monospace; font-size: 12px" placeholder="账号 A Cookie（可选，已登录则直接填）" />
          </div>
          <div class="acct-group">
            <div class="acct-title">账号 B（越权目标账号，测 A 越权到 B）</div>
            <input v-model="authB.username" style="width: 100%" placeholder="账号 B 用户名/邮箱" />
            <input v-model="authB.password" type="password" style="width: 100%" placeholder="账号 B 密码" />
            <input v-model="authB.login_url" style="width: 100%" placeholder="登录地址（可选）" />
            <input v-model="authB.cookie" style="width: 100%; font-family: monospace; font-size: 12px" placeholder="账号 B Cookie（可选）" />
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
        <label class="field-label">红线 / 自定义要求（AI 每一轮都必须遵守，每行一条）</label>
        <textarea v-model="redLines" rows="2" style="width: 100%; font-size: 12.5px"
          placeholder="例如：禁止爆破/高频请求&#10;只在授权范围测试&#10;不测试支付/资金相关接口&#10;发现任何涉及用户数据的问题立即停止并上报" />
      </div>
      <div v-if="error" class="form-error">
        <Icon name="alert" :size="14" />
        <span>{{ error }}</span>
      </div>
      <div class="row">
        <UiButton variant="primary" size="lg" :disabled="loading" @click="start">
          <Icon name="play" :size="14" />
          <span style="margin-left: 6px">{{ loading ? '启动中…' : '启动评估' }}</span>
        </UiButton>
      </div>
    </div>

    <div v-if="delError" class="card" style="padding: 12px 14px; border-color: var(--danger); margin-bottom: 12px">
      <span style="color: var(--danger); font-size: 13px">✕ 删除失败：{{ delError }}</span>
    </div>

    <div v-if="targetsLoading" class="card" style="padding: 18px">
      <div class="sk-grid">
        <UiSkeleton v-for="i in 4" :key="i" block height="156px" radius="12px" />
      </div>
    </div>
    <div v-else-if="targets.length" class="eng-grid">
      <div v-for="t in targets" :key="t.id" class="eng-card card">
        <div class="eng-card-head">
          <span class="pill">{{ t.type || 'auto' }}</span>
          <span v-if="hasAuth(t)" class="pill confirmed" title="已配置身份会话">已授权会话</span>
          <span class="spacer" />
          <span class="muted" style="font-size: 11px">{{ t.created_at ? new Date(t.created_at).toLocaleDateString() : '' }}</span>
        </div>
        <div class="eng-card-value" @click="viewRuns(t)">{{ t.name || t.value || t.normalized || t.id }}</div>
        <div v-if="t.name && t.name !== t.value" class="muted" style="font-size: 12px">{{ t.value || t.normalized }}</div>
        <div class="eng-card-meta muted">{{ (sevByTarget[t.id] ? Object.entries(sevByTarget[t.id]).map(([s, c]) => `${s} ${c}`).join(' · ') : '暂无发现') }}</div>
        <div class="eng-card-foot">
          <div class="eng-card-foot-info">
            <UiBadge v-if="lastRun[t.id]" kind="status" :value="lastRun[t.id].status" :dot="true" />
            <span v-else class="muted" style="font-size: 11px">暂无运行</span>
            <span class="muted" style="font-size: 11px">{{ runCounts[t.id] || 0 }} 次运行</span>
          </div>
          <div class="eng-card-foot-actions">
            <button class="ghost" @click="newRun(t)">新建运行</button>
            <button @click="viewRuns(t)">历史</button>
            <button @click="exportReport(t)">导出报告</button>
            <button class="ghost danger-text" @click="removeTarget(t)" :aria-label="'删除项目 ' + (t.name || t.value)">删除</button>
          </div>
        </div>
      </div>
    </div>
    <div v-else-if="!showForm" class="card" style="padding: 0">
      <EmptyState
        icon="target"
        title="还没有授权目标"
        description="填入一个公司名、域名、URL 或 IP，AI 会自动识别类型并启动一次侦察评估。所有目标的扫描进度、漏洞成果、攻击链都会在这里汇总。"
        primary-label="新建第一个目标"
        @primary="showForm = true"
      />
    </div>
  </div>
</template>

<style scoped>
.eng-head { display: flex; justify-content: space-between; align-items: flex-start; }
.create-form { display: flex; flex-direction: column; gap: 14px; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr 220px; gap: 12px; }
.field-label { font-size: 12px; color: var(--text-dim); margin-bottom: 4px; display: block; }
.auth-details summary { cursor: pointer; font-size: 13px; color: var(--stellar-bright); }
.auth-fields { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 12px; margin-top: 12px; }
.eng-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 14px; }
.eng-card { display: flex; flex-direction: column; gap: 8px; transition: border-color 0.15s, transform 0.15s; }
.eng-card:hover { border-color: var(--border-bright); transform: translateY(-2px); box-shadow: 0 6px 24px rgba(125, 146, 232, 0.12); }
.eng-card-head { display: flex; align-items: center; gap: 8px; }
.eng-card-value { font-size: 14px; font-weight: 600; cursor: pointer; word-break: break-all; }
.eng-card-value:hover { color: var(--stellar-bright); }
.eng-card-meta { font-size: 12px; }
.eng-card-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding-top: 10px;
  border-top: 1px solid var(--border-soft);
  flex-wrap: wrap;
}
.eng-card-foot-info {
  display: flex;
  align-items: center;
  gap: 10px;
  white-space: nowrap;
  flex-shrink: 0;
  font-size: 11.5px;
}
.eng-card-foot-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
.eng-card-foot-actions button {
  min-height: 28px;
  padding: 0 12px;
  font-size: 12px;
  white-space: nowrap;
  flex-shrink: 0;
}
.eng-card-foot-actions .danger-text { color: var(--danger); }
.sk-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 14px; }
.form-error {
  display: flex; align-items: center; gap: 8px;
  padding: 10px 12px;
  background: rgba(226, 100, 114, 0.08);
  border: 1px solid rgba(226, 100, 114, 0.28);
  border-radius: var(--radius-sm);
  color: var(--sev-critical);
  font-size: 12.5px;
}
@media (max-width: 900px) { .form-grid, .auth-fields { grid-template-columns: 1fr; } }
</style>
