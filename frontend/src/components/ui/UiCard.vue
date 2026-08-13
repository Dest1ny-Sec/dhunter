<script setup lang="ts">
defineProps<{ title?: string; pad?: boolean }>()
</script>

<template>
  <section class="ui-card" :class="{ 'no-pad': pad === false }">
    <header v-if="title || $slots.actions" class="ui-card-head">
      <h3 v-if="title" class="ui-card-title">{{ title }}</h3>
      <div class="spacer" />
      <slot name="actions" />
    </header>
    <div class="ui-card-body">
      <slot />
    </div>
  </section>
</template>

<style scoped>
.ui-card {
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow-1);
  overflow: hidden;
}
.ui-card-head {
  display: flex; align-items: center; gap: 12px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
}
.ui-card-title { font-size: 14px; font-weight: 600; color: var(--text); margin: 0; }
.ui-card-body { padding: 18px; }
.no-pad .ui-card-body { padding: 0; }
</style>
