<script setup lang="ts">
import { onMounted, ref } from 'vue'

interface HistoryEntry {
  id: number
  agent_type?: string
  provider?: string
  iteration?: number
  block?: number
  code?: string
  created: string
}

const entries = ref<HistoryEntry[]>([])
const expandedId = ref<number | null>(null)
const loading = ref(false)

async function loadHistory() {
  loading.value = true
  try {
    const resp = await fetch('/api/history')
    const data = await resp.json()
    entries.value = data.entries || []
  } catch {
    entries.value = []
  } finally {
    loading.value = false
  }
}

async function toggleEntry(entry: HistoryEntry) {
  if (expandedId.value === entry.id) {
    expandedId.value = null
    return
  }
  if (!entry.code) {
    try {
      const resp = await fetch('/api/history/' + entry.id)
      const data = await resp.json()
      entry.code = data.code || '(empty)'
    } catch {
      entry.code = '(error loading)'
    }
  }
  expandedId.value = entry.id
}

function formatTime(iso: string) {
  const d = new Date(iso)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

onMounted(loadHistory)

defineExpose({ refresh: loadHistory })
</script>

<template>
  <div class="history-panel">
    <div class="panel-header">
      <span>History</span>
      <button class="refresh-btn" @click="loadHistory" title="Refresh">↻</button>
    </div>
    <div v-if="loading && !entries.length" class="empty">Loading...</div>
    <div v-else-if="!entries.length" class="empty">No history yet</div>
    <div v-else class="history-list">
      <div
        v-for="entry in entries"
        :key="entry.id"
        class="history-item"
        :class="{ expanded: expandedId === entry.id }"
        @click="toggleEntry(entry)"
      >
        <div class="item-header">
          <span class="item-id">#{{ entry.id }}</span>
          <span class="item-meta">{{ entry.provider }} · iter {{ entry.iteration }}</span>
          <span class="item-time">{{ formatTime(entry.created) }}</span>
        </div>
        <div v-if="expandedId === entry.id && entry.code" class="item-code">
          <pre>{{ entry.code }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.history-panel {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.panel-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.refresh-btn {
  background: none;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 14px;
  padding: 2px 6px;
  border-radius: 4px;
  transition: color 0.15s, background 0.15s;
}
.refresh-btn:hover {
  color: var(--text-primary);
  background: var(--bg-secondary);
}
.empty {
  padding: 24px 12px;
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
}
.history-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}
.history-item {
  cursor: pointer;
  border-bottom: 1px solid var(--border);
  transition: background 0.15s;
}
.history-item:hover {
  background: var(--bg-secondary);
}
.history-item.expanded {
  background: var(--bg-secondary);
}
.item-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  font-size: 12px;
}
.item-id {
  color: var(--accent);
  font-weight: 600;
  font-family: monospace;
  min-width: 40px;
}
.item-meta {
  color: var(--text-muted);
  font-size: 11px;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.item-time {
  color: var(--text-muted);
  font-size: 11px;
  flex-shrink: 0;
}
.item-code {
  padding: 0 12px 8px;
}
.item-code pre {
  margin: 0;
  padding: 8px;
  background: var(--bg-tertiary);
  border: 1px solid var(--border);
  border-radius: 4px;
  font-size: 11px;
  line-height: 1.4;
  max-height: 200px;
  overflow: auto;
  color: var(--text-primary);
  white-space: pre;
}
</style>
