<script setup lang="ts">
import { useEditorStore } from '@/stores/editor'
import { useEventStore } from '@/stores/events'
import { onMounted, onUnmounted, ref } from 'vue'

const editorStore = useEditorStore()
const eventStore = useEventStore()

interface MemEntry {
  id: number
  ns: string
  kind: string
  content: string
  created: string
  modified: string
}

const workspaceEntries = ref<MemEntry[]>([])
const userEntries = ref<MemEntry[]>([])
const loading = ref(true)
const error = ref('')
const wsHasMore = ref(false)
const userHasMore = ref(false)
let wsCursor = ''
let userCursor = ''

async function loadEntries(append = false) {
  loading.value = true
  error.value = ''
  try {
    const url = new URL('/api/memory-entries', window.location.origin)
    url.searchParams.set('limit', '20')
    if (wsCursor && append) url.searchParams.set('ws_cursor', wsCursor)
    if (userCursor && append) url.searchParams.set('user_cursor', userCursor)
    const resp = await fetch(url.toString())
    if (!resp.ok) throw new Error(await resp.text())
    const data = await resp.json()
    if (append) {
      workspaceEntries.value = [...workspaceEntries.value, ...(data.workspace || [])]
      userEntries.value = [...userEntries.value, ...(data.user || [])]
    } else {
      workspaceEntries.value = data.workspace || []
      userEntries.value = data.user || []
    }
    wsCursor = data.ws_cursor || ''
    userCursor = data.user_cursor || ''
    wsHasMore.value = wsCursor !== ''
    userHasMore.value = userCursor !== ''
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function loadMore() {
  if ((wsHasMore.value || userHasMore.value) && !loading.value) {
    loadEntries(true)
  }
}

function refresh() {
  wsCursor = ''
  userCursor = ''
  loadEntries(false)
}

function preview(content: string): string {
  const first = content.trim().split('\n')[0] || ''
  return first.length > 60 ? first.slice(0, 57) + '…' : first
}

function relativeAge(iso: string): string {
  if (!iso) return ''
  const ms = Date.now() - new Date(iso).getTime()
  const mins = Math.floor(ms / 60000)
  if (mins < 60) return `${mins}m`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h`
  return `${Math.floor(hrs / 24)}d`
}

function openEntry(entry: MemEntry) {
  const path = `memory://entry-${entry.ns}-${entry.id}`
  const label = `Memory #${entry.id} (${entry.ns})`
  editorStore.openMemoryEntryTab(path, label, entry.content, entry.id, entry.ns)
}

async function deleteEntry(entry: MemEntry) {
  try {
    const scope = entry.ns ? 'workspace' : 'user'
    const resp = await fetch(`/api/memory-entries/${scope}/${entry.id}`, { method: 'DELETE' })
    if (!resp.ok) throw new Error(await resp.text())
    workspaceEntries.value = workspaceEntries.value.filter(e => e.id !== entry.id)
    userEntries.value = userEntries.value.filter(e => e.id !== entry.id)
    editorStore.closeFile(`memory://entry-${scope}-${entry.id}`)
  } catch (e: any) {
    error.value = 'Delete failed: ' + e.message
  }
}

function onMemoryEvent(evt: any) {
  if (evt.action === 'deleted' && evt.id) {
    workspaceEntries.value = workspaceEntries.value.filter(e => e.id !== evt.id)
    userEntries.value = userEntries.value.filter(e => e.id !== evt.id)
    editorStore.closeFile(`memory://entry-${evt.ns}-${evt.id}`)
    return
  }
  refresh()
}

onMounted(() => {
  loadEntries()
  eventStore.on('memory_updated', onMemoryEvent)
})

onUnmounted(() => {
  eventStore.off('memory_updated', onMemoryEvent)
})
</script>

<template>
  <div class="memory-panel">
    <div class="panel-header">
      <span class="panel-title">Memory</span>
      <button class="refresh-btn" @click="refresh" :disabled="loading" title="Refresh">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>
        </svg>
      </button>
    </div>

    <div class="panel-list">
      <div v-if="loading && !workspaceEntries.length && !userEntries.length" class="panel-empty">Loading…</div>
      <div v-else-if="error" class="panel-error">{{ error }}</div>
      <template v-else>
        <!-- Workspace Entries -->
        <div v-if="workspaceEntries.length > 0" class="mem-section">
          <div class="section-label">Workspace</div>
          <div
            v-for="entry in workspaceEntries"
            :key="'ws-' + entry.id"
            class="mem-item"
            @click="openEntry(entry)"
          >
            <div class="mem-top">
              <span class="mem-preview">{{ preview(entry.content) }}</span>
              <button class="mem-delete" @click.stop="deleteEntry(entry)" title="Delete">✕</button>
            </div>
            <div class="mem-meta">
              <span class="mem-kind" :class="entry.kind">{{ entry.kind }}</span>
              <span class="mem-age">{{ relativeAge(entry.created) }}</span>
            </div>
          </div>
        </div>

        <!-- User Entries -->
        <div v-if="userEntries.length > 0" class="mem-section">
          <div class="section-label">User</div>
          <div
            v-for="entry in userEntries"
            :key="'user-' + entry.id"
            class="mem-item"
            @click="openEntry(entry)"
          >
            <div class="mem-top">
              <span class="mem-preview">{{ preview(entry.content) }}</span>
              <button class="mem-delete" @click.stop="deleteEntry(entry)" title="Delete">✕</button>
            </div>
            <div class="mem-meta">
              <span class="mem-kind" :class="entry.kind">{{ entry.kind }}</span>
              <span class="mem-age">{{ relativeAge(entry.created) }}</span>
            </div>
          </div>
        </div>

        <div v-if="workspaceEntries.length === 0 && userEntries.length === 0" class="panel-empty">
          <span>No memory entries</span>
          <span class="panel-hint">Use mem.add() from chat to save knowledge</span>
        </div>

        <button v-if="wsHasMore || userHasMore" class="view-more-btn" @click="loadMore" :disabled="loading">
          {{ loading ? 'Loading…' : 'View More' }}
        </button>
      </template>
    </div>
  </div>
</template>

<style scoped>
.memory-panel {
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
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.panel-hint {
  font-size: 11px;
  color: var(--text-dim);
}
.panel-error {
  padding: 12px;
  font-size: 12px;
  color: var(--error);
}
.mem-section {
  margin-bottom: 4px;
}
.section-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-dim);
  padding: 8px 12px 4px;
}
.mem-item {
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.15s;
  border-bottom: 1px solid var(--border);
}
.mem-item:last-child {
  border-bottom: none;
}
.mem-item:hover {
  background: var(--bg-secondary);
}
.mem-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 4px;
}
.mem-preview {
  font-size: 13px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  margin-right: 8px;
}
.mem-delete {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 14px;
  padding: 2px 4px;
  border-radius: 3px;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s;
}
.mem-item:hover .mem-delete {
  opacity: 1;
}
.mem-delete:hover {
  color: var(--error);
}
.mem-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}
.mem-kind {
  font-size: 10px;
  font-weight: 500;
  padding: 1px 5px;
  border-radius: 3px;
  white-space: nowrap;
}
.mem-kind.core {
  background: rgba(99, 102, 241, 0.1);
  color: var(--accent-light);
}
.mem-kind.learned {
  background: rgba(34, 197, 94, 0.1);
  color: var(--success);
}
.mem-kind.note {
  background: rgba(245, 158, 11, 0.1);
  color: #f59e0b;
}
.mem-age {
  font-size: 10px;
  color: var(--text-dim);
  white-space: nowrap;
}
.view-more-btn {
  display: block;
  width: 100%;
  padding: 8px 12px;
  background: transparent;
  border: none;
  border-top: 1px solid var(--border);
  color: var(--accent);
  font-size: 12px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}
.view-more-btn:hover:not(:disabled) {
  background: var(--bg-secondary);
}
.view-more-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
