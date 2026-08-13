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
}
.ui-btn:hover { background: var(--bg-elev-3); border-color: var(--text-faint); }
.ui-btn:disabled { opacity: 0.45; cursor: not-allowed; }

.sm { min-height: 30px; padding: 0 12px; font-size: 12px; }
.md { min-height: 38px; padding: 0 18px; font-size: 13px; }
.lg { min-height: 48px; padding: 0 28px; font-size: 14px; }

.primary {
  background: linear-gradient(135deg, var(--accent-dim), var(--indigo));
  border: none; color: #fff;
  box-shadow: 0 0 0 1px rgba(124,102,255,0.4), 0 4px 16px rgba(124,102,255,0.25);
}
.primary:hover { filter: brightness(1.12); }
.danger { background: var(--danger); border: none; color: #fff; }
.danger:hover { filter: brightness(1.1); }
.ghost { background: transparent; border-color: transparent; color: var(--text-dim); }
.ghost:hover { background: var(--bg-elev-2); color: var(--text); border-color: transparent; }
</style>
