<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useEditorStore } from '@/stores/editor'
import { useEventStore } from '@/stores/events'

const editorStore = useEditorStore()
const eventStore = useEventStore()

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
    // Close the tab if it's open
    editorStore.closeFile(`memory://cron-${id}`)
  } catch (e: any) {
    error.value = 'Delete failed: ' + e.message
  }
}

function openJob(job: CronJob) {
  const label = `Cron: ${job.schedule}`
  const path = `memory://cron-${job.id}`
  const lang = job.script ? 'javascript' : 'markdown'
  editorStore.openVirtualTab(path, label, job.instructions, lang)
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

function onCronEvent(evt: any) {
  if (evt.action === 'deleted' && evt.id) {
    jobs.value = jobs.value.filter(j => j.id !== evt.id)
    editorStore.closeFile(`memory://cron-${evt.id}`)
    return
  }
  loadJobs()
}

onMounted(() => {
  loadJobs()
  eventStore.on('cron_updated', onCronEvent)
})

onUnmounted(() => {
  eventStore.off('cron_updated', onCronEvent)
})
</script>

<template>
  <div class="cron-panel">
    <div class="panel-header">
      <span class="panel-title">Cron Jobs</span>
      <button class="refresh-btn" @click="loadJobs" :disabled="loading" title="Refresh">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>
        </svg>
      </button>
    </div>

    <div class="panel-list">
      <div v-if="loading" class="panel-empty">Loading…</div>
      <div v-else-if="error" class="panel-error">{{ error }}</div>
      <div v-else-if="jobs.length === 0" class="panel-empty">
        <span>No scheduled jobs</span>
      </div>
      <div
        v-for="job in jobs"
        :key="job.id"
        class="job-item"
        @click="openJob(job)"
      >
        <div class="job-top">
          <span class="job-schedule">{{ job.schedule }}</span>
          <button class="job-delete" @click.stop="deleteJob(job.id)" title="Delete">✕</button>
        </div>
        <div class="job-meta">
          <span class="job-type" :class="job.script ? 'script' : job.one_shot ? 'oneshot' : 'recurring'">{{ jobType(job) }}</span>
          <span v-if="job.next_run" class="job-next">{{ formatDate(job.next_run) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cron-panel {
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

/* Job item */
.job-item {
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.15s;
  border-bottom: 1px solid var(--border);
}
.job-item:last-child {
  border-bottom: none;
}
.job-item:hover {
  background: var(--bg-secondary);
}
.job-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 4px;
}
.job-schedule {
  font-size: 12px;
  font-weight: 600;
  font-family: monospace;
  color: var(--accent-light);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.job-delete {
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
.job-item:hover .job-delete {
  opacity: 1;
}
.job-delete:hover {
  color: var(--error);
}
.job-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}
.job-type {
  font-size: 10px;
  font-weight: 500;
  padding: 1px 5px;
  border-radius: 3px;
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
.job-next {
  font-size: 10px;
  color: var(--text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
