import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(localStorage.getItem('dhunter_token'))
  const username = ref<string | null>(localStorage.getItem('dhunter_user'))

  const isLoggedIn = () => !!token.value

  async function login(user: string, password: string) {
    const res = await api.post('/auth/login', { username: user, password })
    const t = res.data?.token || res.data?.access_token
    if (!t) throw new Error('no token in response')
    token.value = t
    username.value = user
    localStorage.setItem('dhunter_token', t)
    localStorage.setItem('dhunter_user', user)
  }

  function logout() {
    token.value = null
    username.value = null
    localStorage.removeItem('dhunter_token')
    localStorage.removeItem('dhunter_user')
  }

  return { token, username, isLoggedIn, login, logout }
})
