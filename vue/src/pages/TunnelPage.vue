<script setup lang="ts">
import { useWorkspaceStore } from '@/stores/workspace'
import { computed, onMounted, ref } from 'vue'

const wsStore = useWorkspaceStore()

const loading = ref(false)

// Pairing form
const pairCode = ref('')
const pairError = ref('')
const pairSuccess = ref('')

const tunnelLink = computed(() => {
  if (!wsStore.tunnelUrl) return ''
  const proto = globalThis.location?.protocol || 'https:'
  return `${proto}//${wsStore.tunnelUrl}`
})

async function loadStatus() {
  try {
    const resp = await fetch('/api/tunnel-status')
    if (resp.ok) {
      const data = await resp.json()
      wsStore.tunnelStatus = data.status || 'disconnected'
      wsStore.tunnelPaired = data.paired || false
      wsStore.tunnelHubUrl = data.hub_url || ''
      wsStore.patch({ tunnel_host: data.url || '' })
    }
  } catch { /* ignore */ }
}

async function connect() {
  loading.value = true
  try {
    const resp = await fetch('/api/tunnel-connect', { method: 'POST' })
    if (resp.ok) {
      const data = await resp.json()
      wsStore.tunnelStatus = data.status
    }
  } catch { /* ignore */ }
  loading.value = false
}

async function disconnect() {
  loading.value = true
  try {
    const resp = await fetch('/api/tunnel-disconnect', { method: 'POST' })
    if (resp.ok) {
      wsStore.tunnelStatus = 'disconnected'
      wsStore.patch({ tunnel_host: '' })
    }
  } catch { /* ignore */ }
  loading.value = false
}

async function pair() {
  pairError.value = ''
  pairSuccess.value = ''
  if (!pairCode.value.trim()) {
    pairError.value = 'Enter the 6-digit code from the hub'
    return
  }
  loading.value = true
  try {
    const resp = await fetch('/api/tunnel-pair', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code: pairCode.value.trim() }),
    })
    const data = await resp.json()
    if (resp.ok) {
      pairSuccess.value = `Paired! Hostname: ${data.hostname}-relay.${data.domain}`
      wsStore.tunnelPaired = true
      pairCode.value = ''
      await loadStatus()
    } else {
      pairError.value = data.error || data.message || 'Pairing failed'
    }
  } catch (e) {
    pairError.value = 'Failed to reach server'
  }
  loading.value = false
}

async function unpair() {
  loading.value = true
  try {
    const resp = await fetch('/api/tunnel-unpair', { method: 'POST' })
    if (resp.ok) {
      wsStore.tunnelPaired = false
      wsStore.tunnelHubUrl = ''
      pairSuccess.value = ''
    }
  } catch { /* ignore */ }
  loading.value = false
}

onMounted(() => {
  loadStatus()
})
</script>

<template>
  <div class="container">
    <h2 class="page-title">🌐 Tunnel</h2>
    <p class="page-desc">Make this workspace accessible over the public internet.</p>

    <!-- Status Section -->
    <div class="section">
      <h3 class="section-title">Status</h3>
      <div class="status-row">
        <span class="status-badge" :class="'status-' + wsStore.tunnelStatus">
          <template v-if="wsStore.tunnelStatus === 'connected'">● Connected</template>
          <template v-else-if="wsStore.tunnelStatus === 'connecting'">◌ Connecting...</template>
          <template v-else>○ Disconnected</template>
        </span>
        <span v-if="wsStore.tunnelStatus == 'connected' && wsStore.tunnelUrl" class="tunnel-url">
          <a :href="tunnelLink" target="_blank">{{ wsStore.tunnelUrl }}</a>
        </span>
      </div>
      <div class="status-actions">
        <template v-if="wsStore.tunnelStatus === 'disconnected'">
          <button class="btn btn-primary" @click="connect" :disabled="loading">
            🚀 Go Public
          </button>
        </template>
        <template v-else>
          <button class="btn btn-ghost" @click="disconnect" :disabled="loading">
            Disconnect
          </button>
        </template>
      </div>
      <p class="hint" v-if="wsStore.tunnelStatus === 'disconnected'">
        "Go Public" creates a password-protected public URL. No hub account needed.
      </p>
    </div>

    <!-- Pair with Hub Section -->
    <div class="section">
      <h3 class="section-title">Pair with Hub</h3>
      <p class="hint" style="margin-bottom: 16px">
        Pairing allows hub users to access this workspace from the hub.
        Get a 6-digit code from your hub dashboard.
      </p>
      <div v-if="wsStore.tunnelPaired" class="paired-info">
        <span class="status-badge status-online">✓ Paired</span>
        <span v-if="wsStore.tunnelHubUrl" class="paired-hub">Hub: {{ wsStore.tunnelHubUrl }}</span>
        <button class="btn btn-ghost btn-sm" @click="unpair" :disabled="loading">Unpair</button>
      </div>
      <div v-else-if="wsStore.tunnelStatus !== 'connected'" class="hint">
        Connect the tunnel first before pairing with a hub.
      </div>
      <div v-else class="pair-form">
        <div class="field">
          <label>Pairing Code</label>
          <input
            v-model="pairCode"
            placeholder="123456"
            maxlength="6"
            class="pair-code-input"
            @keyup.enter="pair"
          />
        </div>
        <div v-if="pairError" class="error-msg">{{ pairError }}</div>
        <div v-if="pairSuccess" class="success-msg">{{ pairSuccess }}</div>
        <button class="btn btn-primary" @click="pair" :disabled="loading || !pairCode.trim()">
          Register
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-title {
  font-size: 22px;
  font-weight: 700;
  margin-bottom: 4px;
}
.page-desc {
  color: var(--text-muted);
  font-size: 14px;
  margin-bottom: 24px;
}
.status-row {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
}
.status-connecting {
  background: rgba(245, 158, 11, 0.15);
  color: var(--warning);
}
.status-connected {
  background: rgba(16, 185, 129, 0.15);
  color: var(--success);
}
.status-disconnected {
  background: rgba(113, 113, 122, 0.15);
  color: var(--text-muted);
}
.tunnel-url a {
  color: var(--accent-light);
  font-family: monospace;
  font-size: 14px;
}
.status-actions {
  display: flex;
  gap: 12px;
  margin-bottom: 8px;
}
.paired-info {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.paired-hub {
  color: var(--text-secondary);
  font-size: 13px;
}
.pair-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.pair-code-input {
  font-size: 24px;
  letter-spacing: 6px;
  text-align: center;
  font-family: monospace;
  font-weight: 700;
}
.error-msg {
  color: var(--error);
  font-size: 13px;
}
.success-msg {
  color: var(--success);
  font-size: 13px;
}
</style>
