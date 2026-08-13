<script setup lang="ts">
import UiBadge from './ui/UiBadge.vue'
defineProps<{ severity: string }>()
</script>

<template>
  <UiBadge kind="severity" :value="severity || 'info'" />
</template>
