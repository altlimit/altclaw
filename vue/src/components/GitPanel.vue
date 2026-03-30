<script setup lang="ts">
import { onMounted, ref } from 'vue'

interface DiffFile {
  path: string
  status: string
}

interface CommitEntry {
  hash: string
  message: string
  date: string
  files: DiffFile[]
}

interface DiffResult {
  path: string
  old_content: string
  new_content: string
  commit: string
}

const commits = ref<CommitEntry[]>([])
const loading = ref(false)
const expandedHash = ref<string | null>(null)
const diffData = ref<DiffResult | null>(null)
const diffLoading = ref(false)
const restoreStatus = ref('')
const hasMore = ref(false)

const emit = defineEmits<{
  (e: 'open-diff', data: DiffResult): void
  (e: 'file-restored', path: string): void
}>()

async function loadHistory(append = false) {
  loading.value = true
  try {
    const skip = append ? commits.value.length : 0
    const resp = await fetch(`/api/git-log?limit=50&skip=${skip}`)
    if (resp.ok) {
      const data = await resp.json()
      const newCommits = data.commits || []
      if (append) {
        commits.value = [...commits.value, ...newCommits]
      } else {
        commits.value = newCommits
      }
      hasMore.value = data.has_more || false
    }
  } catch { /* ignore */ } finally {
    loading.value = false
  }
}

function loadMore() {
  if (hasMore.value && !loading.value) loadHistory(true)
}

function toggleCommit(commit: CommitEntry) {
  if (expandedHash.value === commit.hash) {
    expandedHash.value = null
    diffData.value = null
    return
  }
  expandedHash.value = commit.hash
  diffData.value = null
}

async function viewDiff(commit: CommitEntry, file: DiffFile) {
  diffLoading.value = true
  try {
    const resp = await fetch(`/api/git-diff?commit=${commit.hash}&path=${encodeURIComponent(file.path)}`)
    if (resp.ok) {
      diffData.value = await resp.json()
      emit('open-diff', diffData.value!)
    }
  } catch { /* ignore */ } finally {
    diffLoading.value = false
  }
}

async function restoreFile(commit: CommitEntry, file: DiffFile) {
  if (!confirm(`Restore ${file.path} from ${commit.hash}?`)) return
  try {
    const resp = await fetch(`/api/git-restore?commit=${commit.hash}&path=${encodeURIComponent(file.path)}`, { method: 'POST' })
    if (resp.ok) {
      restoreStatus.value = `✓ Restored ${file.path}`
      setTimeout(() => restoreStatus.value = '', 3000)
      // Explicitly refresh the editor if this file is open
      emit('file-restored', file.path)
    }
  } catch { /* ignore */ }
}

async function restoreCommit(commit: CommitEntry) {
  if (!confirm(`Revert workspace to snapshot ${commit.hash}?`)) return
  try {
    const resp = await fetch(`/api/git-restore-commit?commit=${commit.hash}`, { method: 'POST' })
    if (resp.ok) {
      restoreStatus.value = `✓ Restored workspace to ${commit.hash}`
      setTimeout(() => restoreStatus.value = '', 3000)
      // Refresh all files from the restored commit
      if (commit.files) {
        for (const f of commit.files) {
          emit('file-restored', f.path)
        }
      }
    }
  } catch { /* ignore */ }
}

function formatTime(iso: string) {
  const d = new Date(iso)
  const now = new Date()
  const isToday = d.toDateString() === now.toDateString()
  if (isToday) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function statusIcon(status: string) {
  switch (status) {
    case 'added': return 'A'
    case 'modified': return 'M'
    case 'deleted': return 'D'
    default: return '?'
  }
}

function statusClass(status: string) {
  return 'status-' + status
}

onMounted(loadHistory)
defineExpose({ refresh: loadHistory })
</script>

<template>
  <div class="git-panel">
    <div class="panel-header">
      <span>History</span>
      <button class="refresh-btn" @click="loadHistory()" title="Refresh">↻</button>
    </div>

    <div v-if="restoreStatus" class="restore-toast">{{ restoreStatus }}</div>

    <div v-if="loading && !commits.length" class="empty">Loading...</div>
    <div v-else-if="!commits.length" class="empty">No snapshots yet</div>
    <div v-else class="commit-list">
      <div
        v-for="commit in commits"
        :key="commit.hash"
        class="commit-item"
        :class="{ expanded: expandedHash === commit.hash }"
      >
        <div class="commit-header" @click="toggleCommit(commit)">
          <span class="commit-hash">{{ commit.hash }}</span>
          <span class="commit-msg">{{ commit.message }}</span>
          <span class="commit-time">{{ formatTime(commit.date) }}</span>
          <button
            class="restore-btn"
            @click.stop="restoreCommit(commit)"
            title="Revert entire commit"
          >↺</button>
        </div>
        <div v-if="expandedHash === commit.hash && commit.files" class="commit-files">
          <div
            v-for="file in commit.files"
            :key="file.path"
            class="file-entry"
          >
            <span class="file-status" :class="statusClass(file.status)">{{ statusIcon(file.status) }}</span>
            <span class="file-path" @click.stop="viewDiff(commit, file)" :title="'View diff: ' + file.path">{{ file.path }}</span>
            <button
              v-if="file.status !== 'deleted'"
              class="restore-btn"
              @click.stop="restoreFile(commit, file)"
              title="Restore this file"
            >↺</button>
          </div>
        </div>
      </div>
      <button v-if="hasMore" class="view-more-btn" @click="loadMore" :disabled="loading">
        {{ loading ? 'Loading…' : 'View More' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.git-panel {
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
.restore-toast {
  padding: 6px 12px;
  background: rgba(166, 227, 161, 0.15);
  color: #a6e3a1;
  font-size: 12px;
  border-bottom: 1px solid var(--border);
  text-align: center;
}
.empty {
  padding: 24px 12px;
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
}
.commit-list {
  flex: 1;
  overflow-y: auto;
  padding: 2px 0;
}
.commit-item {
  border-bottom: 1px solid var(--border);
}
.commit-item.expanded {
  background: var(--bg-secondary);
}
.commit-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 12px;
  font-size: 12px;
  cursor: pointer;
  transition: background 0.15s;
}
.commit-header:hover {
  background: var(--bg-secondary);
}
.commit-hash {
  color: var(--accent);
  font-family: monospace;
  font-weight: 600;
  font-size: 11px;
  flex-shrink: 0;
}
.commit-msg {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
  font-size: 12px;
}
.commit-time {
  color: var(--text-muted);
  font-size: 10px;
  flex-shrink: 0;
}
.commit-files {
  padding: 2px 12px 8px 28px;
}
.file-entry {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 0;
  font-size: 12px;
}
.file-status {
  font-family: monospace;
  font-weight: 700;
  font-size: 11px;
  width: 14px;
  text-align: center;
  flex-shrink: 0;
}
.status-added { color: #a6e3a1; }
.status-modified { color: #f9e2af; }
.status-deleted { color: #f38ba8; }
.file-path {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
  color: var(--text-primary);
  transition: color 0.15s;
}
.file-path:hover {
  color: var(--accent);
  text-decoration: underline;
}
.restore-btn {
  background: none;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 13px;
  padding: 1px 4px;
  border-radius: 3px;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s, background 0.15s;
  flex-shrink: 0;
}
.file-entry:hover .restore-btn,
.commit-header:hover .restore-btn {
  opacity: 1;
}
.restore-btn:hover {
  color: var(--accent);
  background: var(--bg-tertiary, var(--bg-secondary));
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
  transition: background 0.15s;
}
.view-more-btn:hover {
  background: var(--bg-secondary);
}
</style>
