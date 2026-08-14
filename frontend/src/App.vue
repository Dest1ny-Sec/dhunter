<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from './stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

interface NavItem { name: string; label: string; path: string; icon: string }
interface NavGroup { key: string; label: string; items: NavItem[] }

const navGroups = ref<NavGroup[]>([
  {
    key: 'workspace', label: '工作区',
    items: [
      { name: 'dashboard', label: '仪表盘', path: '/dashboard', icon: '⊟' },
      { name: 'targets', label: '授权目标', path: '/targets', icon: '◇' },
      { name: 'runs', label: '运行记录', path: '/runs', icon: '▶' },
      { name: 'vulns', label: '漏洞成果', path: '/vulns', icon: '⚑' },
    ],
  },
  {
    key: 'system', label: '系统',
    items: [
      { name: 'settings', label: '设置', path: '/settings', icon: '⚙' },
    ],
  },
])

const collapsed = ref<Record<string, boolean>>({})

const isLogin = computed(() => route.name === 'login')
const isActive = (path: string) => route.path === path || route.path.startsWith(path + '/')
const userInitial = computed(() => (auth.username || 'U')[0].toUpperCase())
const userLabel = computed(() => auth.username || 'admin')

function toggleGroup(key: string) {
  collapsed.value = { ...collapsed.value, [key]: !collapsed.value[key] }
}

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
        <div class="brand-mark">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" style="color: #fff">
            <path d="M12 2 L22 12 L12 22 L2 12 Z" fill="currentColor" stroke="none" />
            <path d="M7 12 L12 7 L17 12 L12 17 Z" fill="rgba(255,255,255,0.25)" stroke="none" />
          </svg>
        </div>
        <span>Dhunter</span>
      </div>

      <div
        v-for="group in navGroups"
        :key="group.key"
        class="nav-group"
        :class="{ collapsed: collapsed[group.key] }"
      >
        <div class="nav-group-head" @click="toggleGroup(group.key)">
          <span>{{ group.label }}</span>
          <span class="chev">▾</span>
        </div>
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
      </div>
    </aside>

    <header class="app-header">
      <h1 class="page-title">{{ (route.meta.title as string) || 'Dhunter' }}</h1>
      <div class="spacer" />
      <div class="header-search">
        <span class="search-icon">⚲</span>
        <input type="text" placeholder="快速搜索..." />
      </div>
      <button class="header-action" title="任务通知">
        <span>✉</span>
        <span class="dot" />
      </button>
      <button class="header-action" title="告警">
        <span>🔔</span>
      </button>
      <div class="header-avatar" :title="userLabel" @click="logout">{{ userInitial }}</div>
    </header>

    <main class="app-main">
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.page-title { font-size: 18px; font-weight: 600; margin: 0; letter-spacing: -0.01em; color: var(--text); }
</style>
