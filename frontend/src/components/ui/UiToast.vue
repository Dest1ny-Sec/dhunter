<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'

export interface Toast { id: number; message: string; type: 'success' | 'error' | 'info' }

const toasts = ref<Toast[]>([])
let nextId = 1

function show(message: string, type: Toast['type'] = 'info', ttl = 3000) {
  const id = nextId++
  toasts.value.push({ id, message, type })
  setTimeout(() => {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }, ttl)
}
defineExpose({ show, success: (m: string) => show(m, 'success'), error: (m: string) => show(m, 'error') })
onBeforeUnmount(() => (toasts.value = []))
</script>

<template>
  <Teleport to="body">
    <div class="ui-toasts">
      <TransitionGroup name="toast">
        <div v-for="t in toasts" :key="t.id" class="ui-toast" :class="t.type">
          <span class="t-icon">{{ t.type === 'success' ? '✓' : t.type === 'error' ? '✕' : 'ℹ' }}</span>
          <span>{{ t.message }}</span>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.ui-toasts {
  position: fixed; top: 20px; right: 20px; z-index: 200;
  display: flex; flex-direction: column; gap: 8px;
}
.ui-toast {
  display: flex; align-items: center; gap: 10px;
  background: var(--bg-elev-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 10px 16px;
  font-size: 13px;
  color: var(--text);
  box-shadow: var(--shadow-2);
  min-width: 220px;
}
.t-icon { font-weight: 700; }
.success .t-icon { color: var(--ok); }
.error { border-color: rgba(239,68,68,0.4); }
.error .t-icon { color: var(--danger); }
.info .t-icon { color: var(--accent); }

.toast-enter-active, .toast-leave-active { transition: all 0.2s ease; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translateX(20px); }
</style>
