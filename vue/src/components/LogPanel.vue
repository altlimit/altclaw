<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch, nextTick } from 'vue'

interface LogEntry {
  time: string
  level: string
  msg: string
  attrs?: Record<string, string>
}

const entries = ref<LogEntry[]>([])
const loading = ref(false)
const error = ref('')
const searchQuery = ref('')
const activeLevels = ref<Set<string>>(new Set(['DEBUG', 'INFO', 'WARN', 'ERROR']))
const autoRefresh = ref(true)
const expandedIdx = ref<Set<number>>(new Set())
const listEl = ref<HTMLElement | null>(null)
const autoScroll = ref(true)

let pollTimer: ReturnType<typeof setInterval> | null = null

const allLevels = ['DEBUG', 'INFO', 'WARN', 'ERROR'] as const

async function fetchLogs() {
  try {
    const url = new URL('/api/logs', window.location.origin)
    url.searchParams.set('limit', '200')
    if (searchQuery.value.trim()) {
      url.searchParams.set('query', searchQuery.value.trim())
    }
    const levelArr = Array.from(activeLevels.value)
    if (levelArr.length < allLevels.length) {
      url.searchParams.set('level', levelArr.join(','))
    }
    const resp = await fetch(url.toString())
    if (!resp.ok) throw new Error(await resp.text())
    const data = await resp.json()
    entries.value = data.entries || []
    if (autoScroll.value) {
      nextTick(() => scrollToBottom())
    }
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function scrollToBottom() {
  if (listEl.value) {
    listEl.value.scrollTop = listEl.value.scrollHeight
  }
}

function toggleLevel(level: string) {
  const s = new Set(activeLevels.value)
  if (s.has(level)) {
    if (s.size > 1) s.delete(level) // keep at least one
  } else {
    s.add(level)
  }
  activeLevels.value = s
}

function toggleExpand(idx: number) {
  const s = new Set(expandedIdx.value)
  if (s.has(idx)) {
    s.delete(idx)
  } else {
    s.add(idx)
  }
  expandedIdx.value = s
}

function toggleAutoRefresh() {
  autoRefresh.value = !autoRefresh.value
}

function clearSearch() {
  searchQuery.value = ''
}

function relativeTime(iso: string): string {
  if (!iso) return ''
  const ms = Date.now() - new Date(iso).getTime()
  const secs = Math.floor(ms / 1000)
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h`
  return `${Math.floor(hrs / 24)}d`
}

function levelClass(level: string): string {
  switch (level) {
    case 'DEBUG': return 'level-debug'
    case 'INFO': return 'level-info'
    case 'WARN': return 'level-warn'
    case 'ERROR': return 'level-error'
    default: return 'level-info'
  }
}

const filteredEntries = computed(() => entries.value)

// Start / stop polling based on autoRefresh
function startPolling() {
  stopPolling()
  fetchLogs()
  pollTimer = setInterval(fetchLogs, 3000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

watch(autoRefresh, (on) => {
  if (on) startPolling()
  else stopPolling()
})

// Re-fetch when filters change
watch([activeLevels, searchQuery], () => {
  fetchLogs()
}, { deep: true })

onMounted(() => {
  if (autoRefresh.value) startPolling()
})

onUnmounted(() => {
  stopPolling()
})
</script>

<template>
  <div class="log-panel">
    <div class="panel-header">
      <span class="panel-title">Logs</span>
      <div class="header-actions">
        <button
          class="action-btn"
          :class="{ active: autoRefresh }"
          @click="toggleAutoRefresh"
          :title="autoRefresh ? 'Pause auto-refresh' : 'Resume auto-refresh'"
        >
          <svg v-if="autoRefresh" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/></svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="5 3 19 12 5 21 5 3"/></svg>
        </button>
        <button class="action-btn" @click="fetchLogs" title="Refresh now">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>
          </svg>
        </button>
        <button
          class="action-btn"
          :class="{ active: autoScroll }"
          @click="autoScroll = !autoScroll"
          :title="autoScroll ? 'Auto-scroll ON' : 'Auto-scroll OFF'"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><polyline points="19 12 12 19 5 12"/></svg>
        </button>
      </div>
    </div>

    <!-- Search -->
    <div class="search-bar">
      <input
        v-model="searchQuery"
        type="text"
        placeholder="Search logs…"
        class="search-input"
        @keyup.enter="fetchLogs"
      />
      <button v-if="searchQuery" class="search-clear" @click="clearSearch">✕</button>
    </div>

    <!-- Level Filters -->
    <div class="level-filters">
      <button
        v-for="lv in allLevels"
        :key="lv"
        class="level-chip"
        :class="[levelClass(lv), { inactive: !activeLevels.has(lv) }]"
        @click="toggleLevel(lv)"
      >{{ lv }}</button>
    </div>

    <!-- Log List -->
    <div class="log-list" ref="listEl">
      <div v-if="loading && entries.length === 0" class="panel-empty">Loading…</div>
      <div v-else-if="error" class="panel-error">{{ error }}</div>
      <div v-else-if="filteredEntries.length === 0" class="panel-empty">
        <span>No log entries</span>
        <span class="panel-hint">Logs are captured from the application runtime</span>
      </div>
      <template v-else>
        <div
          v-for="(entry, idx) in filteredEntries"
          :key="idx"
          class="log-entry"
          :class="levelClass(entry.level)"
          @click="entry.attrs && Object.keys(entry.attrs).length > 0 && toggleExpand(idx)"
        >
          <div class="log-main">
            <span class="log-level-badge" :class="levelClass(entry.level)">{{ entry.level }}</span>
            <span class="log-msg">{{ entry.msg }}</span>
            <span class="log-time">{{ relativeTime(entry.time) }}</span>
          </div>
          <transition name="attrs-expand">
            <div v-if="expandedIdx.has(idx) && entry.attrs" class="log-attrs">
              <div v-for="(val, key) in entry.attrs" :key="key" class="log-attr">
                <span class="attr-key">{{ key }}</span>
                <span class="attr-val">{{ val }}</span>
              </div>
            </div>
          </transition>
          <div v-if="entry.attrs && Object.keys(entry.attrs).length > 0 && !expandedIdx.has(idx)" class="log-attrs-hint">
            <span v-for="(val, key) in entry.attrs" :key="key" class="attr-inline">
              <span class="attr-key-inline">{{ key }}</span>={{ val }}
            </span>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.log-panel {
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
.header-actions {
  display: flex;
  gap: 4px;
}
.action-btn {
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
.action-btn:hover {
  color: var(--text-primary);
  background: var(--bg-secondary);
}
.action-btn.active {
  color: var(--accent);
}

/* Search */
.search-bar {
  display: flex;
  align-items: center;
  padding: 6px 12px;
  border-bottom: 1px solid var(--border);
  gap: 4px;
}
.search-input {
  flex: 1;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 5px 8px;
  font-size: 12px;
  color: var(--text-primary);
  outline: none;
  transition: border-color 0.2s;
}
.search-input:focus {
  border-color: var(--accent);
}
.search-input::placeholder {
  color: var(--text-dim, var(--text-muted));
}
.search-clear {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 12px;
  padding: 2px 4px;
}
.search-clear:hover {
  color: var(--text-primary);
}

/* Level Filters */
.level-filters {
  display: flex;
  gap: 4px;
  padding: 6px 12px;
  border-bottom: 1px solid var(--border);
}
.level-chip {
  font-size: 10px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 10px;
  border: 1px solid transparent;
  cursor: pointer;
  transition: all 0.15s;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}
.level-chip.inactive {
  opacity: 0.3;
}
.level-chip.level-debug {
  background: rgba(148, 163, 184, 0.15);
  color: #94a3b8;
  border-color: rgba(148, 163, 184, 0.3);
}
.level-chip.level-info {
  background: rgba(96, 165, 250, 0.15);
  color: #60a5fa;
  border-color: rgba(96, 165, 250, 0.3);
}
.level-chip.level-warn {
  background: rgba(251, 191, 36, 0.15);
  color: #fbbf24;
  border-color: rgba(251, 191, 36, 0.3);
}
.level-chip.level-error {
  background: rgba(248, 113, 113, 0.15);
  color: #f87171;
  border-color: rgba(248, 113, 113, 0.3);
}

/* Log List */
.log-list {
  flex: 1;
  overflow-y: auto;
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  font-size: 11px;
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
  color: var(--text-dim, var(--text-muted));
}
.panel-error {
  padding: 12px;
  font-size: 12px;
  color: var(--error);
}

/* Log Entry */
.log-entry {
  padding: 4px 12px;
  border-bottom: 1px solid var(--border);
  cursor: default;
  transition: background 0.1s;
}
.log-entry:hover {
  background: var(--bg-secondary);
}
.log-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 22px;
}
.log-level-badge {
  font-size: 9px;
  font-weight: 700;
  padding: 1px 5px;
  border-radius: 3px;
  white-space: nowrap;
  flex-shrink: 0;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}
.log-level-badge.level-debug {
  background: rgba(148, 163, 184, 0.15);
  color: #94a3b8;
}
.log-level-badge.level-info {
  background: rgba(96, 165, 250, 0.15);
  color: #60a5fa;
}
.log-level-badge.level-warn {
  background: rgba(251, 191, 36, 0.15);
  color: #fbbf24;
}
.log-level-badge.level-error {
  background: rgba(248, 113, 113, 0.15);
  color: #f87171;
}
.log-msg {
  flex: 1;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.log-time {
  font-size: 10px;
  color: var(--text-dim, var(--text-muted));
  white-space: nowrap;
  flex-shrink: 0;
}

/* Inline attrs preview */
.log-attrs-hint {
  display: flex;
  gap: 8px;
  padding: 2px 0 2px 42px;
  flex-wrap: wrap;
}
.attr-inline {
  font-size: 10px;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 200px;
}
.attr-key-inline {
  color: var(--accent-light, var(--accent));
}

/* Expanded attrs */
.log-attrs {
  padding: 4px 0 4px 42px;
}
.log-attr {
  display: flex;
  gap: 8px;
  padding: 1px 0;
}
.attr-key {
  color: var(--accent-light, var(--accent));
  font-size: 10px;
  flex-shrink: 0;
}
.attr-val {
  color: var(--text-primary);
  font-size: 10px;
  word-break: break-all;
}

/* Expand transition */
.attrs-expand-enter-active,
.attrs-expand-leave-active {
  transition: max-height 0.15s ease, opacity 0.15s ease;
  overflow: hidden;
  max-height: 200px;
}
.attrs-expand-enter-from,
.attrs-expand-leave-to {
  max-height: 0;
  opacity: 0;
}
</style>
