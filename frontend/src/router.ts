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
    meta: { title: 'Dashboard' },
  },
  {
    path: '/targets',
    name: 'targets',
    component: () => import('./views/TargetsView.vue'),
    meta: { title: 'Engagements' },
  },
  {
    path: '/runs',
    name: 'runs',
    component: () => import('./views/RunsView.vue'),
    meta: { title: 'Runs' },
  },
  {
    path: '/runs/:id',
    name: 'run-detail',
    component: () => import('./views/RunDetailView.vue'),
    props: true,
    meta: { title: 'Run Detail' },
  },
  {
    path: '/vulns',
    name: 'vulns',
    component: () => import('./views/VulnsView.vue'),
    meta: { title: 'Vulnerabilities' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('./views/SettingsView.vue'),
    meta: { title: 'Settings' },
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
