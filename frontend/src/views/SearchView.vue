<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import { onEnter } from '../utils/ime'
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

// Static "hot" keywords — the most common things security folks search
// for after a run. Real suggestion-from-history would require tracking,
// but this gives the empty state a sense of life.
const hotKeywords = ['SQL 注入', 'XSS', '验证码', 'isNeedCaptcha', 'checkNeedCaptcha', '鉴权绕过', 'IDOR', 'nuclei', 'CORS', 'admin']
function quickSearch(k: string) { q.value = k; search() }
</script>

<template>
  <div class="col">
    <h2 class="page-title">历史对话搜索</h2>
    <p class="muted" style="font-size: 13px; margin: 6px 0 14px">
      跨所有运行全文搜索 AI 的思考、工具调用与回复（trigram FTS，支持中文）
    </p>

    <div class="row" style="gap: 8px; margin-bottom: 14px">
      <input
        v-model="q"
        placeholder="输入关键词，例如：验证码 / SQLi / checkNeedCaptcha / isNeed…"
        style="flex: 1; max-width: 520px"
        @keyup.enter="onEnter(search)"
      />
      <UiButton variant="primary" :disabled="searching" @click="search">
        {{ searching ? '搜索中…' : '搜索' }}
      </UiButton>
    </div>

    <div v-if="searched && !searching" class="muted result-count">
      共 <b>{{ hits.length }}</b> 条结果
    </div>

    <div v-if="hits.length" class="col hits-list">
      <UiCard v-for="h in hits" :key="h.id" class="hit-card" @click="openRun(h)">
        <div class="hit-head">
          <span class="hit-role" :class="roleKind(h)">{{ roleLabel(h) }}</span>
          <span class="muted hit-target">{{ h.target || h.run_id.slice(0, 8) }}</span>
          <span class="muted hit-time">{{ fmtTime(h.created_at) }}</span>
        </div>
        <div class="hit-body">{{ snippet(h.content) }}</div>
      </UiCard>
    </div>
    <div v-else-if="searched && !searching" class="empty-result">
      <div class="empty-result-icon">∅</div>
      <div class="empty-result-text">没有匹配的对话记录</div>
      <div class="empty-result-hint">试试更短的关键词，或换用其他相关词</div>
    </div>
    <div v-else class="search-hints">
      <div class="hint-group">
        <div class="hint-label">
          <span class="hint-dot" style="--c: var(--stellar-bright)"></span>
          热门关键词
        </div>
        <div class="hint-chips">
          <button v-for="(k, i) in hotKeywords" :key="k" class="hint-chip" :style="{ '--d': i * 30 + 'ms' }" @click="quickSearch(k)">
            {{ k }}
          </button>
        </div>
      </div>
      <div class="hint-group">
        <div class="hint-label">
          <span class="hint-dot" style="--c: var(--aurora)"></span>
          搜索技巧
        </div>
        <ul class="hint-tips">
          <li><code>关键词</code> · 直接搜，如 <code>SQLi</code>、<code>验证码</code></li>
          <li><code>tool:tool_name</code> · 限定工具调用，如 <code>tool:nuclei</code></li>
          <li><code>run:r-001</code> · 限定单次 run 内搜索</li>
        </ul>
      </div>
    </div>
  </div>
</template>

<style scoped>
.result-count {
  font-size: 12px; margin-bottom: 14px;
  font-family: var(--font-mono); font-variant-numeric: tabular-nums;
}
.result-count b { color: var(--text); font-weight: 600; }

.hits-list { gap: 8px; }
.hit-card { cursor: pointer; transition: border-color 0.15s, transform 0.15s; }
.hit-card:hover {
  border-color: var(--border-bright);
  transform: translateX(2px);
}
.hit-head { display: flex; align-items: center; gap: 10px; margin-bottom: 6px; }
.hit-target, .hit-time { font-size: 11px; font-family: 'JetBrains Mono', monospace; }
.hit-time { margin-left: auto; }
.hit-role {
  font-size: 10.5px; font-weight: 600;
  padding: 2px 9px; border-radius: 4px;
  background: var(--bg-elev-2); border: 1px solid var(--border);
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.hit-role.assistant { color: var(--ok); border-color: rgba(95, 200, 154, 0.34); }
.hit-role.reasoning { color: var(--nebula-bright); border-color: rgba(194, 179, 255, 0.34); }
.hit-role.tool { color: var(--star-amber-bright); border-color: rgba(232, 200, 121, 0.34); }
.hit-role.system { color: var(--text-dim); }
.hit-body { font-size: 13px; color: var(--text); line-height: 1.55; white-space: pre-wrap; }

/* empty result (after a search that returned 0) */
.empty-result {
  text-align: center;
  padding: 60px 20px;
  color: var(--text-dim);
}
.empty-result-icon {
  font-size: 36px; color: var(--text-faint);
  width: 64px; height: 64px;
  border: 1px solid var(--border);
  border-radius: 50%;
  display: inline-flex; align-items: center; justify-content: center;
  margin-bottom: 16px;
  font-family: var(--font-serif);
}
.empty-result-text { font-family: var(--font-serif); font-size: 18px; color: var(--text); margin-bottom: 6px; }
.empty-result-hint { font-size: 12px; }

/* pre-search hints (when input is empty) */
.search-hints {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 32px;
  padding: 28px 0 0;
  margin-top: 8px;
}
.hint-label {
  font-size: 10.5px; letter-spacing: 0.16em; text-transform: uppercase;
  color: var(--text-faint);
  font-weight: 600;
  display: flex; align-items: center; gap: 8px;
  margin-bottom: 14px;
}
.hint-dot {
  width: 6px; height: 6px; border-radius: 50%;
  background: var(--c, var(--stellar));
  box-shadow: 0 0 6px var(--c, var(--stellar));
}
.hint-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.hint-chip {
  font-size: 12px;
  padding: 5px 12px;
  background: rgba(125, 146, 232, 0.06);
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--text-dim);
  cursor: pointer;
  transition: all 0.15s;
  animation: hint-rise 0.4s ease-out backwards;
  animation-delay: var(--d, 0ms);
}
.hint-chip:hover {
  color: var(--text);
  background: rgba(125, 146, 232, 0.16);
  border-color: var(--border-bright);
  transform: translateY(-1px);
}
.hint-tips {
  list-style: none; padding: 0; margin: 0;
  display: flex; flex-direction: column; gap: 8px;
}
.hint-tips li {
  font-size: 12.5px; color: var(--text-dim);
  display: flex; align-items: center; gap: 8px;
}
.hint-tips code {
  font-family: var(--font-mono);
  font-size: 11px;
  padding: 1px 6px;
  background: rgba(125, 146, 232, 0.1);
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--stellar-bright);
}
@keyframes hint-rise {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: translateY(0); }
}

@media (max-width: 720px) {
  .search-hints { grid-template-columns: 1fr; gap: 24px; }
}
</style>
