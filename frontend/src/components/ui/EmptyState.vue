<script setup lang="ts">
/**
 * Composed empty state — not just a sad "no data" line.
 * - icon (SVG) or emoji
 * - title
 * - subtitle / explanation
 * - optional primary CTA
 * - optional secondary CTA
 */
import Icon from '../icons/Icon.vue'

withDefaults(defineProps<{
  icon?: string
  title: string
  description?: string
  primaryLabel?: string
  secondaryLabel?: string
  /** accent line color: 'violet' | 'cyan' | 'amber' | 'rose' */
  accent?: 'violet' | 'cyan' | 'amber' | 'rose'
}>(), { accent: 'violet' })

defineEmits<{ (e: 'primary'): void; (e: 'secondary'): void }>()
</script>

<template>
  <div class="empty" :class="`accent-${accent}`">
    <div class="empty-icon">
      <Icon :name="icon || 'inbox'" :size="22" />
    </div>
    <h3 class="empty-title">{{ title }}</h3>
    <p v-if="description" class="empty-desc">{{ description }}</p>
    <div v-if="primaryLabel || secondaryLabel" class="empty-actions">
      <button v-if="primaryLabel" class="empty-primary" @click="$emit('primary')">
        <Icon name="arrow-right" :size="14" />
        <span>{{ primaryLabel }}</span>
      </button>
      <button v-if="secondaryLabel" class="empty-secondary" @click="$emit('secondary')">
        {{ secondaryLabel }}
      </button>
    </div>
    <slot />
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 48px 24px 44px;
  gap: 12px;
}
.empty-icon {
  width: 56px; height: 56px;
  border-radius: 16px;
  display: flex; align-items: center; justify-content: center;
  background: rgba(125, 146, 232, 0.08);
  border: 1px solid rgba(125, 146, 232, 0.18);
  color: var(--stellar-bright);
  margin-bottom: 4px;
  position: relative;
}
.empty-icon::after {
  content: '';
  position: absolute;
  inset: -8px;
  border-radius: 22px;
  background: radial-gradient(circle, currentColor 0%, transparent 60%);
  opacity: 0.12;
  z-index: -1;
  filter: blur(8px);
}
.empty.accent-cyan .empty-icon { color: var(--aurora-bright); background: rgba(95, 200, 212, 0.08); border-color: rgba(95, 200, 212, 0.2); }
.empty.accent-amber .empty-icon { color: var(--star-amber-bright); background: rgba(232, 200, 121, 0.08); border-color: rgba(232, 200, 121, 0.22); }
.empty.accent-rose .empty-icon { color: var(--sev-critical); background: rgba(226, 100, 114, 0.08); border-color: rgba(226, 100, 114, 0.22); }

.empty-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
  font-family: var(--font-display);
  letter-spacing: -0.01em;
  margin: 0;
}
.empty-desc {
  font-size: 13px;
  color: var(--text-dim);
  max-width: 420px;
  margin: 0;
  line-height: 1.6;
}
.empty-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 10px;
}
.empty-primary {
  display: inline-flex; align-items: center; gap: 8px;
  background: linear-gradient(135deg, rgba(125, 146, 232, 0.95) 0%, rgba(95, 110, 200, 0.95) 100%);
  color: #fff;
  border: 1px solid rgba(163, 180, 255, 0.4);
  box-shadow: 0 4px 20px rgba(125, 146, 232, 0.32);
  border-radius: 8px;
  padding: 9px 18px;
  min-height: 36px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: filter 0.2s, transform 0.2s, box-shadow 0.2s;
}
.empty-primary:hover { filter: brightness(1.1); transform: translateY(-1px); box-shadow: 0 6px 26px rgba(125, 146, 232, 0.42); }
.empty-secondary {
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-dim);
  border-radius: 8px;
  padding: 9px 18px;
  min-height: 36px;
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s;
}
.empty-secondary:hover { color: var(--text); border-color: var(--border-bright); }
</style>
