<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from './stores/auth'
import Icon from './components/icons/Icon.vue'
import BrandMark from './components/icons/BrandMark.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

// Global header search — Enter 或 ⌘K / Ctrl+K 聚焦，回车跳到 /search 带关键词。
const searchQuery = ref('')
const searchInput = ref<HTMLInputElement | null>(null)

function goSearch() {
  const q = searchQuery.value.trim()
  router.push(q ? { path: '/search', query: { q } } : '/search')
}

function onGlobalKey(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    searchInput.value?.focus()
  }
}
onMounted(() => window.addEventListener('keydown', onGlobalKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onGlobalKey))

interface NavItem { name: string; label: string; path: string; icon: string }
interface NavGroup { key: string; label: string; items: NavItem[] }

const navGroups = ref<NavGroup[]>([
  {
    key: 'workspace', label: '工作区',
    items: [
      { name: 'dashboard', label: '仪表盘', path: '/dashboard', icon: 'dashboard' },
      { name: 'targets', label: '授权目标', path: '/targets', icon: 'target' },
      { name: 'runs', label: '运行记录', path: '/runs', icon: 'play' },
      { name: 'vulns', label: '漏洞成果', path: '/vulns', icon: 'flag' },
      { name: 'search', label: '历史对话', path: '/search', icon: 'search' },
    ],
  },
  {
    key: 'system', label: '系统',
    items: [
      { name: 'settings', label: '设置', path: '/settings', icon: 'settings' },
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
  <a v-if="!isLogin" href="#main" class="skip-link">跳到主要内容</a>
  <div v-if="isLogin" class="login-shell">
    <router-view />
  </div>
  <div v-else class="app-shell">
    <aside class="app-sidebar">
      <div class="brand">
        <BrandMark :size="32" :glow="true" :animate="true" />
        <span class="brand-text">Dhunter</span>
      </div>

      <div
        v-for="group in navGroups"
        :key="group.key"
        class="nav-group"
        :class="{ collapsed: collapsed[group.key] }"
      >
        <div class="nav-group-head" @click="toggleGroup(group.key)">
          <span>{{ group.label }}</span>
          <span class="chev"><Icon name="chevron-down" :size="12" /></span>
        </div>
        <router-link
          v-for="item in group.items"
          :key="item.name"
          :to="item.path"
          class="nav-item"
          :class="{ active: isActive(item.path) }"
        >
          <span class="nav-icon"><Icon :name="item.icon" :size="16" /></span>
          {{ item.label }}
        </router-link>
      </div>
    </aside>

    <header class="app-header">
      <h1 class="page-title">{{ (route.meta.title as string) || 'Dhunter' }}</h1>
      <div class="spacer" />
      <form class="header-search" @submit.prevent="goSearch">
        <span class="search-icon"><Icon name="search" :size="14" /></span>
        <input
          ref="searchInput"
          v-model="searchQuery"
          type="text"
          placeholder="搜索历史对话（回车跳转）…"
          aria-label="全局搜索"
        />
        <kbd class="kbd">⌘K</kbd>
      </form>
      <button class="header-avatar" :title="userLabel + '（点击退出）'" @click="logout" :aria-label="'退出登录 ' + userLabel">
        {{ userInitial }}
      </button>
    </header>

    <main id="main" class="app-main" tabindex="-1">
      <router-view />
    </main>
    <footer class="app-footer">
      ⚠️ Dhunter 仅供学术交流与安全研究使用 · 请确保测试目标已获授权 · 禁止用于任何非法或盈利行为
    </footer>
  </div>
</template>

<style scoped>
.brand-text {
  font-family: var(--font-serif);
  font-weight: 500;
  font-size: 18px;
  letter-spacing: -0.02em;
  font-feature-settings: 'ss01';
}
.skip-link {
  position: fixed;
  top: -100px;
  left: 16px;
  z-index: 9999;
  background: var(--bg-elev-2);
  color: var(--stellar-bright);
  border: 1px solid var(--border-bright);
  padding: 8px 14px;
  border-radius: 6px;
  font-size: 13px;
  text-decoration: none;
  font-family: var(--font-display);
  letter-spacing: 0.02em;
  transition: top 0.2s;
}
.skip-link:focus { top: 12px; outline: none; }
.page-title {
  font-size: 18px;
  font-weight: 600;
  margin: 0;
  letter-spacing: -0.015em;
  color: var(--text);
  font-family: var(--font-display);
}
.kbd {
  display: inline-flex;
  align-items: center;
  font-family: var(--font-mono);
  font-size: 10.5px;
  color: var(--text-faint);
  background: rgba(10, 17, 36, 0.6);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 6px;
  margin-left: 4px;
}
.app-footer {
  position: fixed;
  bottom: 0; left: 248px; right: 0;
  padding: 8px 0;
  text-align: center;
  font-size: 11px;
  color: var(--text-faint);
  background: linear-gradient(0deg, rgba(8, 6, 15, 0.85) 0%, transparent 100%);
  backdrop-filter: blur(8px);
  pointer-events: none;
  z-index: 5;
  letter-spacing: 0.01em;
}
.app-main { padding-bottom: 60px; }
.app-main:focus { outline: none; }
</style>
