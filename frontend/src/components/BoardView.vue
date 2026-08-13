<script setup lang="ts">
import { computed, defineComponent, h, markRaw, onBeforeUnmount, onMounted, ref } from 'vue'
import { VueFlow, useVueFlow } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import { MiniMap } from '@vue-flow/minimap'
import dagre from 'dagre'
import { api } from '../api/client'

const props = defineProps<{ runId: string }>()

const facts = ref<any[]>([])
const intents = ref<any[]>([])
const hints = ref<any[]>([])
const loading = ref(true)
const selected = ref<any>(null)
const hintText = ref('')
const hintMsg = ref('')
let timer: number | null = null

// ---------- data ----------
async function load() {
  try {
    const res = await api.get(`/runs/${props.runId}/graph`)
    facts.value = res.data?.facts || []
    intents.value = res.data?.intents || []
    hints.value = res.data?.hints || []
    loading.value = false
  } catch {
    loading.value = true
  }
}

async function sendHint() {
  const content = hintText.value.trim()
  if (!content) return
  try {
    await api.post(`/runs/${props.runId}/hints`, { content, creator: 'operator' })
    hintText.value = ''
    hintMsg.value = 'hint sent ✓'
    setTimeout(() => (hintMsg.value = ''), 2000)
  } catch (e: any) {
    hintMsg.value = `failed: ${e?.message || 'error'}`
  }
}

onMounted(() => {
  load()
  timer = window.setInterval(load, 2500)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
})

// ---------- node components ----------
const FactNode = markRaw(
  defineComponent({
    props: ['id', 'data'],
    setup(props) {
      return () => {
        const src = (props.data as any)?.source || 'agent'
        return h('div', { class: ['vf-node', `fact-${src}`], 'data-id': props.id }, [
          h('div', { class: 'vf-id' }, String(props.id).slice(0, 10)),
          h('div', { class: 'vf-desc' }, String((props.data as any)?.description || '').slice(0, 90)),
          h('div', { class: 'vf-src' }, src),
        ])
      }
    },
  }),
)

const IntentNode = markRaw(
  defineComponent({
    props: ['id', 'data'],
    setup(props) {
      return () => {
        const d = props.data as any
        const status = d?.status || 'open'
        return h('div', { class: ['vf-node', 'intent-node', `intent-${status}`], 'data-id': props.id }, [
          h('div', { class: 'vf-status' }, status),
          h('div', { class: 'vf-desc' }, String(d?.description || '').slice(0, 90)),
          h('div', { class: 'vf-src' }, d?.worker ? `worker ${d.worker}` : 'unclaimed'),
        ])
      }
    },
  }),
)

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const nodeTypes: any = { fact: FactNode, intent: IntentNode }

// ---------- graph build + layout ----------
interface VNode { id: string; type: string; position: { x: number; y: number }; data: Record<string, any> }
interface VEdge { id: string; source: string; target: string; label?: string; animated?: boolean; style?: Record<string, any> }

const { fitView } = useVueFlow()

const nodes = computed<VNode[]>(() => {
  const factNodes: VNode[] = facts.value.map((f) => ({
    id: f.id,
    type: 'fact',
    position: { x: 0, y: 0 },
    data: { description: f.description, source: f.source },
  }))
  const intentNodes: VNode[] = intents.value.map((it) => ({
    id: `intent:${it.id}`,
    type: 'intent',
    position: { x: 0, y: 0 },
    data: { description: it.description, status: it.status, worker: it.worker },
  }))
  return layout(factNodes, intentNodes)
})

const edges = computed<VEdge[]>(() => {
  const out: VEdge[] = []
  for (const it of intents.value) {
    const intentNodeId = `intent:${it.id}`
    const from = (it.from || []).filter((id: string) => facts.value.some((f) => f.id === id))
    const sources = from.length ? from : facts.value.filter((f) => f.source === 'origin').map((f) => f.id)
    for (const s of sources) {
      out.push({
        id: `e-${it.id}-${s}`,
        source: s,
        target: intentNodeId,
        animated: it.status === 'claimed',
        style: it.status === 'failed' ? { stroke: 'var(--text-faint)', strokeDasharray: '4 3' } : undefined,
      })
    }
    if (it.to_fact_id) {
      out.push({
        id: `e2-${it.id}`,
        source: intentNodeId,
        target: it.to_fact_id,
        label: '→',
      })
    }
  }
  return out
})

function layout(factNodes: VNode[], intentNodes: VNode[]): VNode[] {
  const all = [...factNodes, ...intentNodes]
  if (all.length === 0) return []
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: 'TB', nodesep: 40, ranksep: 70, marginx: 20, marginy: 20 })
  g.setDefaultEdgeLabel(() => ({}))
  for (const n of all) g.setNode(n.id, { width: 210, height: n.type === 'intent' ? 88 : 78 })
  for (const e of edges.value) g.setEdge(e.source, e.target)
  dagre.layout(g)
  return all.map((n) => {
    const pos = g.node(n.id)
    return { ...n, position: { x: pos.x - 105, y: pos.y - (n.type === 'intent' ? 44 : 39) } }
  })
}

function onNodeClick({ node }: any) {
  if (node.type === 'fact') {
    const f = facts.value.find((x) => x.id === node.id)
    selected.value = { kind: 'fact', data: f }
  } else {
    const it = intents.value.find((x) => x.id === node.id.replace('intent:', ''))
    selected.value = { kind: 'intent', data: it }
  }
}

const statusLabel: Record<string, string> = { open: 'open', claimed: 'working…', concluded: 'done', failed: 'dead end' }
</script>

<template>
  <div class="board">
    <div class="board-head">
      <div class="board-title">
        <strong>Attack Graph</strong>
        <span class="muted" style="font-size: 11px">{{ facts.length }} facts · {{ intents.length }} intents · {{ hints.length }} hints</span>
      </div>
      <button v-if="nodes.length" class="ghost" style="min-height: 28px; padding: 0 10px; font-size: 12px" @click="() => fitView()">Fit view</button>
    </div>

    <div class="graph-wrap">
      <VueFlow
        v-if="nodes.length"
        :nodes="nodes"
        :edges="edges"
        :node-types="nodeTypes"
        :min-zoom="0.1"
        :max-zoom="2"
        @node-click="onNodeClick"
      >
        <Background :gap="20" pattern-color="#1a1e3a" />
        <Controls position="bottom-left" />
        <MiniMap position="bottom-right" :pane-color="'#0c0e1a'" :node-color="'#7c66ff'" :mask-color="'rgba(6,7,13,0.7)'" />
      </VueFlow>
      <div v-else class="graph-empty muted">No facts yet — the agent is starting up.</div>
    </div>

    <!-- detail panel -->
    <div v-if="selected" class="detail-panel">
      <div class="detail-head">
        <span class="pill small" :class="selected.kind === 'fact' ? 'confirmed' : (selected.data?.status || 'open')">
          {{ selected.kind === 'fact' ? 'fact' : statusLabel[selected.data?.status] || selected.data?.status }}
        </span>
        <button class="ghost" style="min-height: 24px; padding: 0 6px" @click="selected = null">✕</button>
      </div>
      <div v-if="selected.kind === 'fact'">
        <div class="detail-desc">{{ selected.data?.description }}</div>
        <div class="muted" style="font-size: 11px; margin-top: 6px">source: {{ selected.data?.source }} · {{ selected.data?.id }}</div>
      </div>
      <div v-else>
        <div class="detail-desc">{{ selected.data?.description }}</div>
        <div class="muted" style="font-size: 11px; margin-top: 6px">
          {{ selected.data?.worker ? `worker: ${selected.data.worker}` : 'unclaimed' }} · {{ selected.data?.id }}
        </div>
      </div>
    </div>

    <div class="hint-block">
      <div class="muted" style="font-size: 11px">Hints (inject guidance the AI reads next turn)</div>
      <div class="hint-input">
        <input v-model="hintText" placeholder="e.g. try /actuator/env, use admin/admin creds…" @keyup.enter="sendHint" />
        <button class="primary" style="min-height: 34px" @click="sendHint">Send</button>
        <span v-if="hintMsg" class="muted" style="font-size: 11px">{{ hintMsg }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.board { display: flex; flex-direction: column; gap: 12px; height: 100%; min-height: 400px; }
.board-head { display: flex; justify-content: space-between; align-items: center; }
.board-title { display: flex; align-items: baseline; gap: 10px; }
.graph-wrap {
  flex: 1;
  height: 460px;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
  position: relative;
}
.graph-empty {
  position: absolute; inset: 0;
  display: flex; align-items: center; justify-content: center;
  font-size: 13px;
}
.detail-panel {
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 12px 14px;
}
.detail-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.detail-desc { font-size: 13px; line-height: 1.5; }
.hint-block { display: flex; flex-direction: column; gap: 6px; }
.hint-input { display: flex; gap: 8px; align-items: center; }
.hint-input input { flex: 1; }
</style>

<style>
/* Vue Flow node styling (global so node components can use tokens) */
.vf-node {
  width: 210px;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--bg-elev-2);
  border: 1px solid var(--border);
  font-size: 12px;
  cursor: pointer;
  transition: border-color 0.12s, box-shadow 0.12s;
}
.vf-node:hover { border-color: var(--accent); box-shadow: var(--shadow-glow); }
.vf-id { font-size: 9px; color: var(--text-faint); font-family: 'JetBrains Mono', monospace; margin-bottom: 4px; }
.vf-desc { color: var(--text); line-height: 1.35; word-break: break-word; }
.vf-src { font-size: 9px; color: var(--text-faint); margin-top: 6px; text-transform: uppercase; letter-spacing: 0.04em; }

.fact-origin { border-color: rgba(6, 182, 212, 0.6); background: rgba(6, 182, 212, 0.08); }
.fact-goal { border-color: rgba(239, 68, 68, 0.6); background: rgba(239, 68, 68, 0.08); }
.fact-agent, .fact-intent { border-color: rgba(124, 102, 255, 0.5); background: rgba(124, 102, 255, 0.06); }
.fact-origin .vf-id { color: var(--cyan); }
.fact-goal .vf-id { color: var(--danger); }

.intent-node { border-style: dashed; background: rgba(245, 158, 11, 0.05); }
.vf-status { font-size: 9px; text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 4px; color: var(--warn); }
.intent-claimed { border-color: rgba(124, 102, 255, 0.7); }
.intent-claimed .vf-status { color: var(--accent); }
.intent-concluded { border-color: rgba(16, 185, 129, 0.5); }
.intent-concluded .vf-status { color: var(--ok); }
.intent-failed { border-color: var(--text-faint); opacity: 0.7; }
.intent-failed .vf-status { color: var(--text-faint); }
</style>
