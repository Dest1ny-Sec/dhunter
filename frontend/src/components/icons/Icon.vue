<script setup lang="ts">
/**
 * Inline SVG icon set — Dhunter design system
 * All icons share:
 *   - 24×24 viewBox
 *   - stroke-width 1.6 (1.5 for tiny ones)
 *   - stroke-linecap=round, stroke-linejoin=round
 *   - currentColor (so they inherit text color)
 *   - 20px default size, can be overridden via `size` prop
 */
withDefaults(defineProps<{
  name: string
  size?: number | string
  stroke?: number
}>(), { size: 18, stroke: 1.6 })
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    :stroke-width="stroke"
    stroke="currentColor"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    class="dh-icon"
  >
    <!-- dashboard — four panels -->
    <template v-if="name === 'dashboard'">
      <rect x="3" y="3" width="8" height="10" rx="1.5" />
      <rect x="13" y="3" width="8" height="6" rx="1.5" />
      <rect x="13" y="11" width="8" height="10" rx="1.5" />
      <rect x="3" y="15" width="8" height="6" rx="1.5" />
    </template>

    <!-- target — concentric circles with crosshair -->
    <template v-else-if="name === 'target'">
      <circle cx="12" cy="12" r="9" />
      <circle cx="12" cy="12" r="5" />
      <circle cx="12" cy="12" r="1.4" fill="currentColor" />
      <line x1="12" y1="2" x2="12" y2="5" />
      <line x1="12" y1="19" x2="12" y2="22" />
      <line x1="2" y1="12" x2="5" y2="12" />
      <line x1="19" y1="12" x2="22" y2="12" />
    </template>

    <!-- play — pentagon-like arrow -->
    <template v-else-if="name === 'play'">
      <path d="M8 5.5 L19 12 L8 18.5 Z" fill="currentColor" stroke="none" />
    </template>

    <!-- pause -->
    <template v-else-if="name === 'pause'">
      <rect x="7" y="5" width="3.5" height="14" rx="1" fill="currentColor" stroke="none" />
      <rect x="13.5" y="5" width="3.5" height="14" rx="1" fill="currentColor" stroke="none" />
    </template>

    <!-- flag -->
    <template v-else-if="name === 'flag'">
      <path d="M5 21 L5 4" />
      <path d="M5 4 L18 4 L15 8.5 L18 13 L5 13" />
    </template>

    <!-- search -->
    <template v-else-if="name === 'search'">
      <circle cx="11" cy="11" r="6.5" />
      <line x1="16" y1="16" x2="20" y2="20" />
    </template>

    <!-- settings — gear -->
    <template v-else-if="name === 'settings'">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h0a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h0a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v0a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </template>

    <!-- chevron down -->
    <template v-else-if="name === 'chevron-down'">
      <polyline points="6 9 12 15 18 9" />
    </template>

    <!-- chevron right -->
    <template v-else-if="name === 'chevron-right'">
      <polyline points="9 6 15 12 9 18" />
    </template>

    <!-- chevron left -->
    <template v-else-if="name === 'chevron-left'">
      <polyline points="15 6 9 12 15 18" />
    </template>

    <!-- arrow up-right (sparkline arrows) -->
    <template v-else-if="name === 'arrow-up-right'">
      <line x1="6" y1="18" x2="18" y2="6" />
      <polyline points="9 6 18 6 18 15" />
    </template>

    <!-- arrow right (card hover arrow) -->
    <template v-else-if="name === 'arrow-right'">
      <line x1="5" y1="12" x2="19" y2="12" />
      <polyline points="13 6 19 12 13 18" />
    </template>

    <!-- back -->
    <template v-else-if="name === 'arrow-left'">
      <line x1="19" y1="12" x2="5" y2="12" />
      <polyline points="11 18 5 12 11 6" />
    </template>

    <!-- plus -->
    <template v-else-if="name === 'plus'">
      <line x1="12" y1="5" x2="12" y2="19" />
      <line x1="5" y1="12" x2="19" y2="12" />
    </template>

    <!-- close -->
    <template v-else-if="name === 'close'">
      <line x1="6" y1="6" x2="18" y2="18" />
      <line x1="18" y1="6" x2="6" y2="18" />
    </template>

    <!-- check -->
    <template v-else-if="name === 'check'">
      <polyline points="5 12 10 17 19 7" />
    </template>

    <!-- refresh -->
    <template v-else-if="name === 'refresh'">
      <polyline points="21 4 21 10 15 10" />
      <polyline points="3 20 3 14 9 14" />
      <path d="M3.51 9a9 9 0 0 1 14.85-3.36L21 8" />
      <path d="M20.49 15a9 9 0 0 1-14.85 3.36L3 16" />
    </template>

    <!-- bell -->
    <template v-else-if="name === 'bell'">
      <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" />
      <path d="M10.3 21a1.94 1.94 0 0 0 3.4 0" />
    </template>

    <!-- sparkle (decorative) -->
    <template v-else-if="name === 'sparkle'">
      <path d="M12 3 L13.5 9.5 L20 11 L13.5 12.5 L12 19 L10.5 12.5 L4 11 L10.5 9.5 Z" fill="currentColor" stroke="none" />
      <path d="M19 4 L19.7 6 L21.5 6.5 L19.7 7 L19 9 L18.3 7 L16.5 6.5 L18.3 6 Z" fill="currentColor" stroke="none" />
    </template>

    <!-- circle (running) -->
    <template v-else-if="name === 'circle'">
      <circle cx="12" cy="12" r="9" />
    </template>

    <!-- trending up (chart icon) -->
    <template v-else-if="name === 'trending-up'">
      <polyline points="3 17 9 11 13 15 21 7" />
      <polyline points="15 7 21 7 21 13" />
    </template>

    <!-- activity / pulse -->
    <template v-else-if="name === 'pulse'">
      <polyline points="3 12 7 12 10 5 14 19 17 12 21 12" />
    </template>

    <!-- alert triangle (warn) -->
    <template v-else-if="name === 'alert'">
      <path d="M10.3 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z" />
      <line x1="12" y1="9" x2="12" y2="13" />
      <line x1="12" y1="17" x2="12.01" y2="17" />
    </template>

    <!-- bolt -->
    <template v-else-if="name === 'bolt'">
      <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" fill="currentColor" stroke="none" />
    </template>

    <!-- shield (security) -->
    <template v-else-if="name === 'shield'">
      <path d="M12 2 L20 6 L20 12 C20 17 16 21 12 22 C8 21 4 17 4 12 L4 6 Z" />
    </template>

    <!-- compass (search/explore) -->
    <template v-else-if="name === 'compass'">
      <circle cx="12" cy="12" r="9" />
      <polygon points="16 8 13 13 8 16 11 11 16 8" fill="currentColor" stroke="none" />
    </template>

    <!-- spark / fire (recent activity) -->
    <template v-else-if="name === 'fire'">
      <path d="M12 2 C12 6 8 7 8 12 C8 16 10 18 12 18 C14 18 16 16 16 12 C16 9 14 7 12 2 Z" fill="currentColor" stroke="none" />
    </template>

    <!-- book (history) -->
    <template v-else-if="name === 'book'">
      <path d="M4 4 L4 20 L9 20 C11 20 12 19 12 17 C12 19 13 20 15 20 L20 20 L20 4 L15 4 C13 4 12 5 12 7 C12 5 11 4 9 4 Z" />
    </template>

    <!-- terminal / code (tool) -->
    <template v-else-if="name === 'terminal'">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <polyline points="7 9 10 12 7 15" />
      <line x1="12" y1="15" x2="17" y2="15" />
    </template>

    <!-- logout -->
    <template v-else-if="name === 'logout'">
      <path d="M9 21H5 a2 2 0 0 1-2-2V5 a2 2 0 0 1 2-2h4" />
      <polyline points="16 17 21 12 16 7" />
      <line x1="21" y1="12" x2="9" y2="12" />
    </template>

    <!-- warning sign -->
    <template v-else-if="name === 'warn-circle'">
      <circle cx="12" cy="12" r="9" />
      <line x1="12" y1="8" x2="12" y2="13" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </template>

    <!-- empty / nothing here -->
    <template v-else-if="name === 'inbox'">
      <polyline points="22 12 16 12 14 15 10 15 8 12 2 12" />
      <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11Z" />
    </template>

    <!-- library / collection (3 stacked horizontal books) -->
    <template v-else-if="name === 'library'">
      <rect x="3" y="5"  width="18" height="3" rx="0.6" />
      <rect x="3" y="10.5" width="18" height="3" rx="0.6" />
      <rect x="3" y="16" width="18" height="3" rx="0.6" />
      <line x1="6" y1="5" x2="6" y2="8" />
      <line x1="6" y1="10.5" x2="6" y2="13.5" />
      <line x1="6" y1="16" x2="6" y2="19" />
    </template>

    <!-- plug (MCP connect) -->
    <template v-else-if="name === 'plug'">
      <path d="M6 6 L6 11 A4 4 0 0 0 10 15 L14 15 A4 4 0 0 0 18 11 L18 6" />
      <line x1="10" y1="3" x2="10" y2="6" />
      <line x1="18" y1="3" x2="18" y2="6" />
      <line x1="12" y1="15" x2="12" y2="20" />
    </template>

    <!-- puzzle (skill — modular plug-in) -->
    <template v-else-if="name === 'puzzle'">
      <path d="M10 3 H14 V6 A2 2 0 0 0 18 6 V10 H21 V14 H18 A2 2 0 0 0 18 18 V21 H14 V18 A2 2 0 0 0 10 18 V14 H3 V10 H6 A2 2 0 0 0 6 6 V3 Z" />
    </template>

    <!-- template (grid of squares) -->
    <template v-else-if="name === 'template'">
      <rect x="3" y="3" width="7" height="7" rx="1" />
      <rect x="14" y="3" width="7" height="7" rx="1" />
      <rect x="3" y="14" width="7" height="7" rx="1" />
      <rect x="14" y="14" width="7" height="7" rx="1" />
    </template>
  </svg>
</template>

<style scoped>
.dh-icon { display: inline-block; vertical-align: middle; flex-shrink: 0; }
</style>
