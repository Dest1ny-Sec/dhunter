<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import UiCard from '../components/ui/UiCard.vue'
import UiBadge from '../components/ui/UiBadge.vue'

// The platform bundles its own MCP toolbelt + agent (started by the launcher),
// so the user does NOT configure them. This page auto-detects and shows the
// actual status. The only external knob is the LLM (set at deploy via env).
const status = ref<any>(null)

async function load() {
  try {
    const res = await api.get('/status')
    status.value = res.data
  } catch {}
}

onMounted(load)
</script>

<template>
  <div class="col" style="max-width: 720px">
    <h2 style="font-size: 20px; font-weight: 600; margin: 0">设置</h2>
    <div class="muted" style="font-size: 13px">平台服务状态 — 工具集与 Agent 由平台自带并自动管理，无需手动配置</div>

    <UiCard title="平台服务（自动检测）">
      <div class="svc-row">
        <div class="svc-info">
          <div class="svc-name">MCP 工具集 <span class="muted" style="font-size: 11px">({{ status?.mcp?.url || '—' }})</span></div>
          <div class="muted" style="font-size: 12px">20 个原创渗透工具，随平台启动</div>
        </div>
        <UiBadge kind="status" :value="status?.mcp?.status || 'pending'" :dot="true" />
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

    <UiCard title="大模型（部署时通过环境变量配置）">
      <div class="svc-row">
        <div class="svc-info">
          <div class="svc-name">{{ status?.llm?.provider || '—' }} · {{ status?.llm?.model || '—' }}</div>
          <div class="muted" style="font-size: 12px">{{ status?.llm?.base_url || '—' }}</div>
        </div>
        <UiBadge kind="status" :value="status?.llm?.key_set ? 'connected' : 'pending'" :dot="true" />
      </div>
      <div class="muted" style="font-size: 12px; margin-top: 8px">
        密钥通过 <code>DHUNTER_LLM_KEY</code> 环境变量注入，不存储在前端。
      </div>
    </UiCard>
  </div>
</template>

<style scoped>
.svc-row {
  display: flex; justify-content: space-between; align-items: center; gap: 16px;
  padding: 12px 0;
  border-bottom: 1px solid var(--border-soft);
}
.svc-row:last-child { border-bottom: none; }
.svc-name { font-size: 13.5px; font-weight: 500; }
</style>
