<script setup lang="ts">
import { useEventStore } from '@/stores/events'
import { useToast } from '@/composables/useToast'
import { onMounted, ref, watch, computed } from 'vue'

interface DocInfo {
  name: string
  description: string
  example: string
  filename: string
}

const props = defineProps<{ providerId: number }>()
const emit = defineEmits<{ (e: 'saved', id: number): void; (e: 'deleted'): void }>()
const toast = useToast()
const eventStore = useEventStore()

const loading = ref(true)
const saving = ref(false)
const isNew = computed(() => props.providerId === 0)

// Form state
const name = ref('')
const providerType = ref('openai')
const model = ref('')
const apiKey = ref('')
const baseUrl = ref('')
const host = ref('')
const description = ref('')
const dockerImage = ref('')
const rateLimit = ref(0)
const dailyPromptCap = ref(0)
const dailyCompletionCap = ref(0)
const docs = ref<string[]>([])
const inMemory = ref(false)
const dbId = ref(0)

// Model browser
const models = ref<{ id: string; name: string; description?: string }[]>([])
const modelsLoading = ref(false)
const modelsError = ref('')
const showModels = ref(false)
const modelFilter = ref('')

// Docs
const availableDocs = ref<DocInfo[]>([])
const docSearch = ref('')
const showDocDropdown = ref(false)

async function loadProvider() {
  loading.value = true
  try {
    const docsResp = await fetch('/api/docs')
    if (docsResp.ok) availableDocs.value = await docsResp.json()

    if (props.providerId > 0) {
      const resp = await fetch(`/api/provider/${props.providerId}`)
      if (!resp.ok) throw new Error('Failed to load provider')
      const p = await resp.json()
      name.value = p.name || ''
      providerType.value = p.provider || 'openai'
      model.value = p.model || ''
      apiKey.value = p.api_key || ''
      baseUrl.value = p.base_url || ''
      host.value = p.host || ''
      description.value = p.description || ''
      dockerImage.value = p.docker_image || ''
      rateLimit.value = p.rate_limit || 0
      dailyPromptCap.value = p.daily_prompt_cap || 0
      dailyCompletionCap.value = p.daily_completion_cap || 0
      docs.value = p.docs || []
      inMemory.value = p.in_memory || false
      dbId.value = p.id || 0
    }
  } catch (e: any) {
    toast.error(e.message)
  } finally {
    loading.value = false
  }
}

async function saveProvider() {
  saving.value = true
  try {
    const body: any = {
      name: name.value.trim() || 'default',
      provider: providerType.value,
      model: model.value,
      docs: docs.value,
      description: description.value,
      docker_image: dockerImage.value,
      rate_limit: rateLimit.value || 0,
      daily_prompt_cap: dailyPromptCap.value || 0,
      daily_completion_cap: dailyCompletionCap.value || 0,
    }
    if (dbId.value) body.id = dbId.value
    if (providerType.value === 'ollama') {
      body.host = host.value
    } else {
      body.api_key = apiKey.value
      body.base_url = baseUrl.value
    }
    const resp = await fetch('/api/provider', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!resp.ok) throw new Error(await parseError(resp))
    const data = await resp.json()
    dbId.value = data.id
    toast.success(isNew.value ? 'Provider created!' : 'Provider saved!')
    emit('saved', data.id)
  } catch (e: any) {
    toast.error('Save failed: ' + e.message)
  } finally {
    saving.value = false
  }
}

async function deleteProvider() {
  if (!dbId.value) { emit('deleted'); return }
  try {
    const resp = await fetch(`/api/provider/${dbId.value}`, { method: 'DELETE' })
    if (!resp.ok) throw new Error(await parseError(resp))
    toast.success('Provider deleted')
    emit('deleted')
  } catch (e: any) {
    toast.error('Delete failed: ' + e.message)
  }
}

async function fetchModels() {
  modelsLoading.value = true
  modelsError.value = ''
  models.value = []
  try {
    const params = new URLSearchParams({ provider: providerType.value })
    if (apiKey.value) params.set('api_key', apiKey.value)
    if (baseUrl.value) params.set('base_url', baseUrl.value)
    if (host.value) params.set('host', host.value)
    const resp = await fetch('/api/models?' + params.toString())
    if (!resp.ok) throw new Error(await resp.text() || resp.statusText)
    const data = await resp.json()
    models.value = data.models || []
    showModels.value = true
    modelFilter.value = ''
  } catch (e: any) {
    modelsError.value = e.message
  } finally {
    modelsLoading.value = false
  }
}

function selectModel(m: { id: string }) {
  model.value = m.id
  showModels.value = false
}

const filteredModels = computed(() => {
  if (!modelFilter.value) return models.value
  const q = modelFilter.value.toLowerCase()
  return models.value.filter(m => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q))
})

const filteredDocs = computed(() => {
  const existing = new Set(docs.value)
  const q = docSearch.value.toLowerCase()
  return availableDocs.value.filter(s =>
    !existing.has(s.name) && (s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q))
  )
})

function addDoc(docName: string) {
  if (!docs.value.includes(docName)) docs.value.push(docName)
  docSearch.value = ''
  showDocDropdown.value = false
}

function removeDoc(docName: string) {
  docs.value = docs.value.filter(s => s !== docName)
}

async function parseError(resp: Response): Promise<string> {
  const text = await resp.text()
  try { const json = JSON.parse(text); return json.error || json.message || text } catch { return text || resp.statusText }
}

watch(() => providerType.value, () => {
  models.value = []
  showModels.value = false
})

onMounted(() => {
  loadProvider()
  document.addEventListener('click', (e) => {
    if (!(e.target as HTMLElement)?.closest('.model-combobox')) showModels.value = false
    if (!(e.target as HTMLElement)?.closest('.doc-picker')) showDocDropdown.value = false
  })
})
</script>

<template>
  <div class="container" v-if="!loading">
    <div class="page-header">
      <h2 class="page-title">{{ isNew ? 'New Provider' : (name || 'default') }}</h2>
      <div class="page-actions">
        <button v-if="!inMemory && dbId" type="button" class="btn btn-ghost btn-danger" @click="deleteProvider">Delete</button>
      </div>
    </div>

    <div v-if="inMemory" class="profile-notice">
      This provider is managed by a hub profile. Changes are read-only.
    </div>

    <form @submit.prevent="saveProvider">
      <!-- Row 1: Name + Provider Type -->
      <div class="section" style="position: relative; z-index: 60;">
        <div class="row">
          <div class="field">
            <label>Name</label>
            <input v-model="name" placeholder="default" :disabled="inMemory" />
          </div>
          <div class="field">
            <label>Provider</label>
            <select v-model="providerType" :disabled="inMemory">
              <optgroup label="Core">
                <option value="openai">OpenAI</option>
                <option value="gemini">Gemini</option>
                <option value="anthropic">Anthropic</option>
                <option value="ollama">Ollama</option>
              </optgroup>
              <optgroup label="OpenAI Compatible">
                <option value="grok">Grok (xAI)</option>
                <option value="deepseek">DeepSeek</option>
                <option value="mistral">Mistral</option>
                <option value="openrouter">OpenRouter</option>
                <option value="perplexity">Perplexity</option>
                <option value="hugging_face">Hugging Face</option>
                <option value="minimax">MiniMax</option>
                <option value="glm">GLM (Zhipu)</option>
              </optgroup>
            </select>
          </div>
        </div>
      </div>

      <!-- Row 2: Credentials -->
      <div class="section" style="position: relative; z-index: 50;">
        <div class="section-title">Credentials</div>
        <div class="row">
          <div class="field" v-if="providerType !== 'ollama'">
            <label>API Key</label>
            <input v-model="apiKey" type="password" placeholder="sk-..." :disabled="inMemory" />
          </div>
          <div class="field" v-if="providerType === 'ollama'">
            <label>Host</label>
            <input v-model="host" placeholder="http://localhost:11434" :disabled="inMemory" />
          </div>
          <div class="field" v-if="providerType !== 'ollama'" v-show="!inMemory">
            <label>Base URL</label>
            <input v-model="baseUrl" placeholder="(default for provider)" />
            <div class="hint">Custom API endpoint. Leave empty for default.</div>
          </div>
        </div>
      </div>

      <!-- Row 3: Model -->
      <div class="section" style="position: relative; z-index: 40;">
        <div class="section-title">Model</div>
        <div class="field">
          <div class="model-combobox">
            <div class="model-input-row">
              <input
                v-model="model"
                placeholder="e.g. gpt-4o, gemini-2.5-flash"
                @focus="models.length && (showModels = true)"
              />
              <button
                type="button"
                class="btn btn-ghost btn-sm fetch-btn"
                :disabled="modelsLoading"
                @click="fetchModels"
              >
                <span v-if="modelsLoading" class="spinner"></span>
                {{ modelsLoading ? 'Loading...' : 'Fetch Models' }}
              </button>
            </div>
            <div v-if="modelsError" class="model-error">{{ modelsError }}</div>
            <div v-if="showModels && models.length" class="model-dropdown">
              <input
                v-model="modelFilter"
                class="model-search"
                placeholder="Filter models..."
                @click.stop
              />
              <div class="model-list">
                <button
                  v-for="m in filteredModels"
                  :key="m.id"
                  type="button"
                  class="model-option"
                  :class="{ selected: model === m.id }"
                  @click="selectModel(m)"
                >
                  <span class="model-id">{{ m.id }}</span>
                  <span v-if="m.description" class="model-desc">{{ m.description }}</span>
                </button>
                <div v-if="filteredModels.length === 0" class="model-empty">No models match filter</div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Row 4: Description + Docs -->
      <div class="section" style="position: relative; z-index: 30;">
        <div class="row">
          <div class="field">
            <label>Description</label>
            <input v-model="description" placeholder="Best for complex code tasks" />
            <div class="hint">Provider's strengths (shown to AI for delegation).</div>
          </div>
          <div class="field">
            <label>Docs</label>
            <div class="doc-picker">
              <div class="doc-tags">
                <span v-for="s in docs" :key="s" class="doc-tag">
                  {{ s }}
                  <button type="button" class="doc-tag-remove" @click="removeDoc(s)">&times;</button>
                </span>
                <input
                  v-model="docSearch"
                  class="doc-input"
                  placeholder="Search docs..."
                  @focus="showDocDropdown = true"
                  @click.stop
                />
              </div>
              <div v-if="showDocDropdown && filteredDocs.length" class="doc-dropdown">
                <button
                  v-for="s in filteredDocs"
                  :key="s.name"
                  type="button"
                  class="doc-option"
                  @click.stop="addDoc(s.name)"
                >
                  <span class="doc-option-name">{{ s.name }}</span>
                  <span class="doc-option-desc">{{ s.description }}</span>
                </button>
              </div>
            </div>
            <div class="hint">Pre-loaded docs. Content auto-injected into system prompt.</div>
          </div>
        </div>
      </div>

      <!-- Docker Image Override -->
      <div class="section" style="position: relative; z-index: 20;">
        <div class="field">
          <label>Docker Image Override</label>
          <input v-model="dockerImage" placeholder="(use global default)" />
          <div class="hint">Override Docker image for this provider's sub-agents.</div>
        </div>
      </div>

      <!-- Limits -->
      <div class="section" style="position: relative; z-index: 10;">
        <div class="section-title">Limits</div>
        <div class="limits-row">
          <div class="limit-field">
            <label>Rate Limit <span class="unit">req/min</span></label>
            <input v-model.number="rateLimit" type="number" min="0" placeholder="default" />
          </div>
          <div class="limit-field">
            <label>Input Cap <span class="unit">tokens/day</span></label>
            <input v-model.number="dailyPromptCap" type="number" min="0" placeholder="default" />
          </div>
          <div class="limit-field">
            <label>Output Cap <span class="unit">tokens/day</span></label>
            <input v-model.number="dailyCompletionCap" type="number" min="0" placeholder="default" />
          </div>
        </div>
      </div>

      <!-- Save -->
      <div class="btn-row" v-if="!inMemory">
        <button type="submit" class="btn btn-primary" :disabled="saving">
          {{ saving ? 'Saving...' : (isNew ? 'Create Provider' : 'Save Provider') }}
        </button>
      </div>
    </form>
  </div>
  <div v-else class="loading-state">Loading...</div>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.page-title {
  font-size: 20px;
  font-weight: 700;
}

.profile-notice {
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 13px;
  background: rgba(16, 185, 129, 0.1);
  border: 1px solid rgba(16, 185, 129, 0.3);
  color: #10b981;
  margin-bottom: 16px;
}

.btn-danger { color: var(--error) !important; }
.btn-danger:hover { background: rgba(239, 68, 68, 0.1) !important; }

.btn-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 16px;
}

.limits-row {
  display: flex;
  gap: 12px;
  margin-bottom: 4px;
}
.limit-field {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.limit-field label {
  font-size: 12px;
  color: var(--text-muted);
  font-weight: 500;
}
.limit-field input {
  padding: 5px 8px;
  font-size: 12px;
}
.unit {
  font-weight: 400;
  opacity: 0.65;
  font-size: 11px;
}

/* Model combobox */
.model-combobox { position: relative; z-index: 20; }
.model-input-row { display: flex; gap: 8px; }
.model-input-row input { flex: 1; }
.fetch-btn { white-space: nowrap; flex-shrink: 0; }
.model-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  right: 0;
  margin-top: 4px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-hover);
  border-radius: 8px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  z-index: 100;
  overflow: hidden;
}
.model-search {
  border: none;
  border-bottom: 1px solid var(--border);
  border-radius: 0;
  font-size: 13px;
  padding: 8px 12px;
}
.model-search:focus { box-shadow: none; }
.model-list { max-height: 240px; overflow-y: auto; }
.model-option {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  width: 100%;
  padding: 8px 12px;
  border: none;
  background: transparent;
  color: var(--text-primary);
  font-size: 13px;
  cursor: pointer;
  text-align: left;
  transition: background 0.1s;
}
.model-option:hover { background: rgba(99, 102, 241, 0.1); }
.model-option.selected { background: rgba(99, 102, 241, 0.15); }
.model-id { font-weight: 500; }
.model-desc { font-size: 11px; color: var(--text-muted); }
.model-empty { padding: 12px; text-align: center; font-size: 13px; color: var(--text-dim); }
.model-error { font-size: 12px; color: var(--error); margin-top: 4px; }

/* Skill picker */
.doc-picker { position: relative; z-index: 10; }
.doc-tags {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 6px;
  min-height: 36px;
  cursor: text;
  transition: border-color 0.15s;
}
.doc-tags:focus-within { border-color: var(--accent); }
.doc-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  background: rgba(99, 102, 241, 0.15);
  color: var(--accent-light);
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
}
.doc-tag-remove {
  border: none;
  background: none;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  padding: 0 2px;
  transition: color 0.1s;
}
.doc-tag-remove:hover { color: var(--error); }
.doc-input {
  border: none;
  background: transparent;
  outline: none;
  font-size: 13px;
  color: var(--text-primary);
  min-width: 80px;
  flex: 1;
  padding: 0;
}
.doc-input::placeholder { color: var(--text-dim); }
.doc-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  right: 0;
  margin-top: 4px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-hover);
  border-radius: 8px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  z-index: 100;
  max-height: 200px;
  overflow-y: auto;
}
.doc-option {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  width: 100%;
  padding: 8px 12px;
  border: none;
  background: transparent;
  color: var(--text-primary);
  font-size: 13px;
  cursor: pointer;
  text-align: left;
  transition: background 0.1s;
}
.doc-option:hover { background: rgba(99, 102, 241, 0.1); }
.doc-option-name { font-weight: 500; }
.doc-option-desc { font-size: 11px; color: var(--text-muted); }

.spinner {
  display: inline-block;
  width: 14px;
  height: 14px;
  border: 2px solid var(--border-hover);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 200px;
  color: var(--text-muted);
  font-size: 14px;
}

@media (max-width: 768px) {
  .row { grid-template-columns: 1fr; }
  .model-input-row { flex-direction: column; }
  .limits-row { flex-direction: column; }
}
</style>
