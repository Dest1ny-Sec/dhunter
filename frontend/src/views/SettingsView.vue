<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import UiCard from '../components/ui/UiCard.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiButton from '../components/ui/UiButton.vue'

const status = ref<any>(null)

// ---- AI API config (ccswitch-style import + test) ----
const llm = ref({ provider: 'anthropic', base_url: '', model: '', api_key: '', max_tokens: 8192 })
const testState = ref<'idle' | 'testing' | 'ok' | 'fail'>('idle')
const testDetail = ref('')
const llmSaved = ref(false)

// ---- token budget red line ----
const budget = ref(0)

async function load() {
  try {
    const [st, llmRes, bud] = await Promise.all([
      api.get('/status'), api.get('/settings/llm'), api.get('/settings/budget'),
    ])
    status.value = st.data
    if (llmRes.data?.model) llm.value = { ...llm.value, ...llmRes.data, api_key: llmRes.data.api_key || '' }
    budget.value = bud.data?.max_run_tokens || 0
  } catch {}
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
    <h2 style="font-size: 20px; font-weight: 600; margin: 0">设置</h2>

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

      <UiCard title="Token 预算红线（每次扫描最大消耗）">
        <div class="row">
          <input v-model.number="budget" type="number" min="0" step="100000" style="width: 200px" />
          <UiButton @click="saveBudget">保存</UiButton>
          <span class="muted" style="font-size: 12px">0 = 不限。超过预算扫描自动停止。</span>
        </div>
      </UiCard>
    </div>

    <UiCard title="平台服务（自动检测）">
      <div class="svc-row">
        <div class="svc-info">
          <div class="svc-name">MCP 工具集 <span class="muted" style="font-size: 11px">({{ status?.mcp?.url || '—' }})</span></div>
          <div class="muted" style="font-size: 12px">{{ (status?.mcp?.tools || []).length }} 个内置工具，随平台启动</div>
        </div>
        <UiBadge kind="status" :value="status?.mcp?.status || 'pending'" :dot="true" />
      </div>
      <div v-if="(status?.mcp?.tools || []).length" class="tool-chips">
        <span v-for="t in status.mcp.tools" :key="t" class="tool-chip">{{ t }}</span>
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
.settings-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
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
@media (max-width: 1100px) { .settings-grid { grid-template-columns: 1fr; } }
</style>
