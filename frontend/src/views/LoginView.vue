<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/auth'

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
    const redirect = (route.query.redirect as string) || '/targets'
    router.push(redirect)
  } catch (e: any) {
    error.value = e?.response?.data?.error || e?.message || 'Login failed'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-card card">
    <div class="logo" style="text-align: center; margin-bottom: 16px; font-size: 18px">
      ⬢ Dhunter
    </div>
    <div class="muted" style="text-align: center; margin-bottom: 20px; font-size: 12px">
      AI-driven web penetration testing
    </div>
    <form @submit.prevent="submit" class="col">
      <label>
        <div class="muted" style="font-size: 12px; margin-bottom: 4px">Username</div>
        <input v-model="username" autocomplete="username" style="width: 100%" />
      </label>
      <label>
        <div class="muted" style="font-size: 12px; margin-bottom: 4px">Password</div>
        <input v-model="password" type="password" autocomplete="current-password" style="width: 100%" />
      </label>
      <div v-if="error" style="color: var(--red); font-size: 12px">{{ error }}</div>
      <button type="submit" class="primary" :disabled="loading" style="margin-top: 4px">
        {{ loading ? 'Signing in...' : 'Sign in' }}
      </button>
    </form>
  </div>
</template>
