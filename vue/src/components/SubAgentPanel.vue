<script setup lang="ts">
import { marked } from 'marked'
import { nextTick, onMounted, ref } from 'vue'

interface HistoryEntry {
  id: number
  code: string
  result: string
  response: string
  responseHtml?: string
  iteration: number
  block: number
  provider: string
  agent_type: string
  prompt_tokens?: number
  completion_tokens?: number
  created: string
}

const props = defineProps<{
  chatId: number
}>()

const entries = ref<HistoryEntry[]>([])
const loading = ref(false)
const hasMore = ref(false)
const listEl = ref<HTMLElement | null>(null)
let cursor = ''

const copied = ref(false)

async function renderMarkdown(text: string): Promise<string> {
  try {
    const resp = await fetch('/api/render-markdown', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ markdown: text }),
    })
    if (resp.ok) {
      const data = await resp.json()
      return data.html || ''
    }
  } catch { /* fall through */ }
  return marked.parse(text) as string
}

function scrollBottom() {
  nextTick(() => {
    if (listEl.value) {
      listEl.value.scrollTop = listEl.value.scrollHeight
    }
  })
}

async function loadEntries(older = false) {
  if (!props.chatId) return
  loading.value = true
  const prevHeight = listEl.value ? listEl.value.scrollHeight : 0
  try {
    let url = `/api/history/${props.chatId}/agents?limit=20`
    if (older && cursor) url += `&cursor=${encodeURIComponent(cursor)}`
    const resp = await fetch(url)
    if (!resp.ok) return
    const data = await resp.json()
    const list: HistoryEntry[] = (data.entries || []).reverse() // API returns newest-first; reverse for chronological
    // Pre-render response markdown
    for (const e of list) {
      if (e.response) {
        renderMarkdown(e.response).then(html => { e.responseHtml = html })
      }
    }
    if (older) {
      // Prepend older entries at the top, preserve scroll position
      entries.value = [...list, ...entries.value]
      nextTick(() => {
        if (listEl.value) {
          listEl.value.scrollTop = listEl.value.scrollHeight - prevHeight
        }
      })
    } else {
      entries.value = list
      scrollBottom()
    }
    cursor = data.cursor || ''
    hasMore.value = cursor !== ''
  } catch { /* ignore */ } finally {
    loading.value = false
  }
}

function copyAll() {
  const text = entries.value.map((e) => {
    const meta = [e.provider, e.agent_type].filter(Boolean).join(' · ')
    const tokens = (e.prompt_tokens || e.completion_tokens) ? ` | tokens: ↑${e.prompt_tokens ?? 0} ↓${e.completion_tokens ?? 0}` : ''
    const header = `### Step ${(e.iteration ?? 0) + 1}` + (meta ? ` (${meta}${tokens})` : '')
    let block = `${header}\n\`\`\`javascript\n${e.code}\n\`\`\``
    if (e.result) block += `\n**Result:** ${e.result}`
    if (e.response) block += `\n\n<details>\n<summary>AI Response</summary>\n\n${e.response}\n</details>`
    return block
  }).join('\n\n')
  navigator.clipboard.writeText(text)
  copied.value = true
  setTimeout(() => { copied.value = false }, 1500)
}

function formatTime(iso: string) {
  const d = new Date(iso)
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

onMounted(() => loadEntries())
</script>

<template>
  <div class="sub-agent-panel">
    <div class="panel-toolbar">
      <h2 class="panel-title">🤖 Sub-Agent History</h2>
      <div class="panel-actions">
        <button class="toolbar-btn" @click="copyAll" :disabled="!entries.length" :title="copied ? 'Copied!' : 'Copy all'">
          <svg v-if="!copied" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="20 6 9 17 4 12"/></svg>
          {{ copied ? 'Copied!' : 'Copy All' }}
        </button>
        <button class="toolbar-btn" @click="loadEntries(false)" title="Refresh">
          ↻ Refresh
        </button>
      </div>
    </div>

    <div v-if="loading && !entries.length" class="empty-state">Loading...</div>
    <div v-else-if="!entries.length" class="empty-state">
      <div class="empty-icon">🤖</div>
      <p>No sub-agent executions found in this chat.</p>
      <p class="empty-hint">Sub-agents are created when the AI delegates tasks using <code>agent.run()</code>.</p>
    </div>

    <div v-else class="entries-list" ref="listEl">
      <button v-if="hasMore" class="load-older-btn" @click="loadEntries(true)" :disabled="loading">
        {{ loading ? 'Loading...' : '↑ Load Older' }}
      </button>

      <div v-for="entry in entries" :key="entry.id" class="entry-card">
        <div class="entry-header">
          <span class="entry-id">#{{ entry.id }}</span>
          <span class="entry-provider">{{ entry.provider }}</span>
          <span class="entry-meta">iter {{ (entry.iteration ?? 0) + 1 }}, block {{ (entry.block ?? 0) + 1 }}</span>
          <span v-if="(entry.prompt_tokens ?? 0) > 0 || (entry.completion_tokens ?? 0) > 0" class="token-badge" title="Prompt / Completion tokens">
            <span class="tb-in">↑{{ (entry.prompt_tokens ?? 0) >= 1000 ? ((entry.prompt_tokens ?? 0)/1000).toFixed(1)+'k' : (entry.prompt_tokens ?? 0) }}</span>
            <span class="tb-out">↓{{ (entry.completion_tokens ?? 0) >= 1000 ? ((entry.completion_tokens ?? 0)/1000).toFixed(1)+'k' : (entry.completion_tokens ?? 0) }}</span>
          </span>
          <span class="entry-time">{{ formatTime(entry.created) }}</span>
        </div>
        <pre class="entry-code">{{ entry.code }}</pre>
        <div v-if="entry.result" class="entry-result">
          <span class="result-label">Result:</span>
          <span class="result-text">{{ entry.result }}</span>
        </div>
        <details v-if="entry.response" class="entry-response">
          <summary>AI Response</summary>
          <div class="response-content md-content" v-html="entry.responseHtml || marked.parse(entry.response)"></div>
        </details>
      </div>
    </div>
  </div>
</template>

<style scoped>
.sub-agent-panel {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
  overflow: hidden;
}
.panel-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 20px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
  background: var(--bg-secondary);
}
.panel-title {
  font-size: 14px;
  font-weight: 600;
  margin: 0;
  color: var(--text-primary);
}
.token-badge {
  display: inline-flex;
  gap: 4px;
  font-size: 10px;
  font-weight: 500;
  color: var(--text-muted);
}
.tb-in { color: #89b4fa; }
.tb-out { color: #a6e3a1; }
.panel-actions {
  display: flex;
  gap: 8px;
}
.toolbar-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-secondary);
  padding: 4px 10px;
  border-radius: 6px;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.15s;
}
.toolbar-btn:hover {
  color: var(--text-primary);
  border-color: var(--accent);
  background: var(--bg-tertiary, var(--bg-secondary));
}
.toolbar-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--text-muted);
  font-size: 13px;
  padding: 40px;
}
.empty-icon {
  font-size: 40px;
  opacity: 0.5;
}
.empty-hint {
  font-size: 12px;
  opacity: 0.7;
}
.empty-hint code {
  background: var(--bg-secondary);
  padding: 1px 4px;
  border-radius: 3px;
  font-size: 11px;
}
.entries-list {
  flex: 1;
  overflow-y: auto;
  padding: 12px 20px;
}
.load-older-btn {
  display: block;
  width: 100%;
  padding: 10px 0;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-secondary);
  font-size: 12px;
  cursor: pointer;
  transition: color 0.15s, border-color 0.15s;
  margin-bottom: 12px;
}
.load-older-btn:hover {
  color: var(--text-primary);
  border-color: var(--accent);
}
.load-older-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.entry-card {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 12px;
  overflow: hidden;
}
.entry-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  font-size: 12px;
  background: var(--bg-tertiary, var(--bg-secondary));
}
.entry-id {
  color: var(--accent);
  font-weight: 600;
  font-family: monospace;
}
.entry-provider {
  color: var(--text-secondary);
  font-weight: 500;
}
.entry-meta {
  color: var(--text-muted);
  font-size: 11px;
}
.entry-time {
  margin-left: auto;
  color: var(--text-muted);
  font-size: 11px;
  flex-shrink: 0;
}
.entry-code {
  margin: 0;
  padding: 10px 12px;
  font-size: 11px;
  line-height: 1.5;
  max-height: 250px;
  overflow: auto;
  color: var(--text-primary);
  white-space: pre;
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  background: var(--bg-primary);
}
.entry-result {
  padding: 8px 12px;
  font-size: 11px;
  color: var(--text-secondary);
  border-top: 1px solid var(--border);
  max-height: 200px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}
.result-label {
  color: #a6e3a1;
  font-weight: 600;
}
.result-text {
  margin-left: 4px;
}
.entry-response {
  border-top: 1px solid var(--border);
  padding: 0 12px;
}
.entry-response summary {
  cursor: pointer;
  color: var(--text-muted);
  font-size: 11px;
  padding: 8px 0;
  user-select: none;
}
.entry-response summary:hover {
  color: var(--text-primary);
}
.response-content {
  padding: 8px 0 12px;
  font-size: 12px;
  max-height: 400px;
  overflow: auto;
}
</style>
