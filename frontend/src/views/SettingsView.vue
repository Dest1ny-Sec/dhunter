<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import UiCard from '../components/ui/UiCard.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiButton from '../components/ui/UiButton.vue'
import { useAuthStore } from '../stores/auth'

const status = ref<any>(null)
const auth = useAuthStore()

interface ExternalMCPServerStatus {
  name: string
  status: 'connected' | 'error' | 'unknown'
  tool_count: number
  tools: string[]
  error: string
}

interface ExternalMCPSyncStatus {
  last_reload_at: number
  last_reload_error: string
  servers: ExternalMCPServerStatus[]
}

const externalMCP = ref<ExternalMCPSyncStatus | null>(null)
const externalMCPError = ref('')
const externalServers = computed(() => externalMCP.value?.servers || [])
const externalConnected = computed(() => externalServers.value.filter((s) => s.status === 'connected').length)
const externalTools = computed(() => externalServers.value.flatMap((s) =>
  (s.tools || []).map((tool) => ({ server: s.name, tool, name: `${s.name}::${tool}` })),
))
const externalBadge = computed(() => {
  if (externalMCPError.value) return 'failed'
  if (!externalServers.value.length) return 'pending'
  return externalConnected.value === externalServers.value.length ? 'connected' : 'failed'
})

// ---- AI API config (ccswitch-style import + test) ----
const llm = ref({ provider: 'anthropic', base_url: '', model: '', api_key: '', max_tokens: 8192 })
const testState = ref<'idle' | 'testing' | 'ok' | 'fail'>('idle')
const testDetail = ref('')
const llmSaved = ref(false)

// ---- token budget red line ----
const budget = ref(0)

// ---- 登录账号（首启自动生成，可在此修改） ----
const account = ref({ username: '', password: '' })
const accountMsg = ref<{ ok: boolean; text: string } | null>(null)

// ---- 清空数据（二次确认） ----
const clearArmed = ref(false)
const clearMsg = ref<{ ok: boolean; text: string } | null>(null)
const clearing = ref(false)

async function clearData() {
  if (!clearArmed.value) {
    clearArmed.value = true
    clearMsg.value = null
    setTimeout(() => (clearArmed.value = false), 6000)
    return
  }
  clearing.value = true
  clearMsg.value = null
  try {
    await api.post('/settings/clear-data')
    clearMsg.value = { ok: true, text: '已清空全部测试数据' }
    setTimeout(() => location.reload(), 800)
  } catch (e: any) {
    clearMsg.value = { ok: false, text: e?.response?.data?.error || e?.message || '清空失败' }
  } finally {
    clearing.value = false
    clearArmed.value = false
  }
}

async function load() {
  try {
    const externalReq = api.get('/mcp-servers/agent-status')
      .then((r) => r.data)
      .catch((e: any) => {
        externalMCPError.value = e?.response?.data?.error || e?.message || 'Agent 状态不可用'
        return null
      })
    const [st, llmRes, bud, ext] = await Promise.all([
      api.get('/status'), api.get('/settings/llm'), api.get('/settings/budget'), externalReq,
    ])
    status.value = st.data
    externalMCP.value = ext
    if (ext) externalMCPError.value = ''
    if (llmRes.data?.model) llm.value = { ...llm.value, ...llmRes.data, api_key: llmRes.data.api_key || '' }
    budget.value = bud.data?.max_run_tokens || 0
    account.value.username = auth.username || account.value.username || 'admin'
  } catch {}
}

async function saveAccount() {
  accountMsg.value = null
  const name = account.value.username.trim()
  if (!name || account.value.password.length < 6) {
    accountMsg.value = { ok: false, text: '账号必填，新密码至少 6 位' }
    return
  }
  try {
    await api.post('/auth/change', { username: name, password: account.value.password })
    auth.username = name
    localStorage.setItem('dhunter_user', name)
    account.value.password = ''
    accountMsg.value = { ok: true, text: '登录账号已更新，下次登录请使用新凭据' }
  } catch (e: any) {
    accountMsg.value = { ok: false, text: e?.response?.data?.error || e?.message || '保存失败' }
  }
}

async function testConnection() {
  testState.value = 'testing'
  testDetail.value = ''
  try {
    const res = await api.post('/settings/llm/test', llm.value)
    testState.value = res.data?.ok ? 'ok' : 'fail'
    testDetail.value = res.data?.ok
      ? `已连接 · ${res.data?.model} · ${res.data?.latency_ms}ms`
      : `连接失败: ${res.data?.error || res.data?.detail || '未知错误'}`
  } catch (e: any) {
    testState.value = 'fail'
    testDetail.value = e?.response?.data?.error || e?.message || '测试失败'
  }
}

async function saveLLM() {
  try {
    await api.put('/settings/llm', llm.value)
    llmSaved.value = true
    setTimeout(() => (llmSaved.value = false), 2000)
  } catch (e: any) {
    testDetail.value = '保存失败: ' + (e?.response?.data?.error || e?.message)
  }
}

async function saveBudget() {
  try {
    await api.put('/settings/budget', { max_run_tokens: Number(budget.value) || 0 })
  } catch {}
}

onMounted(load)
</script>

<template>
  <div class="col">
    <h2 class="page-title">设置</h2>

    <div class="section-title">配置</div>
    <div class="settings-grid">
      <UiCard title="AI 大模型（导入你自己的模型，测试连接）">
        <div class="form-grid">
          <div>
            <label class="field-label">协议</label>
            <select v-model="llm.provider" style="width: 100%">
              <option value="anthropic">Anthropic 兼容</option>
              <option value="openai">OpenAI 兼容</option>
            </select>
          </div>
          <div>
            <label class="field-label">模型 ID</label>
            <input v-model="llm.model" placeholder="例如 MiniMax-M3[1M] / deepseek-chat" style="width: 100%" />
          </div>
        </div>
        <div>
          <label class="field-label">Base URL</label>
          <input v-model="llm.base_url" placeholder="例如 https://api.minimaxi.com/anthropic" style="width: 100%" />
        </div>
        <div>
          <label class="field-label">API Key</label>
          <input v-model="llm.api_key" type="password" placeholder="sk-..." style="width: 100%" />
        </div>
        <div class="row">
          <UiButton variant="primary" :disabled="testState === 'testing'" @click="testConnection">
            {{ testState === 'testing' ? '测试中…' : '测试连接' }}
          </UiButton>
          <UiButton @click="saveLLM">保存配置</UiButton>
          <span v-if="testState === 'ok'" style="color: var(--ok); font-size: 13px">✓ {{ testDetail }}</span>
          <span v-else-if="testState === 'fail'" style="color: var(--danger); font-size: 13px">✕ {{ testDetail }}</span>
          <span v-if="llmSaved" style="color: var(--ok); font-size: 13px">已保存 ✓</span>
        </div>
        <div class="muted" style="font-size: 12px">
          保存后新建扫描会使用这个模型。兼容 OpenAI/Anthropic 协议（DeepSeek / MiniMax / Qwen / GLM / Claude…）。
        </div>
      </UiCard>

      <UiCard title="登录账号（首次运行自动生成，可修改）">
        <div class="form-grid">
          <div>
            <label class="field-label">用户名</label>
            <input v-model="account.username" autocomplete="username" style="width: 100%" />
          </div>
          <div>
            <label class="field-label">新密码（至少 6 位）</label>
            <input v-model="account.password" type="password" autocomplete="new-password" style="width: 100%" />
          </div>
        </div>
        <div class="row">
          <UiButton variant="primary" @click="saveAccount">更新账号</UiButton>
          <span v-if="accountMsg" :style="accountMsg.ok ? 'color: var(--ok); font-size: 13px' : 'color: var(--danger); font-size: 13px'">
            {{ accountMsg.ok ? '✓ ' : '✕ ' }}{{ accountMsg.text }}
          </span>
        </div>
        <div class="muted" style="font-size: 12px">
          修改后登录凭据立即生效（Bearer token 不变）。
          <b>忘记密码：</b>在 <code>configs/dhunter.yaml</code> 设置
          <code>bootstrap_password: 新密码</code> 并加 <code>force_reset_password: true</code>，
          重启后密码即重置为该值，登录成功后请移除该开关。
        </div>
      </UiCard>
    </div>

    <div class="section-title">资源与预算</div>
    <div class="settings-grid">
      <UiCard title="清空数据（危险操作）">
        <div class="muted" style="font-size: 12px; margin-bottom: 10px">
          清空全部测试数据：目标、运行记录、对话消息、漏洞成果、先验知识。账号与 LLM 配置保留。
        </div>
        <div class="row">
          <UiButton
            variant="danger"
            :disabled="clearing"
            @click="clearData"
          >{{ clearing ? '清空中…' : (clearArmed ? '再次点击确认清空' : '清空全部数据') }}</UiButton>
          <span v-if="clearMsg" :style="clearMsg.ok ? 'color: var(--ok); font-size: 13px' : 'color: var(--danger); font-size: 13px'">
            {{ clearMsg.ok ? '✓ ' : '✕ ' }}{{ clearMsg.text }}
          </span>
        </div>
      </UiCard>

      <UiCard title="Token 预算红线（每次扫描最大消耗）">
        <div class="row">
          <input v-model.number="budget" type="number" min="0" step="100000" style="width: 200px" />
          <UiButton @click="saveBudget">保存</UiButton>
          <span class="muted" style="font-size: 12px">0 = 不限。超过预算扫描自动停止。</span>
        </div>
      </UiCard>
    </div>

    <div class="section-title">平台服务</div>
    <UiCard title="平台服务（自动检测）">
      <div class="svc-row">
        <div class="svc-info">
          <div class="svc-name">MCP 工具集 <span class="muted" style="font-size: 11px">({{ status?.mcp?.url || '—' }})</span></div>
          <div class="muted" style="font-size: 12px">{{ (status?.mcp?.tools || []).length }} 个内置工具，随平台启动</div>
        </div>
        <UiBadge kind="status" :value="status?.mcp?.status || 'pending'" :dot="true" />
      </div>
      <!-- 外部扫描器可用性（缺失自动跳过，不报错） -->
      <div v-if="status?.mcp?.availability" class="tool-chips">
        <span v-for="(ok, name) in status.mcp.availability" :key="name" class="tool-chip" :class="ok ? 'avail' : 'missing'" :title="ok ? name + ' 已安装' : name + ' 未安装（agent 会自动跳过，改用替代方法）'">
          {{ ok ? '✓' : '✗' }} {{ name }}
        </span>
      </div>
      <div v-if="(status?.mcp?.tools || []).length" class="tool-chips">
        <span v-for="t in status.mcp.tools" :key="t" class="tool-chip">{{ t }}</span>
      </div>
      <div class="svc-row">
        <div class="svc-info">
          <div class="svc-name">外部 MCP 扩展</div>
          <div v-if="externalMCPError" class="muted" style="font-size: 12px">{{ externalMCPError }}</div>
          <div v-else-if="externalServers.length" class="muted" style="font-size: 12px">
            {{ externalTools.length }} 个外部工具，{{ externalConnected }}/{{ externalServers.length }} 个服务已连接
          </div>
          <div v-else class="muted" style="font-size: 12px">尚未配置；请前往「库 → MCP 扩展」添加</div>
        </div>
        <UiBadge kind="status" :value="externalBadge" :dot="true" />
      </div>
      <div v-if="externalTools.length" class="tool-chips">
        <span
          v-for="item in externalTools"
          :key="item.name"
          class="tool-chip external"
          :title="`${item.server} 提供的外部工具`"
        >{{ item.name }}</span>
      </div>
      <div class="svc-row">
        <div class="svc-info">
          <div class="svc-name">Agent 引擎 <span class="muted" style="font-size: 11px">({{ status?.agent?.url || '—' }})</span></div>
          <div class="muted" style="font-size: 12px">黑板调度器 + 多 worker，随平台启动</div>
        </div>
        <UiBadge kind="status" :value="status?.agent?.status || 'pending'" :dot="true" />
      </div>
      <div class="svc-row">
        <div class="svc-info">
          <div class="svc-name">存储</div>
          <div class="muted" style="font-size: 12px">{{ status?.db?.path || '—' }}</div>
        </div>
        <UiBadge kind="status" value="connected" :dot="true" />
      </div>
    </UiCard>
  </div>
</template>

<style scoped>
.settings-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 28px; align-items: start; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.field-label { font-size: 12px; color: var(--text-dim); margin-bottom: 4px; display: block; }
.svc-row {
  display: flex; justify-content: space-between; align-items: center; gap: 16px;
  padding: 12px 0; border-bottom: 1px solid var(--border-soft);
}
.svc-row:last-child { border-bottom: none; }
.svc-name { font-size: 13.5px; font-weight: 500; }
.tool-chips { display: flex; flex-wrap: wrap; gap: 6px; padding: 4px 0 12px; }
.tool-chip {
  font-size: 11px; font-family: 'JetBrains Mono', monospace;
  background: var(--bg-elev-2); border: 1px solid var(--border);
  border-radius: 999px; padding: 2px 10px; color: var(--text-dim);
}
.tool-chip.avail { color: var(--ok); border-color: rgba(16,185,129,0.3); }
.tool-chip.missing { color: var(--text-faint); text-decoration: line-through; }
.tool-chip.external { color: var(--aurora-bright); border-color: rgba(95, 200, 212, 0.3); }
@media (max-width: 1100px) { .settings-grid { grid-template-columns: 1fr; } }
</style>
