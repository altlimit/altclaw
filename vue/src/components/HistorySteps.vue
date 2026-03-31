<script setup lang="ts">
import { marked } from 'marked'
import { ref } from 'vue'

export interface HistoryEntry {
  id: number
  code: string
  result: string
  response: string
  responseHtml?: string
  iteration: number
  block: number
  provider: string
  agent_type: string
  created: string
}

const props = defineProps<{
  entries: HistoryEntry[]
  promptTokens?: number
  completionTokens?: number
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

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

// Pre-render response markdown for entries that have it
for (const e of props.entries) {
  if (e.response && !e.responseHtml) {
    renderMarkdown(e.response).then(html => { e.responseHtml = html })
  }
}

const copied = ref(false)
function copyAll() {
  const text = props.entries.map((e) => {
    const meta = [e.provider, e.agent_type].filter(Boolean).join(' · ')
    const header = `### Step ${(e.iteration ?? 0) + 1}` + (meta ? ` (${meta})` : '')
    let block = `${header}\n\`\`\`javascript\n${e.code}\n\`\`\``
    if (e.result) block += `\n**Result:** ${e.result}`
    if (e.response) block += `\n\n<details>\n<summary>AI Response</summary>\n\n${e.response}\n</details>`
    return block
  }).join('\n\n')
  navigator.clipboard.writeText(text)
  copied.value = true
  setTimeout(() => { copied.value = false }, 1500)
}
</script>

<template>
  <div class="history-steps">
    <div class="history-steps-header">
      <span>Execution Steps</span>
      <span v-if="(promptTokens ?? 0) > 0 || (completionTokens ?? 0) > 0" class="token-badge" title="Prompt / Completion tokens">
        <span class="tb-in">↑{{ promptTokens && promptTokens >= 1000 ? (promptTokens/1000).toFixed(1)+'k' : (promptTokens ?? 0) }}</span>
        <span class="tb-out">↓{{ completionTokens && completionTokens >= 1000 ? (completionTokens/1000).toFixed(1)+'k' : (completionTokens ?? 0) }}</span>
      </span>
      <div class="history-actions">
        <button class="copy-steps-btn" @click.stop="copyAll" :title="copied ? 'Copied!' : 'Copy all steps'">
          <svg v-if="!copied" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></svg>
          <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="20 6 9 17 4 12"/></svg>
          {{ copied ? 'Copied!' : 'Copy all' }}
        </button>
        <button class="close-steps-btn" @click.stop="emit('close')" title="Close">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
    </div>
    <div v-for="entry in entries" :key="entry.id" class="history-entry">
      <div class="history-entry-header">
        Step {{ (entry.iteration ?? 0) + 1 }}
        <span v-if="entry.provider || entry.agent_type" class="history-meta">
          {{ [entry.provider, entry.agent_type].filter(Boolean).join(' · ') }}
        </span>
      </div>
      <pre class="file-preview">{{ entry.code }}</pre>
      <div v-if="entry.result" class="history-result">
        <span class="result-label">Result:</span> {{ entry.result }}
      </div>
      <details v-if="entry.response" class="history-response">
        <summary>AI Response</summary>
        <div class="history-response-content md-content" v-html="entry.responseHtml || marked.parse(entry.response)"></div>
      </details>
    </div>
    <div v-if="!entries.length" class="history-empty">No execution steps found.</div>
  </div>
</template>

<style scoped>
.history-steps {
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-top: 8px;
  background: var(--bg-secondary);
  font-size: 12px;
}
.history-steps-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  font-weight: 600;
  font-size: 12px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border);
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
.history-actions {
  margin-left: auto;
  display: flex;
  gap: 6px;
}
.copy-steps-btn, .close-steps-btn {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 11px;
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 2px 6px;
  border-radius: 4px;
  transition: color 0.15s, background 0.15s;
}
.copy-steps-btn:hover, .close-steps-btn:hover {
  color: var(--text-primary);
  background: var(--bg-primary);
}
.history-entry {
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
}
.history-entry:last-child {
  border-bottom: none;
}
.history-entry-header {
  font-weight: 600;
  font-size: 11px;
  color: var(--accent);
  margin-bottom: 4px;
}
.history-meta {
  font-weight: 400;
  color: var(--text-muted);
  font-size: 10px;
  margin-left: 6px;
}
.file-preview {
  margin: 4px 0;
  padding: 8px;
  background: var(--bg-tertiary, var(--bg-primary));
  border: 1px solid var(--border);
  border-radius: 4px;
  font-size: 11px;
  line-height: 1.4;
  max-height: 200px;
  overflow: auto;
  color: var(--text-primary);
  white-space: pre;
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
}
.history-result {
  margin-top: 4px;
  padding: 6px 8px;
  background: var(--bg-tertiary, var(--bg-primary));
  border-radius: 4px;
  font-size: 11px;
  color: var(--text-secondary);
  max-height: 150px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}
.result-label {
  color: #a6e3a1;
  font-weight: 600;
}
.history-response {
  margin-top: 4px;
}
.history-response summary {
  cursor: pointer;
  color: var(--text-muted);
  font-size: 11px;
  user-select: none;
}
.history-response summary:hover {
  color: var(--text-primary);
}
.history-response-content {
  padding: 8px;
  margin-top: 4px;
  background: var(--bg-tertiary, var(--bg-primary));
  border-radius: 4px;
  font-size: 12px;
  max-height: 300px;
  overflow: auto;
}
.history-empty {
  padding: 16px;
  text-align: center;
  color: var(--text-muted);
  font-size: 12px;
}
</style>
