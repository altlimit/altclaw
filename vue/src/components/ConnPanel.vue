<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useEditorStore } from '@/stores/editor'
import { useEventStore } from '@/stores/events'

const editorStore = useEditorStore()
const eventStore = useEventStore()

interface Connection {
  id: number
  chat_id: number
  type: string
  url: string
  handler: string
  status: string
  connected_at: string
  messages_in: number
  last_message: string
  errors: number
  reconnects: number
  created_at: string
}

const conns = ref<Connection[]>([])
const loading = ref(true)
const error = ref('')

async function loadConns() {
  loading.value = true
  error.value = ''
  try {
    const resp = await fetch('/api/connections')
    if (!resp.ok) throw new Error(await resp.text())
    conns.value = await resp.json()
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function deleteConn(id: number) {
  try {
    const resp = await fetch('/api/connections/' + id, { method: 'DELETE' })
    if (!resp.ok) throw new Error(await resp.text())
    conns.value = conns.value.filter(c => c.id !== id)
  } catch (e: any) {
    error.value = 'Delete failed: ' + e.message
  }
}

function openHandler(conn: Connection) {
  const label = `⚡ ${shortenUrl(conn.url)}`
  editorStore.openSpecialTab('connection', label, conn.id)
}

function statusIcon(status: string): string {
  switch (status) {
    case 'connected': return '●'
    case 'connecting': return '◌'
    case 'reconnecting': return '◌'
    case 'error': return '✕'
    default: return '○'
  }
}

function statusClass(status: string): string {
  switch (status) {
    case 'connected': return 'status-connected'
    case 'connecting':
    case 'reconnecting': return 'status-reconnecting'
    case 'error': return 'status-error'
    default: return 'status-closed'
  }
}

function shortenUrl(url: string): string {
  try {
    const u = new URL(url)
    return u.hostname + (u.pathname !== '/' ? u.pathname : '')
  } catch {
    return url
  }
}

function formatCount(n: number): string {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
  return String(n)
}

function onConnEvent(evt: any) {
  if (evt.action === 'deleted' && evt.id) {
    conns.value = conns.value.filter(c => String(c.id) !== evt.id)
    return
  }
  loadConns()
}

onMounted(() => {
  loadConns()
  eventStore.on('conn_updated', onConnEvent)
})

onUnmounted(() => {
  eventStore.off('conn_updated', onConnEvent)
})
</script>

<template>
  <div class="conn-panel">
    <div class="panel-header">
      <span class="panel-title">Connections</span>
      <button class="refresh-btn" @click="loadConns" :disabled="loading" title="Refresh">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>
        </svg>
      </button>
    </div>

    <div class="panel-list">
      <div v-if="loading" class="panel-empty">Loading…</div>
      <div v-else-if="error" class="panel-error">{{ error }}</div>
      <div v-else-if="conns.length === 0" class="panel-empty">
        <span>No active connections</span>
      </div>
      <div
        v-for="conn in conns"
        :key="conn.id"
        class="conn-item"
        @click="openHandler(conn)"
      >
        <div class="conn-top">
          <span class="conn-url" :title="conn.url">{{ shortenUrl(conn.url) }}</span>
          <button class="conn-delete" @click.stop="deleteConn(conn.id)" title="Close">✕</button>
        </div>
        <div class="conn-meta">
          <span class="conn-status" :class="statusClass(conn.status)">
            {{ statusIcon(conn.status) }} {{ conn.status }}
          </span>
          <span class="conn-type">{{ conn.type }}</span>
        </div>
        <div class="conn-stats">
          <span class="conn-stat" title="Messages received">↑ {{ formatCount(conn.messages_in) }} msgs</span>
          <span class="conn-stat" v-if="conn.errors > 0" title="Errors">{{ conn.errors }} errors</span>
          <span class="conn-stat" v-if="conn.reconnects > 0" title="Reconnections">{{ conn.reconnects }} reconn</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.conn-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.panel-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px;
  border-bottom: 1px solid var(--border);
}
.panel-title {
  font-weight: 600;
  font-size: 13px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted);
}
.refresh-btn {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  padding: 4px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  transition: color 0.15s, background 0.15s;
}
.refresh-btn:hover {
  color: var(--text-primary);
  background: var(--bg-secondary);
}
.panel-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}
.panel-empty {
  text-align: center;
  color: var(--text-muted);
  padding: 24px;
  font-size: 13px;
}
.panel-error {
  padding: 12px;
  font-size: 12px;
  color: var(--error);
}

/* Connection item */
.conn-item {
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.15s;
  border-bottom: 1px solid var(--border);
}
.conn-item:last-child {
  border-bottom: none;
}
.conn-item:hover {
  background: var(--bg-secondary);
}
.conn-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 4px;
}
.conn-url {
  font-size: 12px;
  font-weight: 600;
  font-family: monospace;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  min-width: 0;
}
.conn-delete {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 14px;
  padding: 2px 4px;
  border-radius: 3px;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s;
  flex-shrink: 0;
}
.conn-item:hover .conn-delete {
  opacity: 1;
}
.conn-delete:hover {
  color: var(--error);
}
.conn-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 3px;
}
.conn-status {
  font-size: 10px;
  font-weight: 500;
  padding: 1px 5px;
  border-radius: 3px;
  white-space: nowrap;
}
.conn-status.status-connected {
  background: rgba(34, 197, 94, 0.1);
  color: var(--success);
}
.conn-status.status-reconnecting {
  background: rgba(245, 158, 11, 0.1);
  color: #f59e0b;
}
.conn-status.status-error {
  background: rgba(239, 68, 68, 0.1);
  color: var(--error);
}
.conn-status.status-closed {
  background: rgba(99, 102, 241, 0.05);
  color: var(--text-dim);
}
.conn-type {
  font-size: 10px;
  font-weight: 500;
  padding: 1px 5px;
  border-radius: 3px;
  background: rgba(99, 102, 241, 0.1);
  color: var(--accent-light);
  white-space: nowrap;
}
.conn-stats {
  display: flex;
  align-items: center;
  gap: 8px;
}
.conn-stat {
  font-size: 10px;
  color: var(--text-dim);
  white-space: nowrap;
}
</style>
