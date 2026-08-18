<script setup lang="ts">
/**
 * MCP extension center: list / add / edit / delete / test / reload for
 * user-configured external MCP servers. Tools from these servers are
 * aggregated by the agent at startup (or via the "同步到 Agent" button)
 * and surfaced to the LLM as `<server>::<tool>`.
 *
 * Style: matches the rest of the Settings page (stargaze design — dark,
 * glassy, multi-hue star accents, JetBrains Mono for IDs).
 */
import { onMounted, ref, reactive } from 'vue'
import { api } from '../api/client'
import UiCard from './ui/UiCard.vue'
import UiButton from './ui/UiButton.vue'
import UiBadge from './ui/UiBadge.vue'
import UiEmpty from './ui/UiEmpty.vue'
import UiModal from './ui/UiModal.vue'

interface MCPServer {
  id: string
  name: string
  url: string
  transport: string
  has_token: boolean
  enabled: boolean
  description?: string
  created_at: string
  updated_at: string
}

const loading = ref(false)
const servers = ref<MCPServer[]>([])
const error = ref<string>('')

// --- CRUD -------------------------------------------------------------
async function load() {
  loading.value = true
  error.value = ''
  try {
    const r = await api.get('/mcp-servers')
    servers.value = r.data?.servers || []
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}
onMounted(load)

// --- Add / Edit modal -------------------------------------------------
const showForm = ref(false)
const editing = ref<MCPServer | null>(null)
const form = reactive({
  name: '',
  url: '',
  token: '',
  enabled: true,
  description: '',
  // When editing, separate from `token` so the placeholder can read
  // "已设置 / 留空保留" without confusing the "actually clear?" intent.
  clearToken: false,
})
const formError = ref('')
const formBusy = ref(false)
const newTokenReveal = ref<string>('') // returned only on Create

function openAdd() {
  editing.value = null
  form.name = ''
  form.url = ''
  form.token = ''
  form.enabled = true
  form.description = ''
  form.clearToken = false
  formError.value = ''
  newTokenReveal.value = ''
  showForm.value = true
}
function openEdit(s: MCPServer) {
  editing.value = s
  form.name = s.name
  form.url = s.url
  form.token = '' // never re-populate (could be a stale UI value)
  form.enabled = s.enabled
  form.description = s.description || ''
  form.clearToken = false
  formError.value = ''
  newTokenReveal.value = ''
  showForm.value = true
}

async function saveForm() {
  formError.value = ''
  if (!/^[A-Za-z0-9_.\-]{1,64}$/.test(form.name.trim())) {
    formError.value = '名称需匹配 ^[A-Za-z0-9_.-]{1,64}$（用于命名空间隔离）'
    return
  }
  if (!/^https?:\/\//.test(form.url.trim())) {
    formError.value = 'URL 必须以 http:// 或 https:// 开头'
    return
  }
  formBusy.value = true
  try {
    if (editing.value) {
      const body: any = {
        name: form.name.trim(),
        url: form.url.trim(),
        transport: 'http',
        enabled: form.enabled,
        description: form.description.trim(),
      }
      // Token handling: explicit `clear_token: true` wipes; any non-empty
      // `token` replaces; omitting both keeps the stored one.
      if (form.clearToken) body.clear_token = true
      else if (form.token) body.token = form.token
      await api.put(`/mcp-servers/${editing.value.id}`, body)
    } else {
      const r = await api.post('/mcp-servers', {
        name: form.name.trim(),
        url: form.url.trim(),
        transport: 'http',
        token: form.token,
        enabled: form.enabled,
        description: form.description.trim(),
      })
      // The Create response includes the (otherwise redacted) token
      // exactly once — surface it in a copy-friendly banner.
      if (r.data?.token) newTokenReveal.value = r.data.token
    }
    showForm.value = false
    await load()
  } catch (e: any) {
    formError.value = e?.response?.data?.error || e?.message || '保存失败'
  } finally {
    formBusy.value = false
  }
}

// --- Delete -----------------------------------------------------------
const deleting = ref<MCPServer | null>(null)
async function confirmDelete() {
  if (!deleting.value) return
  try {
    await api.delete(`/mcp-servers/${deleting.value.id}`)
    deleting.value = null
    await load()
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || '删除失败'
  }
}

// --- Test connection --------------------------------------------------
const testingId = ref<string>('')
const testResult = ref<{ ok: boolean; server: string; tools: string[]; tool_count: number; error: string; latency_ms: number } | null>(null)
const showTestModal = ref(false)
async function testConnection(s: MCPServer) {
  testingId.value = s.id
  try {
    const r = await api.post(`/mcp-servers/${s.id}/test`)
    testResult.value = r.data
    showTestModal.value = true
  } catch (e: any) {
    testResult.value = {
      ok: false,
      server: s.name,
      tools: [],
      tool_count: 0,
      error: e?.response?.data?.error || e?.message || '测试失败',
      latency_ms: 0,
    }
    showTestModal.value = true
  } finally {
    testingId.value = ''
  }
}

// --- Reload (sync to agent) ------------------------------------------
const reloading = ref(false)
const reloadMsg = ref('')
async function reloadAgent() {
  reloading.value = true
  reloadMsg.value = ''
  try {
    const r = await api.post('/mcp-servers/reload')
    const { connected, reloaded } = r.data || {}
    reloadMsg.value = `已重载 ${reloaded} 个外部 MCP，${connected} 个已连接`
  } catch (e: any) {
    reloadMsg.value = '✕ ' + (e?.response?.data?.error || e?.message || '重载失败')
  } finally {
    reloading.value = false
    setTimeout(() => (reloadMsg.value = ''), 4000)
  }
}

// --- helpers ----------------------------------------------------------
function copy(text: string) {
  navigator.clipboard?.writeText(text).catch(() => {})
}
</script>

<template>
  <div>
    <div class="head">
      <p class="lead">
        把你自己的 MCP server 接进来，工具集会自动以
        <code>&lt;server&gt;::&lt;tool&gt;</code> 命名空间暴露给 Agent，
        与内置工具并行。修改后点 <b>同步到 Agent</b> 即生效，不必重启。
      </p>
      <div class="head-actions">
        <UiButton @click="reloadAgent" :disabled="reloading">
          {{ reloading ? '同步中…' : '同步到 Agent' }}
        </UiButton>
        <UiButton variant="primary" @click="openAdd">+ 添加扩展</UiButton>
      </div>
    </div>
    <div v-if="reloadMsg" class="reload-msg">{{ reloadMsg }}</div>
    <div v-if="error" class="error-msg">{{ error }}</div>

    <UiEmpty v-if="!loading && !servers.length" icon="🛰️" title="还没有任何外部 MCP 扩展">
      <div class="empty-actions">
        <UiButton variant="primary" @click="openAdd">添加第一个</UiButton>
      </div>
    </UiEmpty>

    <div v-else class="list">
      <div v-for="s in servers" :key="s.id" class="row">
        <div class="meta">
          <div class="row-top">
            <span class="name">{{ s.name }}</span>
            <UiBadge kind="status" :value="s.enabled ? 'enabled' : 'disabled'" :dot="true" />
            <UiBadge v-if="s.has_token" kind="status" value="with-token" />
          </div>
          <div class="url">{{ s.url }}</div>
          <div v-if="s.description" class="desc">{{ s.description }}</div>
        </div>
        <div class="row-actions">
          <UiButton size="sm" :disabled="testingId === s.id" @click="testConnection(s)">
            {{ testingId === s.id ? '测试中…' : '测试连接' }}
          </UiButton>
          <UiButton size="sm" @click="openEdit(s)">编辑</UiButton>
          <UiButton size="sm" variant="danger" @click="deleting = s">删除</UiButton>
        </div>
      </div>
    </div>

    <!-- Add / Edit modal -->
    <UiModal :open="showForm" :title="editing ? '编辑 MCP 扩展' : '添加 MCP 扩展'" width="560px" @close="showForm = false">
      <form class="form" @submit.prevent="saveForm">
        <div class="form-grid">
          <div>
            <label class="field-label">名称（用作 <code>&lt;server&gt;::</code> 前缀）</label>
            <input v-model="form.name" placeholder="nuclei" :disabled="!!editing" />
            <div class="hint">英文字母 / 数字 / <code>_ - .</code>，最多 64 字</div>
          </div>
          <div>
            <label class="field-label">Transport</label>
            <input value="http (streamable)" disabled />
          </div>
        </div>
        <div>
          <label class="field-label">MCP endpoint URL</label>
          <input v-model="form.url" placeholder="http://127.0.0.1:9000/mcp" />
        </div>
        <div>
          <label class="field-label">Bearer Token</label>
          <input v-model="form.token" type="password" :placeholder="editing?.has_token ? '已设置；留空保留' : '可选'" autocomplete="off" />
          <div v-if="editing?.has_token" class="row hint">
            <label class="inline-check">
              <input type="checkbox" v-model="form.clearToken" />
              <span>清空已存储的 Token</span>
            </label>
          </div>
        </div>
        <div>
          <label class="field-label">描述（可选）</label>
          <input v-model="form.description" placeholder="例如：nuclei 扫描器" />
        </div>
        <div>
          <label class="inline-check">
            <input type="checkbox" v-model="form.enabled" />
            <span>启用</span>
          </label>
        </div>
        <div v-if="newTokenReveal" class="reveal">
          <div class="reveal-title">已保存。下方 Token 仅显示这一次，请妥善保存：</div>
          <div class="reveal-row">
            <code class="reveal-token">{{ newTokenReveal }}</code>
            <UiButton size="sm" @click="copy(newTokenReveal)">复制</UiButton>
          </div>
        </div>
        <div v-if="formError" class="error-msg">{{ formError }}</div>
        <div class="form-actions">
          <UiButton @click="showForm = false" type="button">取消</UiButton>
          <UiButton variant="primary" type="submit" :disabled="formBusy">
            {{ formBusy ? '保存中…' : (editing ? '保存' : '创建') }}
          </UiButton>
        </div>
      </form>
    </UiModal>

    <!-- Delete confirm -->
    <UiModal :open="!!deleting" title="删除 MCP 扩展" width="440px" @close="deleting = null">
      <p class="modal-text">
        确认删除 <b>{{ deleting?.name }}</b> 吗？该 server 暴露的工具将立即从
        Agent 工具集中移除（下次同步生效）。
      </p>
      <div class="form-actions">
        <UiButton @click="deleting = null">取消</UiButton>
        <UiButton variant="danger" @click="confirmDelete">删除</UiButton>
      </div>
    </UiModal>

    <!-- Test result modal -->
    <UiModal :open="showTestModal && !!testResult" :title="`测试连接 · ${testResult?.server || ''}`" width="520px" @close="showTestModal = false">
      <div v-if="testResult" class="test-result">
        <div v-if="testResult.ok" class="ok">
          ✓ 连接成功 — 共 <b>{{ testResult.tool_count }}</b> 个工具
          <span class="latency">({{ testResult.latency_ms }}ms)</span>
        </div>
        <div v-else class="fail">✕ {{ testResult.error }}</div>
        <ul v-if="testResult.tools?.length" class="tool-list">
          <li v-for="t in testResult.tools" :key="t"><code>{{ testResult.server }}::{{ t }}</code></li>
        </ul>
      </div>
    </UiModal>
  </div>
</template>

<style scoped>
.head { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; margin-bottom: 14px; }
.lead { font-size: 13px; color: var(--text-dim); max-width: 60ch; line-height: 1.55; margin: 0; }
.lead code { font-family: 'JetBrains Mono', monospace; font-size: 12px; color: var(--stellar); }
.head-actions { display: flex; gap: 8px; flex-shrink: 0; }
.reload-msg { font-size: 12.5px; color: var(--ok); margin-bottom: 10px; }
.error-msg { font-size: 12.5px; color: var(--danger); margin-bottom: 10px; }

.list { display: flex; flex-direction: column; gap: 8px; }
.row {
  display: flex; justify-content: space-between; align-items: center; gap: 16px;
  padding: 14px 16px;
  background: var(--bg-elev-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  transition: border-color 0.15s ease;
}
.row:hover { border-color: var(--border-bright); }
.meta { min-width: 0; flex: 1; }
.row-top { display: flex; align-items: center; gap: 8px; margin-bottom: 4px; }
.name { font-family: 'JetBrains Mono', monospace; font-size: 14px; font-weight: 500; color: var(--stellar-bright); }
.url { font-family: 'JetBrains Mono', monospace; font-size: 12px; color: var(--text-dim); word-break: break-all; }
.desc { font-size: 12px; color: var(--text-faint); margin-top: 2px; }
.row-actions { display: flex; gap: 6px; flex-shrink: 0; }
.empty-actions { margin-top: 12px; display: flex; gap: 8px; }

.form { display: flex; flex-direction: column; gap: 14px; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.field-label { font-size: 12px; color: var(--text-dim); margin-bottom: 4px; display: block; }
.hint { font-size: 11.5px; color: var(--text-faint); margin-top: 4px; }
.inline-check { display: flex; align-items: center; gap: 6px; font-size: 12.5px; color: var(--text-dim); }
.form-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }

.reveal { background: var(--bg-elev); border: 1px solid var(--aurora); border-radius: var(--radius-md); padding: 12px; }
.reveal-title { font-size: 12.5px; color: var(--aurora); margin-bottom: 8px; }
.reveal-row { display: flex; align-items: center; gap: 8px; }
.reveal-token { font-family: 'JetBrains Mono', monospace; font-size: 12.5px; word-break: break-all; flex: 1; color: var(--text); }

.modal-text { font-size: 13.5px; line-height: 1.6; color: var(--text); margin: 0 0 12px; }
.test-result { display: flex; flex-direction: column; gap: 12px; }
.test-result .ok { color: var(--ok); font-size: 13.5px; }
.test-result .fail { color: var(--danger); font-size: 13.5px; }
.test-result .latency { color: var(--text-faint); margin-left: 4px; }
.tool-list { list-style: none; padding: 0; margin: 0; max-height: 240px; overflow: auto; display: flex; flex-direction: column; gap: 4px; }
.tool-list code { font-family: 'JetBrains Mono', monospace; font-size: 12px; color: var(--stellar); }
</style>
