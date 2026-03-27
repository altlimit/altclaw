<script setup lang="ts">
import { ref, onMounted } from 'vue'

interface PasskeyInfo {
  id: string
  name: string
  created_at: string
}

const passkeys = ref<PasskeyInfo[]>([])
const newName = ref('My Passkey')
const status = ref({ msg: '', type: '' })
const registering = ref(false)

async function loadPasskeys() {
  try {
    const resp = await fetch('/api/passkeys')
    const data = await resp.json()
    passkeys.value = data.passkeys || []
  } catch (e: any) {
    showStatus('Failed to load passkeys: ' + e.message, 'error')
  }
}

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

async function registerPasskey() {
  registering.value = true
  status.value = { msg: '', type: '' }
  try {
    // 1. Begin registration
    const beginResp = await fetch('/api/passkey-register-begin', { method: 'POST' })
    if (!beginResp.ok) throw new Error(await beginResp.text())
    const options = await beginResp.json()

    // 2. Convert options for navigator.credentials.create
    const publicKey = options.publicKey
    publicKey.challenge = base64urlToBuffer(publicKey.challenge)
    publicKey.user.id = base64urlToBuffer(publicKey.user.id)
    if (publicKey.excludeCredentials) {
      publicKey.excludeCredentials = publicKey.excludeCredentials.map((c: any) => ({
        ...c,
        id: base64urlToBuffer(c.id),
      }))
    }

    // 3. Create credential
    const credential = await navigator.credentials.create({ publicKey }) as PublicKeyCredential
    if (!credential) throw new Error('Credential creation cancelled')

    const attestation = credential.response as AuthenticatorAttestationResponse

    // 4. Send to server
    const body = JSON.stringify({
      id: credential.id,
      rawId: bufferToBase64url(credential.rawId),
      type: credential.type,
      response: {
        attestationObject: bufferToBase64url(attestation.attestationObject),
        clientDataJSON: bufferToBase64url(attestation.clientDataJSON),
      },
    })

    const name = encodeURIComponent(newName.value || 'Passkey')
    const finishResp = await fetch(`/api/passkey-register-finish?name=${name}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    })
    if (!finishResp.ok) throw new Error(await finishResp.text())

    showStatus('Passkey registered!', 'success')
    newName.value = 'My Passkey'
    await loadPasskeys()
  } catch (e: any) {
    showStatus('Registration failed: ' + e.message, 'error')
  } finally {
    registering.value = false
  }
}

async function deletePasskey(id: string) {
  try {
    const resp = await fetch('/api/delete-passkey', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    })
    if (!resp.ok) throw new Error(await resp.text())
    showStatus('Passkey removed', 'success')
    await loadPasskeys()
  } catch (e: any) {
    showStatus('Delete failed: ' + e.message, 'error')
  }
}

function showStatus(msg: string, type: string) {
  status.value = { msg, type }
  if (type === 'success') {
    setTimeout(() => { status.value = { msg: '', type: '' } }, 3000)
  }
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  })
}

onMounted(loadPasskeys)
</script>

<template>
  <div class="container">
    <h2 class="page-title">Security</h2>

    <div class="section">
      <div class="section-title">Passkeys</div>
      <p class="hint" style="margin-bottom: 16px">
        Passkeys let you sign in using biometrics (fingerprint, face) or your device PIN instead of a password.
      </p>

      <div v-if="passkeys.length" class="passkey-list">
        <div v-for="pk in passkeys" :key="pk.id" class="passkey-item">
          <div class="passkey-info">
            <div class="passkey-name">🔑 {{ pk.name }}</div>
            <div class="passkey-date">Added {{ formatDate(pk.created_at) }}</div>
          </div>
          <button class="btn btn-ghost remove-btn" @click="deletePasskey(pk.id)">Remove</button>
        </div>
      </div>
      <div v-else class="empty-state">
        No passkeys registered yet.
      </div>

      <div class="register-section">
        <div class="row" style="align-items: flex-end">
          <div class="field" style="flex: 1">
            <label>Passkey Name</label>
            <input v-model="newName" placeholder="My Passkey" />
          </div>
          <button
            class="btn btn-primary"
            :disabled="registering"
            @click="registerPasskey"
          >
            {{ registering ? 'Registering...' : '+ Add Passkey' }}
          </button>
        </div>
      </div>
    </div>

    <div v-if="status.msg" :class="['status-msg', status.type]">{{ status.msg }}</div>
  </div>
</template>

<style scoped>
.page-title {
  font-size: 20px;
  font-weight: 700;
  margin-bottom: 20px;
}
.passkey-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 20px;
}
.passkey-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--bg-tertiary);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px 16px;
}
.passkey-name {
  font-weight: 600;
  font-size: 14px;
}
.passkey-date {
  font-size: 12px;
  color: var(--text-muted);
  margin-top: 2px;
}
.remove-btn {
  font-size: 12px;
  padding: 4px 12px;
}
.empty-state {
  color: var(--text-muted);
  font-size: 14px;
  padding: 20px 0;
}
.register-section {
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}
.status-msg {
  margin-top: 16px;
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 13px;
}
.status-msg.success {
  background: rgba(34, 197, 94, 0.1);
  border: 1px solid rgba(34, 197, 94, 0.3);
  color: var(--success);
}
.status-msg.error {
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  color: var(--error);
}
</style>
