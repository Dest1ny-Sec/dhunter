<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api/client'

const props = defineProps<{ runId: string }>()

interface Fact { id: string; run_id: string; description: string; source: string; created_at: string }
interface Intent { id: string; run_id: string; from: string[]; description: string; status: string; worker?: string | null; to_fact_id?: string | null }
interface Hint { id: string; content: string; creator: string; created_at: string }

const facts = ref<Fact[]>([])
const intents = ref<Intent[]>([])
const hints = ref<Hint[]>([])
const loading = ref(false)
const hintText = ref('')
const hintMsg = ref('')

let timer: number | null = null

async function load() {
  try {
    const res = await api.get(`/runs/${props.runId}/graph`)
    facts.value = res.data?.facts || []
    intents.value = res.data?.intents || []
    hints.value = res.data?.hints || []
    loading.value = false
  } catch (e) {
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

// --- graph layout (simple layered SVG, no external deps) ---
const statusLabel: Record<string, string> = {
  open: 'open',
  claimed: 'claiming…',
  concluded: 'done',
  failed: 'dead end',
}

const nodeW = 180
const nodeH = 56
const cols = 3
const colW = 220
const rowH = 130

interface Pos { x: number; y: number }

const factPos = computed<Record<string, Pos>>(() => {
  const pos: Record<string, Pos> = {}
  facts.value.forEach((f, i) => {
    const col = i % cols
    const row = Math.floor(i / cols)
    pos[f.id] = { x: 40 + col * colW, y: 20 + row * rowH }
  })
  return pos
})

interface Edge { x1: number; y1: number; x2: number; y2: number; midY: number; intent: Intent; open: boolean }

const edges = computed<Edge[]>(() => {
  const out: Edge[] = []
  for (const it of intents.value) {
    const from = (it.from || []).filter((id) => factPos.value[id])
    const target = it.to_fact_id && factPos.value[it.to_fact_id] ? factPos.value[it.to_fact_id] : null
    for (const fid of from.length ? from : [facts.value[0]?.id]) {
      if (!fid || !factPos.value[fid]) continue
      const a = factPos.value[fid]
      const b = target || a // open intent: draw a stub
      const midY = (a.y + b.y) / 2 + nodeH / 2
      out.push({
        x1: a.x + nodeW / 2,
        y1: a.y + nodeH,
        x2: target ? b.x + nodeW / 2 : b.x + nodeW / 2,
        y2: target ? b.y : b.y,
        intent: it,
        open: !target,
        midY,
      })
    }
  }
  return out
})

const graphHeight = computed(() => Math.max(120, Math.ceil(facts.value.length / cols) * rowH + 40))

function nodeColor(source: string) {
  if (source === 'origin') return 'var(--green, #22c55e)'
  if (source === 'goal') return 'var(--red, #ef4444)'
  return 'var(--accent, #3b82f6)'
}
</script>

<template>
  <div class="board">
    <div class="board-header">
      <strong>Attack Graph</strong>
      <span class="muted" style="font-size: 11px">
        {{ facts.length }} facts · {{ intents.length }} intents · {{ hints.length }} hints
        <span v-if="loading">(backend starting…)</span>
      </span>
    </div>

    <!-- causality graph -->
    <div v-if="facts.length > 0" class="graph-wrap">
      <svg :width="40 + cols * colW" :height="graphHeight">
        <defs>
          <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M 0 0 L 10 5 L 0 10 z" :fill="'var(--border, #555)'" />
          </marker>
        </defs>
        <!-- edges -->
        <g v-for="(e, i) in edges" :key="'e' + i">
          <line :x1="e.x1" :y1="e.y1" :x2="e.x2" :y2="e.y2" :stroke="'var(--border, #555)'"
                :stroke-width="e.open ? 1.5 : 2" :stroke-dasharray="e.open ? '4 3' : undefined"
                marker-end="url(#arrow)" />
          <!-- open intent label -->
          <g v-if="e.open" transform="translate(0, 0)">
            <rect :x="e.x1 - 60" :y="e.midY - 18" width="170" height="30" rx="6"
                  :fill="e.intent.status === 'claimed' ? '#3b2f0a' : '#1f2430'" stroke="#8a6d1a" stroke-dasharray="3 2" />
            <text :x="e.x1 + 25" :y="e.midY - 3" font-size="9" fill="#d8b45a"
                  :text-anchor="'middle'">{{ e.intent.status === 'claimed' ? '⏳' : '▸' }} {{ e.intent.description.slice(0, 22) }}</text>
          </g>
        </g>
        <!-- fact nodes -->
        <g v-for="(f, i) in facts" :key="f.id" :transform="`translate(${factPos[f.id].x}, ${factPos[f.id].y})`">
          <rect width="180" height="56" rx="8" :fill="nodeColor(f.source)" fill-opacity="0.12"
                :stroke="nodeColor(f.source)" stroke-width="1.5" />
          <text x="8" y="18" font-size="9" :fill="nodeColor(f.source)">{{ f.id.slice(0, 12) }}</text>
          <text x="8" y="36" font-size="10" fill="var(--text, #e5e7eb)">{{ f.description.slice(0, 46) }}</text>
          <text x="8" y="50" font-size="8" fill="var(--muted, #888)">{{ f.source }}</text>
        </g>
      </svg>
    </div>
    <div v-else class="muted" style="padding: 12px; font-size: 12px">No facts yet — the agent is starting up.</div>

    <!-- intent list -->
    <div class="board-list">
      <div class="board-subhead">Intents</div>
      <div v-if="intents.length" class="intent-row" v-for="it in intents" :key="it.id">
        <span class="pill small" :class="it.status">{{ statusLabel[it.status] || it.status }}</span>
        <span class="intent-desc">{{ it.description }}</span>
        <span v-if="it.worker" class="muted" style="font-size: 10px">{{ it.worker }}</span>
      </div>
      <div v-else class="muted" style="font-size: 12px">No intents yet — the planner will propose directions soon.</div>
    </div>

    <!-- hints -->
    <div class="board-list">
      <div class="board-subhead">Hints (inject guidance)</div>
      <div class="hint-row" v-for="h in hints" :key="h.id">
        <span class="muted" style="font-size: 10px">{{ h.creator }}</span>
        <span>{{ h.content }}</span>
      </div>
      <div class="hint-input">
        <input v-model="hintText" placeholder="e.g. try /actuator/env, use admin/admin creds…" @keyup.enter="sendHint" />
        <button @click="sendHint">Send</button>
        <span v-if="hintMsg" class="muted" style="font-size: 11px">{{ hintMsg }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.board {
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
  overflow-y: auto;
  padding: 12px;
}
.board-header {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}
.graph-wrap {
  overflow-x: auto;
  background: var(--bg, #12141a);
  border: 1px solid var(--border, #2a2e3a);
  border-radius: 8px;
  padding: 8px;
}
.board-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.board-subhead {
  font-size: 12px;
  font-weight: 500;
  color: var(--muted, #888);
  text-transform: uppercase;
  letter-spacing: 0.4px;
}
.intent-row,
.hint-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  padding: 4px 6px;
  background: var(--bg, #12141a);
  border-radius: 6px;
}
.intent-desc {
  flex: 1;
  color: var(--text, #e5e7eb);
}
.pill.small {
  font-size: 10px;
  padding: 1px 6px;
}
.hint-input {
  display: flex;
  gap: 6px;
  align-items: center;
}
.hint-input input {
  flex: 1;
  background: var(--bg, #12141a);
  border: 1px solid var(--border, #2a2e3a);
  border-radius: 6px;
  padding: 6px 8px;
  color: var(--text, #e5e7eb);
  font-size: 12px;
}
.hint-input button {
  background: var(--accent, #3b82f6);
  border: none;
  color: white;
  border-radius: 6px;
  padding: 6px 12px;
  font-size: 12px;
  cursor: pointer;
}
</style>
