import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from './stores/auth'

const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('./views/LoginView.vue'),
    meta: { public: true },
  },
  {
    path: '/',
    redirect: '/dashboard',
  },
  {
    path: '/dashboard',
    name: 'dashboard',
    component: () => import('./views/DashboardView.vue'),
    meta: { title: '仪表盘' },
  },
  {
    path: '/targets',
    name: 'targets',
    component: () => import('./views/TargetsView.vue'),
    meta: { title: '授权目标' },
  },
  {
    path: '/targets/:id/runs',
    name: 'target-runs',
    component: () => import('./views/TargetRunsView.vue'),
    props: true,
    meta: { title: '项目会话' },
  },
  {
    path: '/runs',
    name: 'runs',
    component: () => import('./views/RunsView.vue'),
    meta: { title: '运行记录' },
  },
  {
    path: '/runs/:id',
    name: 'run-detail',
    component: () => import('./views/RunDetailView.vue'),
    props: true,
    meta: { title: '运行详情' },
  },
  {
    path: '/vulns',
    name: 'vulns',
    component: () => import('./views/VulnsView.vue'),
    meta: { title: '漏洞成果' },
  },
  {
    path: '/search',
    name: 'search',
    component: () => import('./views/SearchView.vue'),
    meta: { title: '历史对话搜索' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('./views/SettingsView.vue'),
    meta: { title: '设置' },
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('./views/NotFoundView.vue'),
    meta: { title: '星域之外', public: true },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to, _from, next) => {
  const auth = useAuthStore()
  if (!to.meta.public && !auth.token) {
    next({ name: 'login', query: { redirect: to.fullPath } })
  } else {
    next()
  }
})

export default router
