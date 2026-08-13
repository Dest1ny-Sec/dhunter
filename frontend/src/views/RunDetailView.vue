<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Marked } from 'marked'
import hljs from 'highlight.js'
import 'highlight.js/styles/github-dark.css'
import EventStream, { type SSEEvent } from '../components/EventStream.vue'
import SeverityBadge from '../components/SeverityBadge.vue'
import { api } from '../api/client'

const route = useRoute()
const runId = computed(() => route.params.id as string)

const events = ref<SSEEvent[]>([])
const runInfo = ref<any>(null)
const vulns = ref<any[]>([])
const report = ref<string>('')
const status = ref<string>('pending')
const error = ref<string | null>(null)
const sseRef = ref<EventSource | null>(null)

const md = new Marked({
  gfm: true,
  breaks: true,
})
md.use({
  renderer: {
    code(this: any, code: any) {
      const c = typeof code === 'object' ? code.text : code
      const lang = typeof code === 'object' ? code.lang : undefined
      let highlighted = c
      if (lang && hljs.getLanguage(lang)) {
        try {
          highlighted = hljs.highlight(c, { language: lang }).value
        } catch {}
      } else {
        try {
          highlighted = hljs.highlightAuto(c).value
        } catch {}
      }
      return `<pre><code class="hljs">${highlighted}</code></pre>`
    },
  },
})

const reportHtml = computed(() => {
  if (!report.value) return ''
  try {
    return md.parse(report.value) as string
  } catch {
    return `<pre>${report.value}</pre>`
  }
})

const toolCalls = computed(() =>
  events.value
    .filter((e) => e.type === 'tool_call' || e.type === 'tool_result')
    .map((e, idx, arr) => {
      if (e.type === 'tool_call') {
        const result = arr.find(
          (x, j) => j > idx && x.type === 'tool_result' && x.data?.call_id === e.data?.call_id
        )
        return { call: e, result }
      }
      return null
    })
    .filter(Boolean) as Array<{ call: SSEEvent; result?: SSEEvent }>
)

const responseText = computed(() =>
  events.value
    .filter((e) => e.type === 'response_delta')
    .map((e) => (typeof e.data === 'string' ? e.data : e.data?.text || ''))
    .join('')
)

function fmtData(d: any): string {
  if (d == null) return ''
  if (typeof d === 'string') return d
  try {
    return JSON.stringify(d, null, 2)
  } catch {
    return String(d)
  }
}

function fmtTime(s?: string) {
  if (!s) return '—'
  try {
    return new Date(s).toLocaleString()
  } catch {
    return s
  }
}

function formatN(n?: number) {
  if (n == null) return '—'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}

async function loadRun() {
  try {
    const res = await api.get(`/runs/${runId.value}`)
    runInfo.value = res.data
    status.value = res.data?.status || 'pending'
  } catch (e: any) {
    console.warn('loadRun', e)
  }
  // Load the vulnerabilities and report (separate endpoints).
  try {
    const [vRes, rRes] = await Promise.all([
      api.get(`/runs/${runId.value}/vulnerabilities`),
      api.get(`/runs/${runId.value}/report`),
    ])
    vulns.value = Array.isArray(vRes.data) ? vRes.data : vRes.data?.vulnerabilities || []
    if (typeof rRes.data === 'string') {
      report.value = rRes.data
    } else if (rRes.data?.report) {
      report.value = rRes.data.report
    } else if (rRes.data?.markdown) {
      report.value = rRes.data.markdown
    }
  } catch (e) {
    // Run still running — vulns/report may be empty
    console.warn('loadArtifacts', e)
  }
}

function connectSSE() {
  if (sseRef.value) {
    sseRef.value.close()
    sseRef.value = null
  }
  // EventSource does not support custom headers, so we pass the token
  // via the query string — the server's SSE route accepts `?token=`.
  const token = localStorage.getItem('dhunter_token') || ''
  const url = `/api/runs/${runId.value}/events?token=${encodeURIComponent(token)}`
  const es = new EventSource(url, { withCredentials: false })
  sseRef.value = es

  es.onmessage = (msg) => {
    try {
      const parsed = JSON.parse(msg.data)
      const ev: SSEEvent = {
        type: parsed.type || parsed.event || 'message',
        data: parsed.data ?? parsed,
        ts: parsed.ts || Date.now(),
      }
      events.value.push(ev)

      // Track status from events
      if (ev.type === 'run_status' && ev.data?.status) {
        status.value = ev.data.status
      }
      if (ev.type === 'run_complete' || ev.type === 'run_finished') {
        status.value = 'completed'
        if (typeof ev.data === 'object' && ev.data?.report) report.value = ev.data.report
        if (Array.isArray(ev.data?.vulns)) vulns.value = ev.data.vulns
      }
      if (ev.type === 'run_failed' || ev.type === 'error') {
        status.value = 'failed'
      }
      if (ev.type === 'vulnerability' || ev.type === 'vuln') {
        vulns.value.push(ev.data)
      }
      if (ev.type === 'report_delta' && typeof ev.data === 'string') {
        report.value += ev.data
      } else if (ev.type === 'report_delta' && typeof ev.data === 'object' && ev.data?.delta) {
        report.value += ev.data.delta
      }
    } catch (e) {
      console.warn('SSE parse error', e, msg.data)
    }
  }

  es.onerror = () => {
    // EventSource will auto-reconnect on transient errors
    // On terminal states, server closes connection
  }
}

onMounted(async () => {
  await loadRun()
  connectSSE()
})

onBeforeUnmount(() => {
  if (sseRef.value) {
    sseRef.value.close()
    sseRef.value = null
  }
})

watch(runId, async (v) => {
  if (v) {
    events.value = []
    vulns.value = []
    report.value = ''
    await loadRun()
    connectSSE()
  }
})
</script>

<template>
  <div class="col" style="height: 100%">
    <div class="row">
      <h2 style="font-size: 16px; font-weight: 500">
        Run <code>{{ runId.slice(0, 8) }}</code>
      </h2>
      <span class="pill" :class="status">{{ status }}</span>
      <div class="spacer" />
      <span v-if="runInfo" class="muted" style="font-size: 12px; display: flex; gap: 12px">
        <span title="input tokens">in {{ formatN(runInfo.input_tokens) }}</span>
        <span title="output tokens">out {{ formatN(runInfo.output_tokens) }}</span>
        <span title="cache read">cache {{ formatN(runInfo.cache_read_input_tokens) }}</span>
      </span>
      <span class="muted" style="font-size: 12px">
        {{ events.length }} events
      </span>
    </div>

    <div v-if="error" style="color: var(--red)">{{ error }}</div>

    <div v-if="status === 'running' || status === 'pending' || events.length > 0" class="run-panes">
      <div class="run-pane">
        <div class="run-pane-header">Reasoning</div>
        <div class="run-pane-body">
          <EventStream :events="events.filter((e) => e.type === 'reasoning_delta' || e.type === 'reasoning' || e.type === 'thinking')" />
        </div>
      </div>
      <div class="run-pane">
        <div class="run-pane-header">Response</div>
        <div class="run-pane-body">
          <EventStream :events="events.filter((e) => e.type === 'response_delta' || e.type === 'response' || e.type === 'message')" />
        </div>
      </div>
      <div class="run-pane">
        <div class="run-pane-header">Tool Calls ({{ toolCalls.length }})</div>
        <div class="run-pane-body">
          <table v-if="toolCalls.length > 0">
            <thead>
              <tr>
                <th>Tool</th>
                <th>Status</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(tc, i) in toolCalls" :key="i">
                <td>
                  <code>{{ tc.call.data?.name || tc.call.data?.tool || '?' }}</code>
                </td>
                <td>
                  <span v-if="tc.result" :style="{ color: tc.result.data?.is_error ? 'var(--red)' : 'var(--green)' }">
                    {{ tc.result.data?.is_error ? 'error' : 'ok' }}
                  </span>
                  <span v-else class="muted">pending</span>
                </td>
                <td class="muted" style="font-size: 11px">
                  {{ tc.call.ts ? new Date(tc.call.ts).toLocaleTimeString() : '—' }}
                </td>
              </tr>
            </tbody>
          </table>
          <div v-else class="muted" style="padding: 12px; text-align: center; font-size: 12px">
            No tool calls yet
          </div>
        </div>
      </div>
    </div>

    <div v-if="status === 'completed' || status === 'failed'" class="col" style="margin-top: 16px">
      <div class="row">
        <h3 style="font-size: 14px; font-weight: 500">Report</h3>
        <div class="spacer" />
        <span class="muted" style="font-size: 12px">
          {{ runInfo?.finished_at ? `finished ${fmtTime(runInfo.finished_at)}` : '' }}
        </span>
      </div>
      <div class="card markdown" v-if="report">
        <div v-html="reportHtml" />
      </div>
      <div v-else class="muted">No report available.</div>

      <h3 style="font-size: 14px; font-weight: 500; margin-top: 16px">Vulnerabilities ({{ vulns.length }})</h3>
      <table v-if="vulns.length > 0" class="card" style="padding: 0">
        <thead>
          <tr>
            <th>Severity</th>
            <th>Title</th>
            <th>Target</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(v, i) in vulns" :key="i">
            <td><SeverityBadge :severity="v.severity || 'info'" /></td>
            <td>{{ v.title || v.name || v.id }}</td>
            <td class="muted"><code>{{ v.target || v.url || v.affected || '—' }}</code></td>
          </tr>
        </tbody>
      </table>
      <div v-else class="muted">No vulnerabilities found.</div>
    </div>
  </div>
</template>
