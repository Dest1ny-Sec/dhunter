<script setup lang="ts">
withDefaults(defineProps<{
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'sm' | 'md' | 'lg'
  disabled?: boolean
}>(), { variant: 'secondary', size: 'md' })
</script>

<template>
  <button class="ui-btn" :class="[variant, size]" :disabled="disabled">
    <slot />
  </button>
</template>

<style scoped>
.ui-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border-radius: var(--radius-sm);
  font-weight: 500;
  transition: all 0.15s ease;
  border: 1px solid var(--border);
  background: var(--bg-elev-2);
  color: var(--text);
  cursor: pointer;
  letter-spacing: 0.01em;
}
.ui-btn:hover { background: var(--bg-elev-3); border-color: var(--border-bright); }
.ui-btn:active { transform: translateY(0.5px); }
.ui-btn:disabled { opacity: 0.45; cursor: not-allowed; }

.sm { min-height: 30px; padding: 0 12px; font-size: 12px; }
.md { min-height: 38px; padding: 0 18px; font-size: 13px; }
.lg { min-height: 48px; padding: 0 28px; font-size: 14px; }

/* primary: deep stellar blue with subtle aurora glow — NOT purple */
.primary {
  background: linear-gradient(135deg, var(--stellar) 0%, var(--stellar-dim) 100%);
  border: 1px solid rgba(125, 146, 232, 0.42);
  color: #0a1124;
  font-weight: 600;
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.18),
    0 4px 18px rgba(95, 200, 212, 0.18),
    0 0 0 1px rgba(125, 146, 232, 0.18);
}
.primary:hover {
  filter: brightness(1.08);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.24),
    0 6px 22px rgba(95, 200, 212, 0.28),
    0 0 0 1px rgba(125, 146, 232, 0.28);
}
.danger { background: var(--danger); border: none; color: #fff; }
.danger:hover { filter: brightness(1.1); }
.ghost { background: transparent; border-color: transparent; color: var(--text-dim); }
.ghost:hover { background: var(--bg-elev-2); color: var(--text); border-color: transparent; }
</style>
