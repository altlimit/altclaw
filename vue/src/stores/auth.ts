import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useRouter } from 'vue-router'

export const useAuthStore = defineStore('auth', () => {
  const authenticated = ref(false)
  const router = useRouter()

  async function login(password: string): Promise<string | null> {
    const resp = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password }),
    })
    if (!resp.ok) {
      const text = await resp.text()
      return text || 'Login failed'
    }
    authenticated.value = true
    return null
  }

  async function checkAuth(): Promise<boolean> {
    try {
      const resp = await fetch('/api/config')
      if (resp.ok) {
        authenticated.value = true
        return true
      }
    } catch {}
    authenticated.value = false
    return false
  }

  function logout() {
    authenticated.value = false
    document.cookie = 'altclaw_session=; Path=/; Max-Age=0'
    router.push('/login')
  }

  return { authenticated, login, checkAuth, logout }
})
