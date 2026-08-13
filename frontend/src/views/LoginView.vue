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
    error.value = 'Please enter username and password'
    return
  }
  error.value = null
  loading.value = true
  try {
    await auth.login(username.value, password.value)
    router.push((route.query.redirect as string) || '/dashboard')
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || 'Login failed'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-card card">
    <div class="login-brand">
      <div class="brand-mark">⬢</div>
      <h1 style="font-size: 22px; font-weight: 700; margin: 0">Dhunter</h1>
    </div>
    <p class="muted" style="font-size: 13px; text-align: center; margin: 8px 0 22px">
      AI-driven penetration testing platform
    </p>
    <form @submit.prevent="submit" class="col">
      <div>
        <label class="field-label">Username</label>
        <input v-model="username" autocomplete="username" style="width: 100%" />
      </div>
      <div>
        <label class="field-label">Password</label>
        <input v-model="password" type="password" autocomplete="current-password" style="width: 100%" />
      </div>
      <div v-if="error" style="color: var(--danger); font-size: 13px">{{ error }}</div>
      <UiButton type="submit" variant="primary" size="lg" :disabled="loading" style="margin-top: 6px">
        {{ loading ? 'Signing in…' : 'Sign in' }}
      </UiButton>
    </form>
  </div>
</template>

<style scoped>
.login-brand { display: flex; flex-direction: column; align-items: center; gap: 12px; }
.brand-mark {
  width: 52px; height: 52px; border-radius: 14px;
  background: linear-gradient(135deg, var(--accent-dim), var(--indigo));
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-size: 24px;
  box-shadow: var(--shadow-glow);
}
.field-label { font-size: 12px; color: var(--text-dim); margin-bottom: 4px; display: block; }
</style>
