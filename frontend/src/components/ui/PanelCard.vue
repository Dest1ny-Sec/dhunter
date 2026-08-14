<script setup lang="ts">
defineProps<{ title?: string }>()
</script>

<template>
  <section class="panel-card">
    <header v-if="title || $slots.actions" class="panel-card-head">
      <h3 v-if="title" class="panel-card-title">{{ title }}</h3>
      <div class="spacer" />
      <div class="panel-card-actions">
        <slot name="actions" />
      </div>
    </header>
    <slot />
  </section>
</template>
