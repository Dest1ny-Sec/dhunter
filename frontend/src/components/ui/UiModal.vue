<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'

const props = withDefaults(defineProps<{ open: boolean; title?: string; width?: string }>(), {
  title: '', width: '560px',
})
const emit = defineEmits<{ (e: 'close'): void }>()

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && props.open) emit('close')
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="open" class="overlay" @click.self="emit('close')">
        <div class="modal" :style="{ maxWidth: width }">
          <header class="modal-head">
            <h3 v-if="title">{{ title }}</h3>
            <button class="modal-close" @click="emit('close')">✕</button>
          </header>
          <div class="modal-body">
            <slot />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.overlay {
  position: fixed; inset: 0; z-index: 100;
  background: rgba(3, 4, 10, 0.7);
  backdrop-filter: blur(4px);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  max-height: 85vh;
  display: flex; flex-direction: column;
}
.modal-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}
.modal-head h3 { font-size: 15px; font-weight: 600; margin: 0; }
.modal-close { background: transparent; border: none; color: var(--text-dim); min-height: 30px; padding: 0 8px; font-size: 14px; }
.modal-close:hover { color: var(--text); background: var(--bg-elev-2); }
.modal-body { padding: 20px; overflow-y: auto; }

.modal-enter-active, .modal-leave-active { transition: opacity 0.15s ease; }
.modal-enter-active .modal, .modal-leave-active .modal { transition: transform 0.15s ease; }
.modal-enter-from, .modal-leave-to { opacity: 0; }
.modal-enter-from .modal, .modal-leave-to .modal { transform: translateY(12px) scale(0.98); }
</style>
