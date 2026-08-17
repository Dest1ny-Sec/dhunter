<script setup lang="ts">
/**
 * BrandMark — CSS-only "double star ring" logo for Dhunter.
 * Two concentric rings, two stars at opposite points on the outer ring,
 * and a small core star. Subtle slow rotation on the inner ring.
 */
withDefaults(defineProps<{
  size?: number       // pixel size of the whole mark
  glow?: boolean      // outer glow (use in headers, login)
  animate?: boolean   // slow rotation
}>(), { size: 32, glow: true, animate: true })
</script>

<template>
  <div class="brand-svg" :class="{ glow, animate }" :style="{ width: size + 'px', height: size + 'px' }">
    <svg :viewBox="`0 0 40 40`" :width="size" :height="size" fill="none" aria-hidden="true">
      <!-- outer ring (subtle) -->
      <circle cx="20" cy="20" r="17" stroke="rgba(163, 180, 255, 0.32)" stroke-width="0.6" />
      <!-- inner ring (slowly rotating) -->
      <g class="ring-inner">
        <circle cx="20" cy="20" r="11" stroke="url(#brandStroke)" stroke-width="0.9" />
        <!-- 4-pointed star at 12 o'clock on the inner ring -->
        <path class="star-a" d="M 20 5 L 21 9 L 25 10 L 21 11 L 20 15 L 19 11 L 15 10 L 19 9 Z"
              fill="var(--star-amber-bright)" />
        <!-- 4-pointed star at 6 o'clock on the inner ring -->
        <path class="star-b" d="M 20 25 L 21 29 L 25 30 L 21 31 L 20 35 L 19 31 L 15 30 L 19 29 Z"
              fill="var(--aurora-bright)" />
      </g>
      <!-- outer ring stars -->
      <circle cx="3" cy="20" r="1.4" fill="var(--stellar-bright)" />
      <circle cx="37" cy="20" r="1.4" fill="var(--nebula-bright)" />
      <!-- core: small cross/star -->
      <path d="M 20 17 L 21 19.5 L 23.5 20 L 21 20.5 L 20 23 L 19 20.5 L 16.5 20 L 19 19.5 Z"
            fill="var(--text)" />
      <defs>
        <linearGradient id="brandStroke" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stop-color="var(--aurora-bright)" stop-opacity="0.85" />
          <stop offset="50%" stop-color="var(--stellar-bright)" stop-opacity="0.6" />
          <stop offset="100%" stop-color="var(--nebula-bright)" stop-opacity="0.85" />
        </linearGradient>
      </defs>
    </svg>
  </div>
</template>

<style scoped>
.brand-svg {
  position: relative;
  display: inline-block;
  line-height: 0;
  flex-shrink: 0;
}
.brand-svg.glow::before {
  content: '';
  position: absolute;
  inset: -30%;
  background: radial-gradient(circle, rgba(125, 146, 232, 0.32), transparent 65%);
  pointer-events: none;
  z-index: -1;
}
.ring-inner {
  transform-origin: 20px 20px;
  transition: transform 0.6s ease;
}
.brand-svg.animate .ring-inner {
  animation: brand-spin 30s linear infinite;
}
@keyframes brand-spin {
  to { transform: rotate(360deg); }
}
.star-a, .star-b {
  filter: drop-shadow(0 0 1.5px currentColor);
}
</style>
