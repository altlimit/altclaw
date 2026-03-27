<script setup lang="ts">
import { useEditorStore } from '@/stores/editor'
import { useEventStore } from '@/stores/events'
import { onMounted, ref } from 'vue'

interface ProviderSummary {
  id: number
  name: string
  provider: string
  model: string
  in_memory?: boolean
}

const editorStore = useEditorStore()
const eventStore = useEventStore()
const providers = ref<ProviderSummary[]>([])
let selfTriggered = false

const providerLabels: Record<string, string> = {
  openai: 'OpenAI', gemini: 'Gemini', anthropic: 'Anthropic', ollama: 'Ollama',
  grok: 'Grok', deepseek: 'DeepSeek', mistral: 'Mistral', openrouter: 'OpenRouter',
  perplexity: 'Perplexity', hugging_face: 'HF', minimax: 'MiniMax', glm: 'GLM',
}

async function loadProviders() {
  try {
    const resp = await fetch('/api/providers')
    if (resp.ok) providers.value = await resp.json()
  } catch { /* ignore */ }
}

function openProvider(p: ProviderSummary) {
  editorStore.openSpecialTab('provider', p.name || 'default', p.id)
}

function addProvider() {
  // Open a new provider tab with id 0
  editorStore.openSpecialTab('provider', 'New Provider', 0)
}

onMounted(() => {
  loadProviders()
  eventStore.on('provider_updated', () => {
    if (selfTriggered) { selfTriggered = false; return }
    loadProviders()
  })
})

defineExpose({ reload: loadProviders, setSelfTriggered: () => { selfTriggered = true } })
</script>

<template>
  <div class="providers-sidebar">
    <div class="providers-header">
      <span class="providers-title">Providers</span>
      <button class="new-btn" @click="addProvider" title="Add Provider">+ New</button>
    </div>
    <div class="providers-list">
      <div
        v-for="p in providers"
        :key="p.id"
        class="provider-item"
        :class="{ active: editorStore.activeFilePath === `special://provider-${p.id}` }"
        @click="openProvider(p)"
      >
        <div class="provider-item-top">
          <span class="provider-name">{{ p.name || 'default' }}</span>
          <span v-if="p.in_memory" class="profile-badge">profile</span>
        </div>
        <div class="provider-item-meta">
          <span>{{ providerLabels[p.provider] || p.provider }}</span>
          <span v-if="p.model" class="model-tag">{{ p.model }}</span>
        </div>
      </div>
      <div v-if="providers.length === 0" class="providers-empty">No providers configured</div>
    </div>
  </div>
</template>

<style scoped>
.providers-sidebar {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.providers-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px 8px;
  border-bottom: 1px solid var(--border);
}
.providers-title {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
}
.new-btn {
  font-size: 12px;
  padding: 3px 10px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  transition: all 0.15s;
}
.new-btn:hover {
  background: rgba(99, 102, 241, 0.1);
  border-color: var(--accent);
  color: var(--accent-light);
}
.providers-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}
.provider-item {
  padding: 8px 16px;
  cursor: pointer;
  border-left: 2px solid transparent;
  transition: all 0.1s;
}
.provider-item:hover {
  background: var(--bg-secondary);
}
.provider-item.active {
  background: rgba(99, 102, 241, 0.08);
  border-left-color: var(--accent);
}
.provider-item-top {
  display: flex;
  align-items: center;
  gap: 6px;
}
.provider-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
.profile-badge {
  font-size: 10px;
  font-weight: 600;
  color: #10b981;
  background: rgba(16, 185, 129, 0.1);
  border: 1px solid rgba(16, 185, 129, 0.25);
  padding: 0 5px;
  border-radius: 3px;
  line-height: 1.6;
}
.provider-item-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 2px;
  font-size: 11px;
  color: var(--text-muted);
}
.model-tag {
  font-family: monospace;
  font-size: 10px;
  background: var(--bg-secondary);
  padding: 1px 4px;
  border-radius: 3px;
  color: var(--text-secondary);
}
.providers-empty {
  text-align: center;
  padding: 24px 16px;
  font-size: 13px;
  color: var(--text-dim);
}
</style>
