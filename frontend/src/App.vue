<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from './stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const navItems = [
  { name: 'targets', label: 'Targets', path: '/targets' },
  { name: 'runs', label: 'Runs', path: '/runs' },
  { name: 'vulns', label: 'Vulnerabilities', path: '/vulns' },
  { name: 'settings', label: 'Settings', path: '/settings' },
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
    <header class="app-header">
      <div class="logo">⬢ Dhunter</div>
      <div class="spacer" />
      <div class="user-menu">
        <span>{{ auth.username || 'user' }}</span>
        <button style="margin-left: 12px" @click="logout">Logout</button>
      </div>
    </header>
    <nav class="app-nav">
      <div class="nav-section">Workspace</div>
      <router-link
        v-for="item in navItems"
        :key="item.name"
        :to="item.path"
        class="nav-item"
        :class="{ active: isActive(item.path) }"
      >
        {{ item.label }}
      </router-link>
    </nav>
    <main class="app-main">
      <router-view />
    </main>
  </div>
</template>
