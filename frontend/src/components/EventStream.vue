<script setup lang="ts">
import { ref, nextTick, watch } from 'vue'

export interface SSEEvent {
  type: string
  data: any
  ts?: number
}

const props = defineProps<{
  events: SSEEvent[]
}>()

const expanded = ref<Record<number, boolean>>({})
const autoScroll = ref(true)
const bodyRef = ref<HTMLElement | null>(null)

function toggle(i: number) {
  expanded.value[i] = !expanded.value[i]
}

function fmtData(d: any): string {
  if (d == null) return ''
  if (typeof d === 'string') return d
  try {
    return JSON.stringify(d, null, 2)
  } catch {
    return String(d)
  }
}

function isExpandable(d: any): boolean {
  if (d == null) return false
  if (typeof d === 'string') return d.length > 120
  if (typeof d === 'object') return true
  return false
}

function isError(d: any): boolean {
  if (!d || typeof d !== 'object') return false
  return d.is_error === true || d.error === true || d.status === 'error'
}

function isCollapsible(t: string): boolean {
  return t === 'tool_call' || t === 'tool_result' || t === 'message'
}

function eventClass(ev: SSEEvent): string[] {
  const cls = ['event', ev.type]
  if (ev.type === 'tool_result' && isError(ev.data)) cls.push('error')
  else if (ev.type === 'tool_result') cls.push('ok')
  return cls
}

function eventTitle(ev: SSEEvent): string {
  if (ev.type === 'tool_call') {
    return ev.data?.name || ev.data?.tool || 'tool call'
  }
  if (ev.type === 'tool_result') {
    return ev.data?.name || ev.data?.tool || 'tool result'
  }
  return ev.type
}

watch(
  () => props.events.length,
  async () => {
    if (autoScroll.value) {
      await nextTick()
      if (bodyRef.value) bodyRef.value.scrollTop = bodyRef.value.scrollHeight
    }
  }
)

// Merged deltas update the LAST row in place (length unchanged) — keep
// auto-scrolling on the latest accumulated content too.
watch(
  () => props.events[props.events.length - 1]?.data,
  async () => {
    if (autoScroll.value) {
      await nextTick()
      if (bodyRef.value) bodyRef.value.scrollTop = bodyRef.value.scrollHeight
    }
  }
)

function scrollToBottom() {
  if (bodyRef.value) bodyRef.value.scrollTop = bodyRef.value.scrollHeight
}
</script>

<template>
  <div class="event-stream">
    <div class="event-toolbar">
      <label class="muted" style="font-size: 12px">
        <input type="checkbox" v-model="autoScroll" /> Auto-scroll
      </label>
      <button v-if="!autoScroll" @click="scrollToBottom" style="font-size: 11px; padding: 2px 8px">
        Jump to latest ↓
      </button>
    </div>
    <div ref="bodyRef" class="event-body-scroll">
      <div v-if="events.length === 0" class="muted" style="padding: 12px; text-align: center">
        Waiting for events...
      </div>
      <div
        v-for="(ev, i) in events"
        :key="i"
        :class="eventClass(ev)"
      >
        <div
          v-if="isCollapsible(ev.type) && isExpandable(ev.data)"
          class="event-toggle"
          @click="toggle(i)"
        >
          <span class="event-meta">
            <span style="margin-right: 6px">{{ expanded[i] ? '▼' : '▶' }}</span>
            <span>{{ eventTitle(ev) }}</span>
            <span v-if="ev.ts" style="margin-left: 8px">{{ new Date(ev.ts).toLocaleTimeString() }}</span>
          </span>
        </div>
        <div v-else class="event-meta">
          <span>{{ eventTitle(ev) }}</span>
          <span v-if="ev.ts" style="margin-left: 8px">{{ new Date(ev.ts).toLocaleTimeString() }}</span>
        </div>
        <div
          v-if="!isCollapsible(ev.type) || expanded[i] || !isExpandable(ev.data)"
          class="event-body"
        >
          <pre v-if="typeof ev.data === 'object' && ev.data !== null" style="margin: 0"><code>{{ fmtData(ev.data) }}</code></pre>
          <template v-else>{{ fmtData(ev.data) }}</template>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.event-stream {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.event-toolbar {
  padding: 4px 8px;
  border-bottom: 1px solid var(--border);
  display: flex;
  gap: 8px;
  align-items: center;
  background: var(--bg);
}
.event-body-scroll {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}
</style>
