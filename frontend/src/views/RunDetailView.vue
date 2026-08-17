<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Marked } from 'marked'
import DOMPurify from 'dompurify'
import { escapeHtml } from '../utils/format'
// highlight.js core + a small language set keeps the 1.3MB RunDetail bundle
// lean — full hljs ships ~30 languages the report never uses.
import hljs from 'highlight.js/lib/core'
import 'highlight.js/styles/github-dark.css'
import bash from 'highlight.js/lib/languages/bash'
import json from 'highlight.js/lib/languages/json'
import http from 'highlight.js/lib/languages/http'
import javascript from 'highlight.js/lib/languages/javascript'
import xml from 'highlight.js/lib/languages/xml'
import sql from 'highlight.js/lib/languages/sql'
import markdown from 'highlight.js/lib/languages/markdown'
import ini from 'highlight.js/lib/languages/ini'
import plaintext from 'highlight.js/lib/languages/plaintext'
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('json', json)
hljs.registerLanguage('http', http)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('ini', ini)
hljs.registerLanguage('plaintext', plaintext)
import EventStream, { type SSEEvent } from '../components/EventStream.vue'
import SeverityBadge from '../components/SeverityBadge.vue'
// Vue Flow 攻击图只在「攻击图」tab 打开时才加载（懒加载，避免拖慢首屏）
const BoardView = defineAsyncComponent(() => import('../components/BoardView.vue'))
import UiBadge from '../components/ui/UiBadge.vue'
import UiProgress from '../components/ui/UiProgress.vue'
import UiEmpty from '../components/ui/UiEmpty.vue'
import UiSkeleton from '../components/ui/UiSkeleton.vue'
import Icon from '../components/icons/Icon.vue'
import { api } from '../api/client'
import { notify, requestNotifyPermission } from '../utils/notify'
import { estimateCostCNY, fmtCostCNY } from '../utils/cost'

const router = useRouter()
const route = useRoute()

const runId = computed(() => route.params.id as string)

const tab = ref<'results' | 'tools' | 'board' | 'stream' | 'report'>('results')
const events = ref<SSEEvent[]>([])
const toolActivity = ref<any[]>([])
let toolTimer: number | null = null

async function loadTools() {
  try {
    const res = await api.get(`/runs/${runId.value}/tool_calls`)
    toolActivity.value = Array.isArray(res.data) ? res.data : res.data?.tool_calls || []
  } catch {}
}
const runInfo = ref<any>(null)
const vulns = ref<any[]>([])
const report = ref<string>('')
const expandedResults = ref<Record<string, boolean>>({})
const status = ref<string>('pending')
const error = ref<string | null>(null)
const llmModel = ref('')
const sseRef = ref<EventSource | null>(null)

const md = new Marked({ gfm: true, breaks: true })
md.use({
  renderer: {
    code(this: any, code: any) {
      const c = typeof code === 'object' ? code.text : code
      const lang = typeof code === 'object' ? code.lang : undefined
      let highlighted = c
      if (lang && hljs.getLanguage(lang)) {
        try { highlighted = hljs.highlight(c, { language: lang }).value } catch {}
      } else {
        try { highlighted = hljs.highlightAuto(c).value } catch {}
      }
      return `<pre><code class="hljs">${highlighted}</code></pre>`
    },
  },
})

const reportHtml = computed(() => {
  if (!report.value) return ''
  try {
    // The report/evidence is largely copied from target responses, which are
    // attacker-influenced data. marked passes raw HTML through unchanged, so
    // every render MUST go through DOMPurify — otherwise a target page that
    // makes the agent copy <img onerror=...> into a finding executes in the
    // admin's browser (stored XSS → token theft).
    return DOMPurify.sanitize(md.parse(report.value) as string)
  } catch {
    return `<pre>${escapeHtml(report.value)}</pre>`
  }
})

// tool_calls paired with their results, maintained INCREMENTALLY as SSE
// events arrive. The old computed re-scanned the whole events array (with an
// O(n) find per tool_call) on EVERY event — quadratic as the stream grows,
// which made the page grind on long runs.
const toolCalls = ref<Array<{ call: SSEEvent; result?: SSEEvent }>>([])
const MAX_TOOL_ROWS = 200

function trackToolCall(ev: SSEEvent) {
  if (ev.type === 'tool_call') {
    toolCalls.value.push({ call: ev })
    if (toolCalls.value.length > MAX_TOOL_ROWS) {
      toolCalls.value.splice(0, toolCalls.value.length - MAX_TOOL_ROWS)
    }
  } else if (ev.type === 'tool_result') {
    const cid = ev.data?.call_id
    let pending: { call: SSEEvent; result?: SSEEvent } | undefined
    if (cid) {
      for (let i = toolCalls.value.length - 1; i >= 0; i--) {
        const t = toolCalls.value[i]
        if (!t.result && t.call.data?.call_id === cid) { pending = t; break }
      }
    }
    if (pending) pending.result = ev
    else toolCalls.value.push({ call: ev as unknown as SSEEvent })
  }
}

const totalTokens = computed(() =>
  (runInfo.value?.input_tokens || 0) + (runInfo.value?.output_tokens || 0) + (runInfo.value?.cache_read_input_tokens || 0)
)
// 缓存命中率 = 缓存读取 token / (新鲜输入 + 缓存读取) — 命中越高越省钱。
const cacheHitRate = computed(() => {
  const read = runInfo.value?.cache_read_input_tokens || 0
  const fresh = runInfo.value?.input_tokens || 0
  const total = read + fresh
  return total > 0 ? Math.round((read / total) * 100) : 0
})
// Token 预算红线（设置页配置，0=不限）：实时展示本次 run 已消耗多少 / 还剩多少。
const budget = ref(0)
const budgetInfo = computed(() => {
  if (!budget.value || !runInfo.value) return null
  const spent = (runInfo.value.input_tokens || 0) + (runInfo.value.output_tokens || 0)
  const pct = Math.min(100, Math.round((spent / budget.value) * 100))
  return { spent, pct, remaining: Math.max(0, budget.value - spent) }
})
// 粗略成本估算（按常见模型单价，仅供预算参考）
const runCost = computed(() => estimateCostCNY(runInfo.value || {}, llmModel.value))
function fmtN(n?: number): string {
  if (n == null) return '—'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}
function toolArgUrl(t: any): string {
  const a = t.arguments
  if (a && typeof a === 'object') return a.url || a.domain || a.target || ''
  return ''
}

function fmtTime(s?: string) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString() } catch { return s }
}

async function continueRun() {
  if (!confirm('从当前黑板状态继续深入这个 run？(保留已发现的 facts/intents，开启新一轮挖掘)')) return
  try {
    await api.post(`/runs/${runId.value}/continue`)
    status.value = 'running'
    await loadRun()
    connectSSE()
  } catch (e: any) {
    alert('继续失败: ' + (e?.response?.data?.error || e?.message))
  }
}

async function pauseRun() {
  if (!confirm('暂停这个 run？已发现的 facts/intents 会保留，可随时点「继续深入」恢复。')) return
  try {
    await api.post(`/runs/${runId.value}/pause`)
    status.value = 'paused'
    await loadRun()
  } catch (e: any) {
    alert('暂停失败: ' + (e?.response?.data?.error || e?.message))
  }
}

async function loadRun() {
  try {
    const res = await api.get(`/runs/${runId.value}`)
    runInfo.value = res.data
    status.value = res.data?.status || 'pending'
  } catch (e: any) {
    error.value = e?.message || 'failed to load'
  }
  try {
    const [vRes, rRes] = await Promise.all([
      api.get(`/runs/${runId.value}/vulnerabilities`),
      api.get(`/runs/${runId.value}/report`),
    ])
    vulns.value = Array.isArray(vRes.data) ? vRes.data : vRes.data?.vulnerabilities || []
    if (typeof rRes.data === 'string') report.value = rRes.data
    else if (rRes.data?.report) report.value = rRes.data.report
    else if (rRes.data?.markdown) report.value = rRes.data.markdown
  } catch {}
}

function connectSSE() {
  if (sseRef.value) { sseRef.value.close(); sseRef.value = null }
  const token = localStorage.getItem('dhunter_token') || ''
  const es = new EventSource(`/api/runs/${runId.value}/events?token=${encodeURIComponent(token)}`, { withCredentials: false })
  sseRef.value = es
  es.onmessage = (msg) => {
    try {
      const parsed = JSON.parse(msg.data)
      // The bridge emits both `type` and `event_type` (Go-side wire name);
      // accept all three so a mismatched field can never silently turn
      // every event into an untyped 'message' again.
      const ev: SSEEvent = { type: parsed.type || parsed.event || parsed.event_type || 'message', data: parsed.data ?? parsed, ts: parsed.ts || Date.now() }
      // response_delta / reasoning_delta arrive PER CHUNK but each carries
      // the FULL accumulated text (protocol behavior). Rendering every chunk
      // as its own row makes the stream look like the same sentence repeated
      // N times, growing by a few characters each — so merge consecutive
      // deltas of the same type into ONE row (a typing effect): the row's
      // content updates in place to the latest accumulated text.
      if (ev.type === 'response_delta' || ev.type === 'reasoning_delta') {
        const last = events.value[events.value.length - 1]
        if (last && last.type === ev.type) {
          events.value[events.value.length - 1] = { ...last, data: ev.data, ts: ev.ts ?? last.ts }
        } else {
          events.value.push(ev)
        }
      } else {
        events.value.push(ev)
      }
      // Long runs stream one event per delta chunk — cap the in-memory list
      // so the live-stream tab doesn't grind to a halt after thousands of
      // events. The DB keeps the full history (tool_calls / messages).
      if (events.value.length > 500) {
        events.value.splice(0, events.value.length - 500)
      }
      // Incremental tool_call/tool_result pairing (O(1)/O(n) per event,
      // not O(n²) across the whole stream).
      trackToolCall(ev)
      if (ev.type === 'run_status' && ev.data?.status) status.value = ev.data.status
      if (ev.type === 'run_done') {
        const st = ev.data?.run_status || ev.data?.status
        if (st) status.value = st
        if (sseRef.value) { sseRef.value.close(); sseRef.value = null }
        // Desktop notification when the run finishes (permission requested
        // on page mount), so the user doesn't have to stare at the tab.
        const label = st === 'success' || st === 'completed' ? '运行完成' : st === 'failed' ? '运行失败' : `运行结束（${st}）`
        notify(label, (ev.data?.content || ev.data?.summary || runInfo.value?.summary || '').slice(0, 120) || undefined)
      }
      if (ev.type === 'run_complete' || ev.type === 'run_finished') {
        status.value = 'completed'
        if (typeof ev.data === 'object' && ev.data?.report) report.value = ev.data.report
        if (Array.isArray(ev.data?.vulns)) vulns.value = ev.data.vulns
      }
      if (ev.type === 'run_failed' || ev.type === 'error') status.value = 'failed'
      if (ev.type === 'run_cancelled') status.value = 'cancelled'
      // terminal — stop reconnecting
      if (ev.type === 'run_complete' || ev.type === 'run_finished' || ev.type === 'run_failed' || ev.type === 'error' || ev.type === 'run_cancelled' || ev.type === 'run_done') {
        if (sseRef.value) { sseRef.value.close(); sseRef.value = null }
      }
      if (ev.type === 'vulnerability' || ev.type === 'vuln') vulns.value.push(ev.data)
      if (ev.type === 'report_delta') {
        const d = ev.data
        report.value += typeof d === 'string' ? d : d?.delta || ''
      }
    } catch {}
  }
}

onMounted(async () => {
  requestNotifyPermission() // ask once; used when the run finishes
  await loadRun()
  connectSSE()
  loadTools()
  // Poll the tool-activity list less aggressively, and only while the
  // 工具活动 tab is actually open — rebuilding that table every 3s from any
  // tab was a big part of long-run page lag.
  toolTimer = window.setInterval(() => { if (tab.value === 'tools') loadTools() }, 6000)
  watch(tab, (t) => { if (t === 'tools') loadTools() })
  try {
    const [llmRes, budRes] = await Promise.all([
      api.get('/settings/llm'), api.get('/settings/budget'),
    ])
    llmModel.value = llmRes.data?.model || ''
    budget.value = budRes.data?.max_run_tokens || 0
  } catch { /* cost estimate falls back to default price */ }
})
onBeforeUnmount(() => {
  if (toolTimer) window.clearInterval(toolTimer)
})
onBeforeUnmount(() => { if (sseRef.value) { sseRef.value.close(); sseRef.value = null } })
watch(runId, async (v) => {
  if (v) {
    // reset per-run state, always land on the findings-first 成果 tab
    tab.value = 'results'
    expandedResults.value = {}
    events.value = []
    toolCalls.value = []
    vulns.value = []
    report.value = ''
    toolActivity.value = []
    await loadRun()
    connectSSE()
    loadTools()
  }
})
</script>

<template>
  <div class="col" style="height: 100%">
    <button class="back-btn" @click="router.back()" aria-label="返回">
      <Icon name="arrow-left" :size="14" />
      <span>返回</span>
    </button>
    <div class="run-head">
      <div class="row">
        <h2 style="font-size: 16px; font-weight: 600; margin: 0">Run <code>{{ runId.slice(0, 8) }}</code></h2>
        <UiBadge kind="status" :value="status" :dot="true" />
        <button v-if="['running', 'pending'].includes(status)" style="min-height: 28px; padding: 0 10px; font-size: 12px" @click="pauseRun">⏸ 暂停</button>
        <button v-if="['success','completed','failed','cancelled','paused'].includes(status)" style="min-height: 28px; padding: 0 10px; font-size: 12px" @click="continueRun">{{ status === 'paused' ? '▶ 继续' : '继续深入' }}</button>
      </div>
      <div class="run-tokens">
        <UiProgress v-if="totalTokens > 0" :value="totalTokens" :max="Math.max(totalTokens, 1)" tone="accent" label="tokens" />
        <span class="muted" style="font-size: 12px">
          in {{ fmtN(runInfo?.input_tokens) }} · out {{ fmtN(runInfo?.output_tokens) }}
          · reasoning {{ fmtN(runInfo?.reasoning_tokens) }}
          · 缓存命中 <b style="color: var(--ok)">{{ cacheHitRate }}%</b>
          · 成本 ≈ <b style="color: var(--warning)">{{ fmtCostCNY(runCost) }}</b>
          <template v-if="budgetInfo">
            · 预算 <b :style="{ color: budgetInfo.pct >= 90 ? 'var(--danger)' : budgetInfo.pct >= 60 ? 'var(--warn)' : 'var(--ok)' }">
              {{ fmtN(budgetInfo.spent) }} / {{ fmtN(budget) }}（{{ budgetInfo.pct }}%）
            </b>
          </template>
        </span>
      </div>
      <span class="muted" style="font-size: 12px">{{ events.length }} events</span>
    </div>

    <div v-if="error" style="color: var(--danger)">{{ error }}</div>

    <!-- run-level outcome: convergence summary, failure reason, or the
         time/budget-stop notice — shown prominently so a failed run isn't
         a silent badge flip. -->
    <div v-if="runInfo?.summary" class="card" style="padding: 10px 14px; margin: 6px 0 10px; font-size: 13px">
      <span class="muted" style="margin-right: 8px">{{ ['failed', 'cancelled'].includes(status) ? '✕' : 'ℹ' }}</span>{{ runInfo.summary }}
    </div>

    <div class="tabs">
      <button :class="['tab', { active: tab === 'results' }]" @click="tab = 'results'">成果</button>
      <button :class="['tab', { active: tab === 'tools' }]" @click="tab = 'tools'">工具活动 ({{ toolActivity.length }})</button>
      <button :class="['tab', { active: tab === 'board' }]" @click="tab = 'board'">攻击图</button>
      <button :class="['tab', { active: tab === 'stream' }]" @click="tab = 'stream'">实时事件</button>
      <button :class="['tab', { active: tab === 'report' }]" @click="tab = 'report'">报告</button>
    </div>

    <div v-if="tab === 'results'" class="col">
      <div class="row">
        <h3 style="font-size: 15px; font-weight: 600">漏洞成果（{{ vulns.length }}）</h3>
        <span class="spacer" />
        <span class="muted" style="font-size: 12px">confirmed {{ vulns.filter(v => v.status === 'confirmed').length }} · dismissed {{ vulns.filter(v => v.status === 'dismissed').length }}</span>
      </div>
      <div v-if="vulns.length" class="card" style="padding: 0">
        <table>
          <thead><tr><th>严重度</th><th>漏洞</th><th>状态</th><th></th></tr></thead>
          <tbody>
            <template v-for="v in vulns" :key="v.id">
              <tr>
                <td><SeverityBadge :severity="v.severity || 'info'" /></td>
                <td>{{ v.title || v.name || v.id }}</td>
                <td><UiBadge kind="status" :value="v.status || 'open'" /></td>
                <td><button style="min-height: 26px; padding: 0 8px; font-size: 11px" @click="expandedResults[v.id] = !expandedResults[v.id]">{{ expandedResults[v.id] ? '收起' : '详情' }}</button></td>
              </tr>
              <tr v-if="expandedResults[v.id]">
                <td colspan="4" style="background: var(--bg-elev-2); padding: 12px">
                  <div class="muted" style="font-size: 12px; margin-bottom: 6px"><code>{{ v.target || '—' }}</code></div>
                  <template v-if="v.reproduction"><div class="muted" style="font-size: 11px; margin-bottom: 4px">复现步骤</div><pre style="max-height: 220px; overflow:auto; margin-bottom: 10px"><code>{{ v.reproduction }}</code></pre></template>
                  <template v-if="v.evidence"><div class="muted" style="font-size: 11px; margin-bottom: 4px">证据</div><pre style="max-height: 220px; overflow:auto"><code>{{ typeof v.evidence === 'string' ? v.evidence : JSON.stringify(v.evidence, null, 2) }}</code></pre></template>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
      <div class="muted" style="font-size: 13px" v-else>暂无漏洞成果</div>
      <button class="tab" @click="tab = 'report'">查看完整 Markdown 报告 →</button>
    </div>

    <div v-if="tab === 'tools'" class="card" style="padding: 0">
      <div v-if="toolActivity.length" class="tool-log">
        <div v-for="t in toolActivity" :key="t.id" class="tool-line">
          <span class="tool-name"><code>{{ t.name }}</code></span>
          <span class="tool-url muted">{{ toolArgUrl(t) }}</span>
          <span class="spacer" />
          <span class="tool-status" :style="{ color: t.is_error ? 'var(--danger)' : 'var(--ok)' }">{{ t.is_error ? 'error' : 'ok' }}</span>
          <span class="muted" style="font-size: 11px">{{ t.duration_ms }}ms</span>
        </div>
      </div>
      <div v-else class="muted" style="padding: 20px; text-align: center">暂无工具调用</div>
    </div>

    <div v-if="tab === 'board'" class="card" style="flex: 1; padding: 14px">
      <BoardView :run-id="runId" />
    </div>

    <div v-if="tab === 'stream' && (status === 'running' || status === 'pending' || events.length > 0)" class="run-panes">
      <div class="run-pane">
        <div class="run-pane-header">推理过程</div>
        <div class="run-pane-body">
          <EventStream :events="events.filter((e) => e.type === 'reasoning_delta' || e.type === 'reasoning' || e.type === 'thinking')" />
        </div>
      </div>
      <div class="run-pane">
        <div class="run-pane-header">AI 回复</div>
        <div class="run-pane-body">
          <EventStream :events="events.filter((e) => e.type === 'response_delta' || e.type === 'response' || e.type === 'message')" />
        </div>
      </div>
      <div class="run-pane">
        <div class="run-pane-header">工具调用 ({{ toolCalls.length }})</div>
        <div class="run-pane-body">
          <table v-if="toolCalls.length > 0">
            <thead><tr><th>工具</th><th>状态</th><th>时间</th></tr></thead>
            <tbody>
              <tr v-for="(tc, i) in toolCalls" :key="i">
                <td><code style="font-size: 11px">{{ tc.call.data?.name || tc.call.data?.tool || '?' }}</code></td>
                <td>
                  <span v-if="tc.result" :style="{ color: tc.result.data?.is_error ? 'var(--danger)' : 'var(--ok)' }">
                    {{ tc.result.data?.is_error ? '失败' : '成功' }}
                  </span>
                  <span v-else class="muted">等待中</span>
                </td>
                <td class="muted" style="font-size: 11px">{{ tc.call.ts ? new Date(tc.call.ts).toLocaleTimeString('zh-CN', { hour12: false }) : '—' }}</td>
              </tr>
            </tbody>
          </table>
          <div v-else class="muted" style="padding: 12px; text-align: center; font-size: 12px">暂无工具调用</div>
        </div>
      </div>
    </div>
    <UiEmpty v-else-if="tab === 'stream'" icon="⟳" message="Agent 运行时此处会显示实时事件流" />

    <div v-if="tab === 'report'" class="col" style="margin-top: 8px">
      <div class="row">
        <h3 style="font-size: 14px; font-weight: 600">报告</h3>
        <span class="spacer" />
        <span class="muted" style="font-size: 12px">{{ runInfo?.finished_at ? `完成于 ${fmtTime(runInfo.finished_at)}` : '' }}</span>
      </div>
      <div class="card markdown" v-if="report"><div v-html="reportHtml" /></div>
      <UiEmpty v-else icon="📄" message="暂无报告" />

      <h3 style="font-size: 14px; font-weight: 600; margin-top: 12px">漏洞列表（{{ vulns.length }}）</h3>
      <div v-if="vulns.length" class="card" style="padding: 0">
        <table>
          <thead><tr><th>严重度</th><th>标题</th><th>目标</th><th>状态</th></tr></thead>
          <tbody>
            <tr v-for="v in vulns" :key="v.id">
              <td><SeverityBadge :severity="v.severity || 'info'" /></td>
              <td>{{ v.title || v.name || v.id }}</td>
              <td class="muted"><code style="font-size: 11px">{{ v.target || v.url || '—' }}</code></td>
              <td><UiBadge kind="status" :value="v.status || 'open'" /></td>
            </tr>
          </tbody>
        </table>
      </div>
      <UiEmpty v-else icon="⚑" message="本次运行未发现漏洞" />
    </div>
  </div>
</template>

<style scoped>
.run-head { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; }
.run-tokens { display: flex; align-items: center; gap: 12px; min-width: 280px; flex: 1; }
.tool-log { max-height: 70vh; overflow-y: auto; }
.tool-line { display: flex; align-items: center; gap: 10px; padding: 6px 14px; border-bottom: 1px solid var(--border-soft); font-size: 12px; }
.tool-name { min-width: 120px; }
.tool-url { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tool-status { font-size: 11px; font-weight: 600; }

.back-btn {
  align-self: flex-start;
  display: inline-flex; align-items: center; gap: 6px;
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-dim);
  padding: 6px 12px;
  min-height: 30px;
  font-size: 12px;
  font-family: var(--font-display);
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s;
}
.back-btn:hover { color: var(--text); border-color: var(--border-bright); background: rgba(125, 146, 232, 0.06); }
</style>
