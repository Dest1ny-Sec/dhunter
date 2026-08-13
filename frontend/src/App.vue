<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from './stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const nav = [
  { section: 'Workspace', items: [
    { name: 'dashboard', label: 'Dashboard', path: '/dashboard', icon: '◧' },
    { name: 'targets', label: 'Engagements', path: '/targets', icon: '◈' },
    { name: 'runs', label: 'Runs', path: '/runs', icon: '⏵' },
    { name: 'vulns', label: 'Vulnerabilities', path: '/vulns', icon: '⚑' },
  ]},
  { section: 'System', items: [
    { name: 'settings', label: 'Settings', path: '/settings', icon: '⚙' },
  ]},
]

const isLogin = computed(() => route.name === 'login')
const isActive = (path: string) => route.path === path || route.path.startsWith(path + '/')

function logout() {
  auth.logout()
  router.push('/login')
}
</script>

<template>
  <div v-if="isLogin" class="login-shell">
    <router-view />
  </div>
  <div v-else class="app-shell">
    <aside class="app-sidebar">
      <div class="brand">
        <div class="brand-mark">⬢</div>
        <span>Dhunter</span>
      </div>
      <template v-for="group in nav" :key="group.section">
        <div class="nav-section">{{ group.section }}</div>
        <router-link
          v-for="item in group.items"
          :key="item.name"
          :to="item.path"
          class="nav-item"
          :class="{ active: isActive(item.path) }"
        >
          <span class="nav-icon">{{ item.icon }}</span>
          {{ item.label }}
        </router-link>
      </template>
      <div class="sidebar-footer">
        <span class="brand-mark" style="width: 26px; height: 26px; font-size: 12px">{{ (auth.username || 'u')[0].toUpperCase() }}</span>
        <span>{{ auth.username || 'user' }}</span>
        <span class="spacer" />
        <button class="ghost" style="min-height: 26px; padding: 0 8px" @click="logout">⏻</button>
      </div>
    </aside>
    <header class="app-header">
      <h1 class="page-title">{{ (route.meta.title as string) || 'Dhunter' }}</h1>
      <div class="spacer" />
      <span class="muted" style="font-size: 12px">{{ new Date().toLocaleDateString() }}</span>
    </header>
    <main class="app-main">
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.page-title { font-size: 16px; font-weight: 600; margin: 0; letter-spacing: -0.01em; }
</style>
