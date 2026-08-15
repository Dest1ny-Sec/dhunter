<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import UiCard from '../components/ui/UiCard.vue'
import UiButton from '../components/ui/UiButton.vue'

const route = useRoute()

interface Hit {
  id: string
  run_id: string
  role: string
  event_type: string
  content: string
  created_at: string
  target_id?: string
  target?: string
}

const router = useRouter()
const q = ref('')
const hits = ref<Hit[]>([])
const searching = ref(false)
const searched = ref(false)

function roleLabel(h: Hit): string {
  const t = h.event_type || h.role
  const map: Record<string, string> = {
    response_delta: 'AI 回复',
    reasoning_delta: '推理',
    tool_call: '工具调用',
    tool_result: '工具结果',
    message_done: 'AI 回复',
    token_usage: '用量',
    run_done: '运行结束',
    system: '系统',
    user: '用户',
    assistant: 'AI',
    tool: '工具',
  }
  return map[t] || t
}

function roleKind(h: Hit): string {
  if (h.event_type === 'reasoning_delta') return 'reasoning'
  if (h.event_type === 'tool_call' || h.event_type === 'tool_result') return 'tool'
  if (h.event_type === 'response_delta' || h.event_type === 'message_done') return 'assistant'
  return 'system'
}

async function search() {
  const query = q.value.trim()
  if (!query) return
  searching.value = true
  searched.value = true
  try {
    const res = await api.get('/search/messages', { params: { q: query } })
    hits.value = res.data?.hits || []
  } catch {
    hits.value = []
  } finally {
    searching.value = false
  }
}

// 从全局搜索框（⌘K / 回车）带 ?q= 跳转过来时，自动填入并搜索。
onMounted(() => {
  const preset = (route.query.q as string) || ''
  if (preset) {
    q.value = preset
    search()
  }
})

function openRun(h: Hit) {
  router.push(`/runs/${h.run_id}`)
}

function fmtTime(s?: string): string {
  if (!s) return '—'
  const d = new Date(s)
  return isNaN(d.getTime()) ? s : d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function snippet(c: string): string {
  const s = c.replace(/\s+/g, ' ').trim()
  return s.length > 160 ? s.slice(0, 160) + '…' : s
}
</script>

<template>
  <div class="col">
    <h2 style="font-size: 20px; font-weight: 600; margin: 0">历史对话搜索</h2>
    <p class="muted" style="font-size: 13px; margin: 6px 0 14px">
      跨所有运行全文搜索 AI 的思考、工具调用与回复（trigram FTS，支持中文）
    </p>

    <div class="row" style="gap: 8px; margin-bottom: 14px">
      <input
        v-model="q"
        placeholder="输入关键词，例如：验证码 / SQLi / checkNeedCaptcha / isNeed…"
        style="flex: 1; max-width: 520px"
        @keyup.enter="search"
      />
      <UiButton variant="primary" :disabled="searching" @click="search">
        {{ searching ? '搜索中…' : '搜索' }}
      </UiButton>
    </div>

    <div v-if="searched && !searching" class="muted" style="font-size: 12px; margin-bottom: 10px">
      共 {{ hits.length }} 条结果
    </div>

    <div v-if="hits.length" class="col" style="gap: 8px">
      <UiCard v-for="h in hits" :key="h.id" class="hit-card" @click="openRun(h)">
        <div class="hit-head">
          <span class="hit-role" :class="roleKind(h)">{{ roleLabel(h) }}</span>
          <span class="muted" style="font-size: 11px; font-family: 'JetBrains Mono', monospace">{{ h.target || h.run_id.slice(0, 8) }}</span>
          <span class="muted" style="font-size: 11px">{{ fmtTime(h.created_at) }}</span>
        </div>
        <div class="hit-body">{{ snippet(h.content) }}</div>
      </UiCard>
    </div>
    <div v-else-if="searched && !searching" class="muted" style="font-size: 13px">没有匹配的对话记录</div>
  </div>
</template>

<style scoped>
.hit-card { cursor: pointer; }
.hit-card:hover { border-color: var(--accent); }
.hit-head { display: flex; align-items: center; gap: 10px; margin-bottom: 6px; }
.hit-role {
  font-size: 11px; font-weight: 600;
  padding: 1px 8px; border-radius: 999px;
  background: var(--bg-elev-2); border: 1px solid var(--border);
}
.hit-role.assistant { color: var(--ok); }
.hit-role.reasoning { color: var(--accent); }
.hit-role.tool { color: var(--warning); }
.hit-body { font-size: 13px; color: var(--text); line-height: 1.5; white-space: pre-wrap; }
</style>
