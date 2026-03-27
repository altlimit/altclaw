<script setup lang="ts">
import { onMounted, ref } from 'vue'

interface CronJob {
  id: string
  chat_id: number
  schedule: string
  instructions: string
  one_shot: boolean
  script: boolean
  created_at: string
  next_run: string
}

const jobs = ref<CronJob[]>([])
const loading = ref(true)
const error = ref('')
const expandedId = ref<string | null>(null)

async function loadJobs() {
  loading.value = true
  error.value = ''
  try {
    const resp = await fetch('/api/cron-jobs')
    if (!resp.ok) throw new Error(await resp.text())
    jobs.value = await resp.json()
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function deleteJob(id: string) {
  try {
    const resp = await fetch('/api/cron-jobs/' + id, { method: 'DELETE' })
    if (!resp.ok) throw new Error(await resp.text())
    jobs.value = jobs.value.filter(j => j.id !== id)
    if (expandedId.value === id) expandedId.value = null
  } catch (e: any) {
    error.value = 'Delete failed: ' + e.message
  }
}

function toggleExpand(id: string) {
  expandedId.value = expandedId.value === id ? null : id
}

function jobType(job: CronJob): string {
  if (job.script) return 'Script'
  if (job.one_shot) return 'One-shot'
  return 'Recurring'
}

function formatDate(iso: string): string {
  if (!iso) return '—'
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

onMounted(loadJobs)
</script>

<template>
  <div class="container">
    <div class="page-header">
      <h2 class="page-title">Cron Jobs</h2>
      <button class="btn btn-ghost btn-sm" @click="loadJobs" :disabled="loading" title="Refresh">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>
        </svg>
        Refresh
      </button>
    </div>

    <div v-if="loading" class="loading">Loading jobs…</div>
    <div v-else-if="error" class="status-msg error">{{ error }}</div>
    <div v-else-if="jobs.length === 0" class="empty-state">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="empty-icon">
        <circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>
      </svg>
      <p>No scheduled jobs</p>
      <span class="empty-hint">Jobs created by the AI via <code>cron.add()</code> will appear here.</span>
    </div>
    <div v-else class="job-list">
      <div
        v-for="job in jobs"
        :key="job.id"
        class="job-card"
        :class="{ expanded: expandedId === job.id }"
      >
        <div class="job-header" @click="toggleExpand(job.id)">
          <div class="job-left">
            <svg class="job-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>
            <span class="job-schedule">{{ job.schedule }}</span>
            <span class="job-type" :class="job.script ? 'script' : job.one_shot ? 'oneshot' : 'recurring'">{{ jobType(job) }}</span>
          </div>
          <div class="job-right">
            <span v-if="job.next_run" class="job-next">Next: {{ formatDate(job.next_run) }}</span>
            <button class="btn-icon" title="Delete job" @click.stop="deleteJob(job.id)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
            </button>
          </div>
        </div>
        <div v-show="expandedId === job.id" class="job-body">
          <div class="job-meta">
            <span>ID: <code>{{ job.id }}</code></span>
            <span v-if="job.chat_id">Chat: #{{ job.chat_id }}</span>
            <span>Created: {{ formatDate(job.created_at) }}</span>
          </div>
          <div class="job-instructions-label">{{ job.script ? 'Script' : 'Instructions' }}</div>
          <pre class="job-instructions">{{ job.instructions }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}
.page-title {
  font-size: 20px;
  font-weight: 700;
  margin: 0;
}
.loading {
  text-align: center;
  color: var(--text-muted);
  padding: 40px 0;
  font-size: 14px;
}

/* Empty state */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 60px 20px;
  color: var(--text-muted);
}
.empty-icon {
  opacity: 0.3;
  margin-bottom: 16px;
}
.empty-state p {
  font-size: 16px;
  font-weight: 500;
  margin: 0 0 8px;
  color: var(--text-secondary);
}
.empty-hint {
  font-size: 13px;
}
.empty-hint code {
  background: var(--bg-tertiary);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
}

/* Job list */
.job-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.job-card {
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  transition: border-color 0.15s;
}
.job-card:hover,
.job-card.expanded {
  border-color: var(--border-hover);
}
.job-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 16px;
  cursor: pointer;
  background: var(--bg-tertiary);
  user-select: none;
  transition: background 0.1s;
}
.job-header:hover {
  background: rgba(99, 102, 241, 0.04);
}
.job-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.job-chevron {
  flex-shrink: 0;
  color: var(--text-muted);
  transition: transform 0.2s;
}
.job-card.expanded .job-chevron {
  transform: rotate(90deg);
}
.job-schedule {
  font-size: 13px;
  font-weight: 600;
  font-family: monospace;
  color: var(--accent-light);
  background: rgba(99, 102, 241, 0.1);
  padding: 2px 8px;
  border-radius: 4px;
  white-space: nowrap;
}
.job-type {
  font-size: 11px;
  font-weight: 500;
  padding: 2px 6px;
  border-radius: 4px;
  white-space: nowrap;
}
.job-type.recurring {
  background: rgba(34, 197, 94, 0.1);
  color: var(--success);
}
.job-type.oneshot {
  background: rgba(245, 158, 11, 0.1);
  color: #f59e0b;
}
.job-type.script {
  background: rgba(99, 102, 241, 0.1);
  color: var(--accent-light);
}
.job-right {
  display: flex;
  align-items: center;
  gap: 12px;
}
.job-next {
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
}
.btn-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  background: transparent;
  color: var(--text-dim);
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.15s;
  flex-shrink: 0;
}
.btn-icon:hover {
  background: rgba(239, 68, 68, 0.1);
  color: var(--error);
}

/* Job body */
.job-body {
  padding: 12px 20px 16px;
  border-top: 1px solid var(--border);
  background: var(--bg-secondary);
}
.job-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 12px;
}
.job-meta code {
  background: var(--bg-tertiary);
  padding: 1px 5px;
  border-radius: 3px;
  font-size: 11px;
}
.job-instructions-label {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted);
  margin-bottom: 6px;
  font-weight: 600;
}
.job-instructions {
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 12px;
  font-size: 13px;
  font-family: monospace;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 300px;
  overflow-y: auto;
  margin: 0;
  color: var(--text-primary);
}

.status-msg.error {
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  color: var(--error);
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 13px;
}

/* Mobile */
@media (max-width: 768px) {
  .job-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
  .job-right {
    width: 100%;
    justify-content: space-between;
  }
}
</style>
