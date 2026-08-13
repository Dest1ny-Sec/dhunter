<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'

const STORAGE_KEY = 'dhunter_settings_v1'

interface Settings {
  llm_provider: string
  llm_model: string
  llm_base_url: string
  llm_api_key: string
  webhunter_url: string
  webhunter_token: string
  agent_url: string
}

const settings = ref<Settings>({
  llm_provider: 'openai',
  llm_model: 'gpt-4o',
  llm_base_url: '',
  llm_api_key: '',
  webhunter_url: 'http://127.0.0.1:9090',
  webhunter_token: '',
  agent_url: 'http://127.0.0.1:8081',
})

const webhunterStatus = ref<'unknown' | 'connected' | 'disconnected' | 'checking'>('unknown')
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

async function testWebhunter() {
  webhunterStatus.value = 'checking'
  try {
    const res = await api.get('/tools/webhunter/health', { timeout: 5000 }).catch(() => null)
    if (res && res.status >= 200 && res.status < 300) {
      webhunterStatus.value = 'connected'
    } else {
      // Fallback: try direct fetch
      const ok = await fetch(settings.value.webhunter_url + '/health', { method: 'GET' }).then(
        (r) => r.ok,
        () => false
      )
      webhunterStatus.value = ok ? 'connected' : 'disconnected'
    }
  } catch {
    webhunterStatus.value = 'disconnected'
  }
}

const statusLabel = {
  unknown: '—',
  connected: 'connected',
  disconnected: 'disconnected',
  checking: 'checking...',
} as const

const statusColor = {
  unknown: 'var(--text-dim)',
  connected: 'var(--green)',
  disconnected: 'var(--red)',
  checking: 'var(--accent)',
} as const

onMounted(() => {
  load()
})
</script>

<template>
  <div class="col" style="max-width: 720px">
    <h2 style="font-size: 18px; font-weight: 500">Settings</h2>
    <div class="muted" style="font-size: 13px">
      Configuration is stored in browser local storage for the MVP. Backend persistence coming later.
    </div>

    <div class="card col">
      <div style="font-weight: 500">LLM</div>
      <div class="row" style="gap: 12px">
        <label class="col" style="flex: 1; gap: 4px">
          <span class="muted" style="font-size: 12px">Provider</span>
          <select v-model="settings.llm_provider">
            <option value="openai">OpenAI</option>
            <option value="anthropic">Anthropic</option>
            <option value="azure">Azure OpenAI</option>
            <option value="ollama">Ollama</option>
            <option value="custom">Custom</option>
          </select>
        </label>
        <label class="col" style="flex: 1; gap: 4px">
          <span class="muted" style="font-size: 12px">Model</span>
          <input v-model="settings.llm_model" />
        </label>
      </div>
      <label class="col" style="gap: 4px">
        <span class="muted" style="font-size: 12px">Base URL (optional)</span>
        <input v-model="settings.llm_base_url" placeholder="https://api.openai.com/v1" />
      </label>
      <label class="col" style="gap: 4px">
        <span class="muted" style="font-size: 12px">API Key</span>
        <input v-model="settings.llm_api_key" type="password" placeholder="sk-..." />
      </label>
    </div>

    <div class="card col">
      <div class="row">
        <div style="font-weight: 500">MCP Tools — WebHunter</div>
        <div class="spacer" />
        <span class="pill" :style="{ color: statusColor[webhunterStatus], borderColor: statusColor[webhunterStatus] }">
          {{ statusLabel[webhunterStatus] }}
        </span>
      </div>
      <label class="col" style="gap: 4px">
        <span class="muted" style="font-size: 12px">WebHunter URL</span>
        <input v-model="settings.webhunter_url" />
      </label>
      <label class="col" style="gap: 4px">
        <span class="muted" style="font-size: 12px">Token</span>
        <input v-model="settings.webhunter_token" type="password" />
      </label>
      <div>
        <button @click="testWebhunter">Test connection</button>
      </div>
    </div>

    <div class="card col">
      <div style="font-weight: 500">Agent</div>
      <label class="col" style="gap: 4px">
        <span class="muted" style="font-size: 12px">Agent service URL</span>
        <input v-model="settings.agent_url" />
      </label>
    </div>

    <div v-if="error" style="color: var(--red)">{{ error }}</div>
    <div class="row">
      <button class="primary" @click="save">Save</button>
      <span v-if="saved" style="color: var(--green); font-size: 13px">Saved.</span>
    </div>
  </div>
</template>
