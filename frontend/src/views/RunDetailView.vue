<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Marked } from 'marked'
import hljs from 'highlight.js'
import 'highlight.js/styles/github-dark.css'
import EventStream, { type SSEEvent } from '../components/EventStream.vue'
import SeverityBadge from '../components/SeverityBadge.vue'
import BoardView from '../components/BoardView.vue'
import UiBadge from '../components/ui/UiBadge.vue'
import UiProgress from '../components/ui/UiProgress.vue'
import UiEmpty from '../components/ui/UiEmpty.vue'
import { api } from '../api/client'

const route = useRoute()
const runId = computed(() => route.params.id as string)

const tab = ref<'board' | 'stream' | 'report'>('board')
const events = ref<SSEEvent[]>([])
const runInfo = ref<any>(null)
const vulns = ref<any[]>([])
const report = ref<string>('')
const status = ref<string>('pending')
const error = ref<string | null>(null)
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
  try { return md.parse(report.value) as string } catch { return `<pre>${report.value}</pre>` }
})

const toolCalls = computed(() =>
  events.value
    .filter((e) => e.type === 'tool_call' || e.type === 'tool_result')
    .map((e, idx, arr) => {
      if (e.type === 'tool_call') {
        const result = arr.find((x, j) => j > idx && x.type === 'tool_result' && x.data?.call_id === e.data?.call_id)
        return { call: e, result }
      }
      return null
    })
    .filter(Boolean) as Array<{ call: SSEEvent; result?: SSEEvent }>
)

const totalTokens = computed(() =>
  (runInfo.value?.input_tokens || 0) + (runInfo.value?.output_tokens || 0) + (runInfo.value?.cache_read_input_tokens || 0)
)
function fmtN(n?: number): string {
  if (n == null) return '—'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}
function fmtTime(s?: string) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString() } catch { return s }
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
      const ev: SSEEvent = { type: parsed.type || parsed.event || 'message', data: parsed.data ?? parsed, ts: parsed.ts || Date.now() }
      events.value.push(ev)
      if (ev.type === 'run_status' && ev.data?.status) status.value = ev.data.status
      if (ev.type === 'run_complete' || ev.type === 'run_finished') {
        status.value = 'completed'
        if (typeof ev.data === 'object' && ev.data?.report) report.value = ev.data.report
        if (Array.isArray(ev.data?.vulns)) vulns.value = ev.data.vulns
      }
      if (ev.type === 'run_failed' || ev.type === 'error') status.value = 'failed'
      if (ev.type === 'vulnerability' || ev.type === 'vuln') vulns.value.push(ev.data)
      if (ev.type === 'report_delta') {
        const d = ev.data
        report.value += typeof d === 'string' ? d : d?.delta || ''
      }
    } catch {}
  }
}

onMounted(async () => { await loadRun(); connectSSE() })
onBeforeUnmount(() => { if (sseRef.value) { sseRef.value.close(); sseRef.value = null } })
watch(runId, async (v) => {
  if (v) { events.value = []; vulns.value = []; report.value = ''; await loadRun(); connectSSE() }
})
</script>

<template>
  <div class="col" style="height: 100%">
    <div class="run-head">
      <div class="row">
        <h2 style="font-size: 16px; font-weight: 600; margin: 0">Run <code>{{ runId.slice(0, 8) }}</code></h2>
        <UiBadge kind="status" :value="status" :dot="true" />
      </div>
      <div class="run-tokens">
        <UiProgress v-if="totalTokens > 0" :value="totalTokens" :max="Math.max(totalTokens, 1)" tone="accent" label="tokens" />
        <span class="muted" style="font-size: 12px">
          in {{ fmtN(runInfo?.input_tokens) }} · out {{ fmtN(runInfo?.output_tokens) }} · cache {{ fmtN(runInfo?.cache_read_input_tokens) }}
        </span>
      </div>
      <span class="muted" style="font-size: 12px">{{ events.length }} events</span>
    </div>

    <div v-if="error" style="color: var(--danger)">{{ error }}</div>

    <div class="tabs">
      <button :class="['tab', { active: tab === 'board' }]" @click="tab = 'board'">攻击图</button>
      <button :class="['tab', { active: tab === 'stream' }]" @click="tab = 'stream'">实时事件</button>
      <button :class="['tab', { active: tab === 'report' }]" @click="tab = 'report'">报告</button>
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
</style>
