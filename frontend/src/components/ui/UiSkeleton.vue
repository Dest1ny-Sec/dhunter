<script setup lang="ts">
/**
 * Skeleton loader — shimmering placeholder matching real layout shape
 * Use instead of generic spinners.
 */
withDefaults(defineProps<{
  width?: string
  height?: string
  radius?: string
  block?: boolean
}>(), { width: '100%', height: '14px', radius: '6px', block: false })
</script>

<template>
  <span
    class="sk"
    :class="{ block }"
    :style="{ width, height, borderRadius: radius }"
    aria-hidden="true"
  />
</template>

<style scoped>
.sk {
  display: inline-block;
  background:
    linear-gradient(
      90deg,
      rgba(125, 146, 232, 0.06) 0%,
      rgba(125, 146, 232, 0.14) 50%,
      rgba(125, 146, 232, 0.06) 100%
    );
  background-size: 200% 100%;
  animation: shimmer 1.4s linear infinite;
  vertical-align: middle;
}
.sk.block { display: block; }
@keyframes shimmer {
  0%   { background-position: 100% 0; }
  100% { background-position: -100% 0; }
}
@media (prefers-reduced-motion: reduce) {
  .sk { animation: none; background: rgba(125, 146, 232, 0.1); }
}
</style>
