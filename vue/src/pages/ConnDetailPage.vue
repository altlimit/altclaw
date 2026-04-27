<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed } from 'vue'
import { useEditorStore } from '@/stores/editor'
import { useEventStore } from '@/stores/events'

const props = defineProps<{ connId: number }>()
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

const conn = ref<Connection | null>(null)
const loading = ref(true)
const error = ref('')

async function loadConn() {
  loading.value = true
  error.value = ''
  try {
    const resp = await fetch('/api/connections')
    if (!resp.ok) throw new Error(await resp.text())
    const list: Connection[] = await resp.json()
    conn.value = list.find(c => c.id === props.connId) || null
    if (!conn.value) error.value = 'Connection not found'
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function closeConn() {
  if (!conn.value) return
  try {
    const resp = await fetch('/api/connections/' + conn.value.id, { method: 'DELETE' })
    if (!resp.ok) throw new Error(await resp.text())
    editorStore.closeFile(`special://connection-${props.connId}`)
  } catch (e: any) {
    error.value = 'Close failed: ' + e.message
  }
}

function openHandler() {
  if (!conn.value) return
  const handler = conn.value.handler
  // If handler looks like a file path, open it in editor
  if (handler.endsWith('.js') || handler.includes('/')) {
    editorStore.openFile(handler, handler.split('/').pop() || handler)
  }
}

function statusClass(status: string): string {
  switch (status) {
    case 'connected': return 'st-connected'
    case 'connecting':
    case 'reconnecting': return 'st-reconnecting'
    case 'error': return 'st-error'
    default: return 'st-closed'
  }
}

function formatTime(ts: string): string {
  if (!ts) return '—'
  try {
    const d = new Date(ts)
    return d.toLocaleString()
  } catch {
    return ts
  }
}

const uptime = computed(() => {
  if (!conn.value?.connected_at) return '—'
  const start = new Date(conn.value.connected_at).getTime()
  const now = Date.now()
  const secs = Math.floor((now - start) / 1000)
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  return `${h}h ${m}m`
})

function onConnEvent() {
  loadConn()
}

onMounted(() => {
  loadConn()
  eventStore.on('conn_updated', onConnEvent)
})

onUnmounted(() => {
  eventStore.off('conn_updated', onConnEvent)
})
</script>

<template>
  <div class="conn-detail">
    <div v-if="loading" class="conn-loading">Loading…</div>
    <div v-else-if="error" class="conn-error">{{ error }}</div>
    <template v-else-if="conn">
      <div class="conn-header">
        <div class="conn-header-left">
          <span class="conn-status-badge" :class="statusClass(conn.status)">
            {{ conn.status }}
          </span>
          <span class="conn-type-badge">{{ conn.type.toUpperCase() }}</span>
        </div>
        <button class="conn-close-btn" @click="closeConn">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6L6 18M6 6l12 12"/></svg>
          Close Connection
        </button>
      </div>

      <div class="conn-section">
        <div class="conn-label">URL</div>
        <div class="conn-value conn-url-value">{{ conn.url }}</div>
      </div>

      <div class="conn-section">
        <div class="conn-label">Handler</div>
        <div class="conn-value conn-handler" @click="openHandler">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 3v4a1 1 0 0 0 1 1h4"/><path d="M17 21H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h7l5 5v11a2 2 0 0 1-2 2z"/></svg>
          {{ conn.handler }}
        </div>
      </div>

      <div class="conn-stats-grid">
        <div class="stat-card">
          <div class="stat-number">{{ conn.messages_in.toLocaleString() }}</div>
          <div class="stat-label">Messages</div>
        </div>
        <div class="stat-card" :class="{ 'stat-warn': conn.errors > 0 }">
          <div class="stat-number">{{ conn.errors.toLocaleString() }}</div>
          <div class="stat-label">Errors</div>
        </div>
        <div class="stat-card">
          <div class="stat-number">{{ conn.reconnects.toLocaleString() }}</div>
          <div class="stat-label">Reconnects</div>
        </div>
        <div class="stat-card">
          <div class="stat-number">{{ uptime }}</div>
          <div class="stat-label">Uptime</div>
        </div>
      </div>

      <div class="conn-meta-grid">
        <div class="conn-section">
          <div class="conn-label">Connected At</div>
          <div class="conn-value">{{ formatTime(conn.connected_at) }}</div>
        </div>
        <div class="conn-section">
          <div class="conn-label">Created At</div>
          <div class="conn-value">{{ formatTime(conn.created_at) }}</div>
        </div>
        <div class="conn-section">
          <div class="conn-label">Connection ID</div>
          <div class="conn-value conn-id">{{ conn.id }}</div>
        </div>
        <div class="conn-section" v-if="conn.last_message">
          <div class="conn-label">Last Message</div>
          <div class="conn-value conn-last-msg">{{ conn.last_message }}</div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.conn-detail {
  padding: 32px;
  max-width: 720px;
  margin: 0 auto;
  font-size: 14px;
}
.conn-loading, .conn-error {
  text-align: center;
  padding: 48px;
  color: var(--text-muted);
}
.conn-error { color: var(--error); }

/* Header */
.conn-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 28px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--border);
}
.conn-header-left {
  display: flex;
  align-items: center;
  gap: 10px;
}
.conn-status-badge {
  font-size: 12px;
  font-weight: 600;
  padding: 4px 12px;
  border-radius: 12px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.conn-status-badge.st-connected {
  background: rgba(34, 197, 94, 0.15);
  color: #22c55e;
}
.conn-status-badge.st-reconnecting {
  background: rgba(245, 158, 11, 0.15);
  color: #f59e0b;
}
.conn-status-badge.st-error {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}
.conn-status-badge.st-closed {
  background: rgba(107, 114, 128, 0.15);
  color: var(--text-muted);
}
.conn-type-badge {
  font-size: 11px;
  font-weight: 700;
  padding: 3px 10px;
  border-radius: 6px;
  background: rgba(99, 102, 241, 0.12);
  color: var(--accent-light);
  letter-spacing: 0.5px;
}
.conn-close-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.15s;
}
.conn-close-btn:hover {
  color: var(--error);
  border-color: var(--error);
  background: rgba(239, 68, 68, 0.06);
}

/* Sections */
.conn-section {
  margin-bottom: 20px;
}
.conn-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted);
  margin-bottom: 4px;
}
.conn-value {
  font-size: 14px;
  color: var(--text-primary);
  word-break: break-all;
}
.conn-url-value {
  font-family: monospace;
  font-size: 13px;
  padding: 8px 12px;
  background: var(--bg-secondary);
  border-radius: 6px;
  border: 1px solid var(--border);
}
.conn-handler {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 6px;
  font-family: monospace;
  font-size: 13px;
  cursor: pointer;
  transition: all 0.15s;
  color: var(--accent-light);
}
.conn-handler:hover {
  border-color: var(--accent);
  background: rgba(99, 102, 241, 0.06);
}
.conn-id {
  font-family: monospace;
  font-size: 12px;
  color: var(--text-dim);
}
.conn-last-msg {
  font-family: monospace;
  font-size: 12px;
  color: var(--text-dim);
  max-height: 60px;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Stats grid */
.conn-stats-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin-bottom: 28px;
}
.stat-card {
  padding: 16px;
  border-radius: 8px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  text-align: center;
  transition: border-color 0.15s;
}
.stat-card.stat-warn {
  border-color: rgba(239, 68, 68, 0.3);
}
.stat-number {
  font-size: 22px;
  font-weight: 700;
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
}
.stat-warn .stat-number { color: var(--error); }
.stat-label {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted);
  margin-top: 2px;
}

/* Meta grid */
.conn-meta-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 24px;
}

@media (max-width: 600px) {
  .conn-stats-grid { grid-template-columns: repeat(2, 1fr); }
  .conn-meta-grid { grid-template-columns: 1fr; }
}
</style>
