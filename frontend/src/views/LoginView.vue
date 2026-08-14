<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import UiButton from '../components/ui/UiButton.vue'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const error = ref<string | null>(null)
const loading = ref(false)

async function submit() {
  if (!username.value || !password.value) {
    error.value = '请输入账号和密码'
    return
  }
  error.value = null
  loading.value = true
  try {
    await auth.login(username.value, password.value)
    router.push((route.query.redirect as string) || '/dashboard')
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-card card">
    <div class="login-brand">
      <div class="brand-mark" style="width: 52px; height: 52px; border-radius: 14px">
        <svg viewBox="0 0 24 24" fill="none" style="width: 28px; height: 28px; color: #fff">
          <path d="M12 2 L22 12 L12 22 L2 12 Z" fill="currentColor" />
          <path d="M7 12 L12 7 L17 12 L12 17 Z" fill="rgba(255,255,255,0.25)" />
        </svg>
      </div>
      <h1 style="font-size: 22px; font-weight: 700; margin: 0">Dhunter</h1>
    </div>
    <p class="muted" style="font-size: 13px; text-align: center; margin: 8px 0 22px">
      AI 驱动的渗透测试平台
    </p>
    <form @submit.prevent="submit" class="col">
      <div>
        <label class="field-label">账号</label>
        <input v-model="username" autocomplete="username" style="width: 100%" />
      </div>
      <div>
        <label class="field-label">密码</label>
        <input v-model="password" type="password" autocomplete="current-password" style="width: 100%" />
      </div>
      <div v-if="error" style="color: var(--danger); font-size: 13px">{{ error }}</div>
      <UiButton type="submit" variant="primary" size="lg" :disabled="loading" style="margin-top: 6px">
        {{ loading ? '登录中…' : '登录' }}
      </UiButton>
    </form>
  </div>
</template>

<style scoped>
.login-brand { display: flex; flex-direction: column; align-items: center; gap: 12px; }
.brand-mark {
  background: linear-gradient(135deg, var(--accent), var(--accent-deep));
  display: flex; align-items: center; justify-content: center;
  box-shadow: 0 8px 24px rgba(139, 92, 246, 0.45);
  position: relative; overflow: hidden;
}
.brand-mark::after {
  content: ''; position: absolute; inset: 0;
  background: linear-gradient(135deg, rgba(255,255,255,0.22), transparent 55%);
  pointer-events: none;
}
.field-label { font-size: 12px; color: var(--text-dim); margin-bottom: 4px; display: block; }
</style>
