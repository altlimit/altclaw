<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const password = ref('')
const error = ref('')
const loading = ref(false)
const hasPasskeys = ref(false)
const passkeyLoading = ref(false)
const isRelay = ref(!['localhost', '127.0.0.1', '::1'].includes(location.hostname))

function bufferToBase64url(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function base64urlToBuffer(b64: string): ArrayBuffer {
  const padded = b64.replace(/-/g, '+').replace(/_/g, '/') + '=='.slice(0, (4 - b64.length % 4) % 4)
  const raw = atob(padded)
  const arr = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i)
  return arr.buffer
}

onMounted(async () => {
  try {
    const resp = await fetch('/api/has-passkeys')
    const data = await resp.json()
    // Passkeys require secure context (HTTPS or localhost)
    hasPasskeys.value = data.has_passkeys && !!navigator.credentials
  } catch {}
})

async function login() {
  if (!password.value) return
  loading.value = true
  error.value = ''
  try {
    const resp = await fetch('/api/auth', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: password.value }),
    })
    if (!resp.ok) {
      error.value = (await resp.json()).error
      return
    }
    router.push('/')
  } catch (e: any) {
    error.value = e.message || 'Login failed'
  } finally {
    loading.value = false
  }
}

async function loginWithPasskey() {
  passkeyLoading.value = true
  error.value = ''
  try {
    // 1. Begin login
    const beginResp = await fetch('/api/passkey-login-begin', { method: 'POST' })
    if (!beginResp.ok) throw new Error(await beginResp.text())
    const options = await beginResp.json()

    // 2. Convert options for navigator.credentials.get
    const publicKey = options.publicKey
    const challengeKey = publicKey.challenge // capture before decode
    publicKey.challenge = base64urlToBuffer(publicKey.challenge)
    if (publicKey.allowCredentials) {
      publicKey.allowCredentials = publicKey.allowCredentials.map((c: any) => ({
        ...c,
        id: base64urlToBuffer(c.id),
      }))
    }

    // 3. Get assertion
    const credential = await navigator.credentials.get({ publicKey }) as PublicKeyCredential
    if (!credential) throw new Error('Authentication cancelled')

    const assertion = credential.response as AuthenticatorAssertionResponse

    // 4. Send to server
    const body = JSON.stringify({
      id: credential.id,
      rawId: bufferToBase64url(credential.rawId),
      type: credential.type,
      response: {
        authenticatorData: bufferToBase64url(assertion.authenticatorData),
        clientDataJSON: bufferToBase64url(assertion.clientDataJSON),
        signature: bufferToBase64url(assertion.signature),
        userHandle: assertion.userHandle ? bufferToBase64url(assertion.userHandle) : '',
      },
    })

    const finishResp = await fetch(`/api/passkey-login-finish?challenge=${encodeURIComponent(challengeKey)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    })
    if (!finishResp.ok) throw new Error('Passkey authentication failed')

    router.push('/')
  } catch (e: any) {
    error.value = e.message || 'Passkey login failed'
  } finally {
    passkeyLoading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card section">
      <img src="/altclaw.svg" alt="AltClaw Logo" class="logo-image" />
      <div class="logo-text">AltClaw</div>
      <form v-if="!isRelay || !hasPasskeys" @submit.prevent="login">
        <div class="field">
          <input
            v-model="password"
            type="password"
            placeholder="Enter password"
            autofocus
          />
        </div>
        <button type="submit" class="btn btn-primary login-btn" :disabled="loading">
          {{ loading ? 'Signing in...' : 'Sign In' }}
        </button>
      </form>
      <div v-if="hasPasskeys && !isRelay" class="divider">
        <span>or</span>
      </div>
      <button
        v-if="hasPasskeys"
        class="btn btn-ghost passkey-btn"
        :disabled="passkeyLoading"
        @click="loginWithPasskey"
      >
        🔑 {{ passkeyLoading ? 'Authenticating...' : 'Sign in with Passkey' }}
      </button>
      <div v-if="error" class="error-msg">{{ error }}</div>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  padding: 20px;
}
.login-card {
  width: 100%;
  max-width: 380px;
  text-align: center;
}
.logo-image {
  width: 72px;
  height: 72px;
  margin-bottom: 12px;
  filter: drop-shadow(0 0 12px rgba(245, 158, 11, 0.4));
}
.logo-text {
  font-size: 22px;
  font-weight: 700;
  background: var(--accent-gradient);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  margin-bottom: 24px;
}
.login-btn {
  width: 100%;
  justify-content: center;
  margin-top: 8px;
}
.divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 16px 0;
  color: var(--text-muted);
  font-size: 13px;
}
.divider::before,
.divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.passkey-btn {
  width: 100%;
  justify-content: center;
}
.error-msg {
  margin-top: 12px;
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 13px;
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  color: var(--error);
}
</style>
