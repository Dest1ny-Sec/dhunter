<script setup lang="ts">
defineProps<{ icon?: string; message?: string }>()
</script>

<template>
  <div class="ui-empty">
    <div v-if="icon" class="empty-icon">{{ icon }}</div>
    <div class="empty-msg">{{ message || 'No data yet' }}</div>
    <slot />
  </div>
</template>

<style scoped>
.ui-empty {
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 8px;
  padding: 40px 20px;
  color: var(--text-faint);
  text-align: center;
}
.empty-icon { font-size: 32px; opacity: 0.6; }
.empty-msg { font-size: 13px; }
</style>
