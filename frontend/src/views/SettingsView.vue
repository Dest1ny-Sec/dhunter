<script setup lang="ts">
import { onMounted, ref } from 'vue'
import UiCard from '../components/ui/UiCard.vue'
import UiBadge from '../components/ui/UiBadge.vue'

const STORAGE_KEY = 'dhunter_settings_v1'

interface Settings {
  llm_provider: string
  llm_model: string
  llm_base_url: string
  llm_api_key: string
  mcp_url: string
  mcp_token: string
  agent_url: string
}

const settings = ref<Settings>({
  llm_provider: 'anthropic',
  llm_model: '',
  llm_base_url: '',
  llm_api_key: '',
  mcp_url: 'http://127.0.0.1:9124',
  mcp_token: '',
  agent_url: 'http://127.0.0.1:9100',
})

const mcpStatus = ref<'unknown' | 'connected' | 'disconnected' | 'checking'>('unknown')
const saved = ref(false)
const error = ref<string | null>(null)

function load() {
  const raw = localStorage.getItem(STORAGE_KEY)
  if (raw) {
    try {
      const parsed = JSON.parse(raw)
      settings.value = { ...settings.value, ...parsed }
    } catch {}
  }
}

function save() {
  error.value = null
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(settings.value))
    saved.value = true
    setTimeout(() => (saved.value = false), 2000)
  } catch (e: any) {
    error.value = e?.message || 'Failed to save'
  }
}

async function testMCP() {
  mcpStatus.value = 'checking'
  try {
    const ok = await fetch(settings.value.mcp_url + '/health', { method: 'GET' }).then((r) => r.ok, () => false)
    mcpStatus.value = ok ? 'connected' : 'disconnected'
  } catch {
    mcpStatus.value = 'disconnected'
  }
}

onMounted(load)
</script>

<template>
  <div class="col" style="max-width: 720px">
    <h2 style="font-size: 20px; font-weight: 600; margin: 0">Settings</h2>
    <div class="muted" style="font-size: 13px">Platform connections — stored locally for now</div>

    <UiCard title="LLM provider">
      <div class="form-grid">
        <div>
          <label class="field-label">Provider</label>
          <select v-model="settings.llm_provider" style="width: 100%">
            <option value="anthropic">Anthropic-compatible</option>
            <option value="openai">OpenAI-compatible</option>
            <option value="custom">Custom</option>
          </select>
        </div>
        <div>
          <label class="field-label">Model</label>
          <input v-model="settings.llm_model" placeholder="e.g. MiniMax-M3[1M]" style="width: 100%" />
        </div>
      </div>
      <div>
        <label class="field-label">Base URL</label>
        <input v-model="settings.llm_base_url" placeholder="https://api.minimaxi.com/anthropic" style="width: 100%" />
      </div>
      <div>
        <label class="field-label">API key (stored locally)</label>
        <input v-model="settings.llm_api_key" type="password" placeholder="sk-..." style="width: 100%" />
      </div>
    </UiCard>

    <UiCard title="MCP toolbelt">
      <div class="row">
        <div class="spacer" />
        <UiBadge kind="status" :value="mcpStatus" :dot="true" />
      </div>
      <div class="form-grid">
        <div>
          <label class="field-label">MCP URL</label>
          <input v-model="settings.mcp_url" style="width: 100%" />
        </div>
        <div>
          <label class="field-label">Token</label>
          <input v-model="settings.mcp_token" type="password" style="width: 100%" />
        </div>
      </div>
      <div>
        <button @click="testMCP">Test connection</button>
      </div>
    </UiCard>

    <UiCard title="Agent service">
      <div>
        <label class="field-label">Agent URL</label>
        <input v-model="settings.agent_url" style="width: 100%" />
      </div>
    </UiCard>

    <div v-if="error" style="color: var(--danger)">{{ error }}</div>
    <div class="row">
      <button class="primary" @click="save">Save</button>
      <span v-if="saved" style="color: var(--ok); font-size: 13px">Saved ✓</span>
    </div>
  </div>
</template>

<style scoped>
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.field-label { font-size: 12px; color: var(--text-dim); margin-bottom: 4px; display: block; }
</style>
