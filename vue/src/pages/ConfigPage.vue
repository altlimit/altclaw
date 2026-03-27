<script setup lang="ts">
import { useConfigStore } from '@/stores/config'
import { useEventStore } from '@/stores/events'
import { onMounted, ref, watch } from 'vue'

interface DocInfo {
  name: string
  description: string
  example: string
  filename: string
}

interface ProviderConfig {
  id?: number
  provider: string
  name?: string
  model: string
  api_key: string
  base_url: string
  host: string
  docs: string[]
  description: string
  docker_image: string
  rate_limit: number
  daily_prompt_cap: number
  daily_completion_cap: number
  in_memory?: boolean
}

interface ProviderEntry {
  name: string
  config: ProviderConfig
  models: { id: string; name: string; description?: string }[]
  modelsLoading: boolean
  modelsError: string
  showModels: boolean
  modelFilter: string
  expanded: boolean
  isNew: boolean
  saving: boolean
  status: { msg: string; type: string }
  docSearch: string
  showDocDropdown: boolean
}

interface ModelInfo {
  id: string
  name: string
  description?: string
}

const configStore = useConfigStore()
const eventStore = useEventStore()

const providers = ref<ProviderEntry[]>([])
const executor = ref('auto')
const dockerImage = ref('alpine:latest')
const localWhitelistText = ref('')
const timeout = ref(120)
const maxIterations = ref(20)
const providerConcurrency = ref(1)
const generalStatus = ref({ msg: '', type: '' })
const availableDocs = ref<DocInfo[]>([])
let selfTriggered = false

function emptyProvider(): ProviderConfig {
  return { provider: 'openai', model: '', api_key: '', base_url: '', host: '', docs: [], description: '', docker_image: '', rate_limit: 0, daily_prompt_cap: 0, daily_completion_cap: 0 }
}

function newEntry(name = '', config?: ProviderConfig, isNew = false): ProviderEntry {
  return {
    name,
    config: config || emptyProvider(),
    models: [],
    modelsLoading: false,
    modelsError: '',
    showModels: false,
    modelFilter: '',
    expanded: isNew,
    isNew,
    saving: false,
    status: { msg: '', type: '' },
    docSearch: '',
    showDocDropdown: false,
  }
}

function addProvider() {
  providers.value.forEach(p => p.expanded = false)
  providers.value.push(newEntry('', undefined, true))
}

function toggleProvider(i: number) {
  const entry = providers.value[i]
  if (entry) entry.expanded = !entry.expanded
}

function providerLabel(entry: ProviderEntry): string {
  const types: Record<string, string> = {
    openai: 'OpenAI', gemini: 'Gemini', anthropic: 'Anthropic', ollama: 'Ollama',
    grok: 'Grok (xAI)', deepseek: 'DeepSeek', mistral: 'Mistral', openrouter: 'OpenRouter',
    perplexity: 'Perplexity', hugging_face: 'Hugging Face', minimax: 'MiniMax', glm: 'GLM (Zhipu)',
  }
  return types[entry.config.provider] || entry.config.provider
}

function filteredDocs(entry: ProviderEntry): DocInfo[] {
  const existing = new Set(entry.config.docs || [])
  const q = entry.docSearch.toLowerCase()
  return availableDocs.value.filter(s =>
    !existing.has(s.name) && (s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q))
  )
}

function addDoc(entry: ProviderEntry, docName: string) {
  if (!entry.config.docs) entry.config.docs = []
  if (!entry.config.docs.includes(docName)) {
    entry.config.docs.push(docName)
  }
  entry.docSearch = ''
  entry.showDocDropdown = false
}

function removeDoc(entry: ProviderEntry, docName: string) {
  entry.config.docs = (entry.config.docs || []).filter(s => s !== docName)
}

async function saveProvider(entry: ProviderEntry) {
  entry.saving = true
  entry.status = { msg: '', type: '' }
  const body: any = {
    name: entry.name.trim() || 'default',
    provider: entry.config.provider,
    model: entry.config.model,
    docs: entry.config.docs || [],
    description: entry.config.description,
    docker_image: entry.config.docker_image,
    rate_limit: entry.config.rate_limit || 0,
    daily_prompt_cap: entry.config.daily_prompt_cap || 0,
    daily_completion_cap: entry.config.daily_completion_cap || 0,
  }
  if (entry.config.id) body.id = entry.config.id
  if (entry.config.provider === 'ollama') {
    body.host = entry.config.host
  } else {
    body.api_key = entry.config.api_key
    body.base_url = entry.config.base_url
  }
  try {
    selfTriggered = true
    const resp = await fetch('/api/provider', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!resp.ok) throw new Error(await parseError(resp))
    const data = await resp.json()
    entry.config.id = data.id
    entry.isNew = false
    entry.name = body.name
    showEntryStatus(entry, 'Saved!', 'success')
  } catch (e: any) {
    showEntryStatus(entry, 'Save failed: ' + e.message, 'error')
  } finally {
    entry.saving = false
  }
}

async function deleteProvider(entry: ProviderEntry, i: number) {
  if (entry.isNew || !entry.config.id) {
    providers.value.splice(i, 1)
    return
  }
  try {
    selfTriggered = true
    const resp = await fetch('/api/provider/' + entry.config.id, {
      method: 'DELETE',
    })
    if (!resp.ok) throw new Error(await parseError(resp))
    providers.value.splice(i, 1)
  } catch (e: any) {
    showEntryStatus(entry, 'Delete failed: ' + e.message, 'error')
  }
}

function showEntryStatus(entry: ProviderEntry, msg: string, type: string) {
  entry.status = { msg, type }
  if (type === 'success') {
    setTimeout(() => { entry.status = { msg: '', type: '' } }, 2000)
  }
}

async function fetchModels(entry: ProviderEntry) {
  entry.modelsLoading = true
  entry.modelsError = ''
  entry.models = []
  try {
    const params = new URLSearchParams({ provider: entry.config.provider })
    if (entry.config.api_key) params.set('api_key', entry.config.api_key)
    if (entry.config.base_url) params.set('base_url', entry.config.base_url)
    if (entry.config.host) params.set('host', entry.config.host)
    const resp = await fetch('/api/models?' + params.toString())
    if (!resp.ok) throw new Error(await resp.text() || resp.statusText)
    const data = await resp.json()
    entry.models = data.models || []
    entry.showModels = true
    entry.modelFilter = ''
  } catch (e: any) {
    entry.modelsError = e.message
  } finally {
    entry.modelsLoading = false
  }
}

function selectModel(entry: ProviderEntry, model: ModelInfo) {
  entry.config.model = model.id
  entry.showModels = false
}

function filteredModels(entry: ProviderEntry) {
  if (!entry.modelFilter) return entry.models
  const q = entry.modelFilter.toLowerCase()
  return entry.models.filter(m =>
    m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q)
  )
}

function applyConfigToRefs(cfg: any) {
  executor.value = cfg.executor || 'auto'
  dockerImage.value = cfg.docker_image || 'alpine:latest'
  localWhitelistText.value = (cfg.local_whitelist || []).join(', ')
  timeout.value = cfg.timeout || 120
  maxIterations.value = cfg.max_iterations || 20
  providerConcurrency.value = cfg.provider_concurrency || 1
}

async function loadConfig() {
  try {
    const [cfgResp, provResp, docsResp] = await Promise.all([
      fetch('/api/config'),
      fetch('/api/providers'),
      fetch('/api/docs'),
    ])
    const cfg = await cfgResp.json()
    const provList: ProviderConfig[] = await provResp.json()
    availableDocs.value = await docsResp.json()

    // Load providers
    providers.value = []
    if (provList && provList.length) {
      for (const p of provList) {
        providers.value.push(newEntry(p.name || 'default', p))
      }
    }
    if (!providers.value.length) addProvider()

    // Load general settings
    applyConfigToRefs(cfg)
  } catch (e: any) {
    showGeneralStatus('Load failed: ' + e.message, 'error')
  }
}

async function saveGeneral() {
  const whitelist = localWhitelistText.value.split(',').map(s => s.trim()).filter(Boolean)
  const cfg: any = {
    executor: executor.value,
    docker_image: dockerImage.value,
    timeout: timeout.value,
    max_iterations: maxIterations.value,
    provider_concurrency: providerConcurrency.value,
  }
  cfg.local_whitelist = whitelist
  try {
    const resp = await fetch('/api/save-config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ params: cfg }),
    })
    if (!resp.ok) throw new Error(await parseError(resp))
    showGeneralStatus('Settings saved!', 'success')
  } catch (e: any) {
    showGeneralStatus('Save failed: ' + e.message, 'error')
  }
}

function showGeneralStatus(msg: string, type: string) {
  generalStatus.value = { msg, type }
  if (type === 'success') {
    setTimeout(() => { generalStatus.value = { msg: '', type: '' } }, 3000)
  }
}

watch(() => providers.value.map(p => p.config.provider), (newTypes, oldTypes) => {
  newTypes.forEach((t, i) => {
    const entry = providers.value[i]
    if (entry && oldTypes && oldTypes[i] !== t) {
      entry.models = []
      entry.showModels = false
    }
  })
}, { deep: true })

onMounted(() => {
  loadConfig()

  // Listen for config_updated SSE events (from other tabs, agent, etc.)
  configStore.listenForUpdates()
  eventStore.on('config_updated', () => {
    loadConfig()
  })
  eventStore.on('provider_updated', () => {
    if (selfTriggered) {
      selfTriggered = false
      return
    }
    loadConfig()
  })

  document.addEventListener('click', (e) => {
    providers.value.forEach(entry => {
      if (!(e.target as HTMLElement)?.closest('.model-combobox')) {
        entry.showModels = false
      }
      if (!(e.target as HTMLElement)?.closest('.doc-picker')) {
        entry.showDocDropdown = false
      }
    })
  })
})
async function parseError(resp: Response): Promise<string> {
  const text = await resp.text()
  try {
    const json = JSON.parse(text)
    return json.error || json.message || text
  } catch {
    return text || resp.statusText
  }
}
</script>


<template>
  <div class="container">
    <h2 class="page-title">User Settings</h2>

    <!-- General Settings -->
    <form @submit.prevent="saveGeneral">
      <div class="section">
        <div class="section-title">General</div>
        <div class="row">
          <div class="field">
            <label>Executor</label>
            <select v-model="executor">
              <option value="auto">Auto (Docker/Podman)</option>
              <option value="docker">Docker</option>
              <option value="podman">Podman</option>
              <option value="local">Local</option>
            </select>
          </div>
          <div class="field" v-if="executor === 'docker' || executor === 'podman' || executor === 'auto'">
            <label>Docker Image</label>
            <input v-model="dockerImage" placeholder="alpine:latest" />
          </div>
          <div class="field" v-else-if="executor === 'local'">
            <label>Command Whitelist</label>
            <input v-model="localWhitelistText" placeholder="npm, go, ls" />
            <div class="hint">Comma-separated allowed commands. Use <strong>*</strong> to allow all.</div>
          </div>
        </div>
        <div class="row">
          <div class="field">
            <label>Timeout (seconds)</label>
            <input v-model.number="timeout" type="number" placeholder="120" min="5" max="3600" />
            <div class="hint">Per code block execution timeout.</div>
          </div>
          <div class="field">
            <label>Max Iterations</label>
            <input v-model.number="maxIterations" type="number" placeholder="20" min="1" max="100" />
            <div class="hint">Max code execution rounds per prompt.</div>
          </div>
        </div>
        <div class="row">
          <div class="field">
            <label>Provider Concurrency</label>
            <input v-model.number="providerConcurrency" type="number" placeholder="0" min="0" max="20" />
            <div class="hint">Max simultaneous requests per endpoint (0 = unlimited).</div>
          </div>
        </div>

        <div v-if="executor === 'local'" class="status-msg error" style="margin-top: 12px;">
          <strong>⚠️ SECURITY WARNING:</strong> Local executor gives the AI direct access to your system.
          <span v-if="!localWhitelistText.trim()"> Commands will require your confirmation before executing.</span>
          <span v-else-if="localWhitelistText.trim() === '*'"> All commands will execute without confirmation.</span>
        </div>

        <div class="btn-row" style="margin-top: 16px;">
          <button type="submit" class="btn btn-primary btn-sm">Save Settings</button>
          <span v-if="generalStatus.msg" :class="['inline-status', generalStatus.type]">{{ generalStatus.msg }}</span>
        </div>
      </div>
    </form>

    <!-- Providers -->
    <div class="section">
      <div class="section-header">
        <div class="section-title">Providers</div>
        <button type="button" class="btn btn-ghost btn-sm" @click="addProvider()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          Add Provider
        </button>
      </div>

      <div
        v-for="(entry, i) in providers"
        :key="entry.config.id || i"
        class="accordion"
        :class="{ expanded: entry.expanded }"
      >
        <!-- Accordion Header -->
        <div class="accordion-header" @click="toggleProvider(i)">
          <div class="accordion-left">
            <svg class="accordion-chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>
            <span class="provider-badge">{{ entry.name || 'default' }}</span>
            <span v-if="entry.config.in_memory" class="profile-badge">profile</span>
            <span class="provider-meta">{{ providerLabel(entry) }}</span>
            <span v-if="entry.config.model" class="model-tag">{{ entry.config.model }}</span>
          </div>
          <button
            v-if="!entry.config.in_memory"
            type="button"
            class="btn-icon"
            title="Delete provider"
            @click.stop="deleteProvider(entry, i)"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
          </button>
        </div>

        <!-- Accordion Body -->
        <div class="accordion-body" v-show="entry.expanded">
          <form @submit.prevent="saveProvider(entry)">
            <!-- Row 1: Name + Provider Type -->
            <div class="row">
              <div class="field">
                <label>Name</label>
                <input v-model="entry.name" placeholder="default" />
              </div>
              <div class="field">
                <label>Provider</label>
                <select v-model="entry.config.provider">
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

            <!-- Row 2: Credentials -->
            <div class="row">
              <div class="field" v-if="entry.config.provider !== 'ollama'">
                <label>API Key</label>
                <input v-model="entry.config.api_key" type="password" placeholder="sk-..." />
              </div>
              <div class="field" v-if="entry.config.provider === 'ollama'">
                <label>Host</label>
                <input v-model="entry.config.host" placeholder="http://localhost:11434" />
              </div>
              <div class="field" v-if="entry.config.provider !== 'ollama'" v-show="!entry.config.in_memory">
                <label>Base URL</label>
                <input v-model="entry.config.base_url" placeholder="(default for provider)" />
                <div class="hint">Custom API endpoint. Leave empty for default.</div>
              </div>
            </div>

            <!-- Row 3: Model -->
            <div class="field">
              <label>Model</label>
              <div class="model-combobox">
                <div class="model-input-row">
                  <input
                    v-model="entry.config.model"
                    placeholder="e.g. gpt-4o, gemini-2.5-flash"
                    @focus="entry.models.length && (entry.showModels = true)"
                  />
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm fetch-btn"
                    :disabled="entry.modelsLoading"
                    @click="fetchModels(entry)"
                  >
                    <svg v-if="!entry.modelsLoading" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/></svg>
                    <span v-if="entry.modelsLoading" class="spinner"></span>
                    {{ entry.modelsLoading ? 'Loading...' : 'Fetch Models' }}
                  </button>
                </div>
                <div v-if="entry.modelsError" class="model-error">{{ entry.modelsError }}</div>
                <div v-if="entry.showModels && entry.models.length" class="model-dropdown">
                  <input
                    v-model="entry.modelFilter"
                    class="model-search"
                    placeholder="Filter models..."
                    @click.stop
                  />
                  <div class="model-list">
                    <button
                      v-for="m in filteredModels(entry)"
                      :key="m.id"
                      type="button"
                      class="model-option"
                      :class="{ selected: entry.config.model === m.id }"
                      @click="selectModel(entry, m)"
                    >
                      <span class="model-id">{{ m.id }}</span>
                      <span v-if="m.description" class="model-desc">{{ m.description }}</span>
                    </button>
                    <div v-if="filteredModels(entry).length === 0" class="model-empty">No models match filter</div>
                  </div>
                </div>
              </div>
            </div>

            <!-- Row 4: Description + Skills -->
            <div class="row">
              <div class="field">
                <label>Description</label>
                <input v-model="entry.config.description" placeholder="Best for complex code tasks" />
                <div class="hint">Provider's strengths (shown to AI for delegation).</div>
              </div>
              <div class="field">
                <label>Docs</label>
                <div class="doc-picker">
                  <div class="doc-tags">
                    <span
                      v-for="s in (entry.config.docs || [])"
                      :key="s"
                      class="doc-tag"
                    >
                      {{ s }}
                      <button type="button" class="doc-tag-remove" @click="removeDoc(entry, s)">&times;</button>
                    </span>
                    <input
                      v-model="entry.docSearch"
                      class="doc-input"
                      placeholder="Search docs..."
                      @focus="entry.showDocDropdown = true"
                      @click.stop
                    />
                  </div>
                  <div v-if="entry.showDocDropdown && filteredDocs(entry).length" class="doc-dropdown">
                    <button
                      v-for="s in filteredDocs(entry)"
                      :key="s.name"
                      type="button"
                      class="doc-option"
                      @click.stop="addDoc(entry, s.name)"
                    >
                      <span class="doc-option-name">{{ s.name }}</span>
                      <span class="doc-option-desc">{{ s.description }}</span>
                    </button>
                  </div>
                </div>
                <div class="hint">Pre-loaded docs. Content auto-injected into system prompt.</div>
              </div>
            </div>

            <!-- Row 5: Docker -->
            <div class="field">
              <label>Docker Image Override</label>
              <input v-model="entry.config.docker_image" placeholder="(use global default)" />
              <div class="hint">Override Docker image for this provider's sub-agents.</div>
            </div>

            <!-- Row 6: Limits (compact inline) -->
            <div class="limits-row">
              <div class="limit-field">
                <label>Rate Limit <span class="unit">req/min</span></label>
                <input v-model.number="entry.config.rate_limit" type="number" min="0" placeholder="default" />
              </div>
              <div class="limit-field">
                <label>Input Cap <span class="unit">tokens/day</span></label>
                <input v-model.number="entry.config.daily_prompt_cap" type="number" min="0" placeholder="default" />
              </div>
              <div class="limit-field">
                <label>Output Cap <span class="unit">tokens/day</span></label>
                <input v-model.number="entry.config.daily_completion_cap" type="number" min="0" placeholder="default" />
              </div>
            </div>

            <!-- Save / Status -->
            <div class="btn-row" v-if="!entry.config.in_memory">
              <button type="submit" class="btn btn-primary btn-sm" :disabled="entry.saving">
                {{ entry.saving ? 'Saving...' : (entry.isNew ? 'Create Provider' : 'Save Provider') }}
              </button>
              <span v-if="entry.status.msg" :class="['inline-status', entry.status.type]">{{ entry.status.msg }}</span>
            </div>
          </form>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-title {
  font-size: 20px;
  font-weight: 700;
  margin-bottom: 20px;
}

.row-4 {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr 1fr;
  gap: 16px;
}
.row-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
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

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.section-header .section-title {
  margin-bottom: 0;
}

/* Accordion */
.accordion {
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 8px;
  overflow: hidden;
  transition: border-color 0.15s;
}
.accordion:hover,
.accordion.expanded {
  border-color: var(--border-hover);
}
.accordion-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 16px;
  cursor: pointer;
  background: var(--bg-tertiary);
  user-select: none;
  transition: background 0.1s;
}
.accordion-header:hover {
  background: rgba(99, 102, 241, 0.04);
}
.accordion-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.accordion-chevron {
  flex-shrink: 0;
  color: var(--text-muted);
  transition: transform 0.2s;
}
.accordion.expanded .accordion-chevron {
  transform: rotate(90deg);
}
.provider-badge {
  font-size: 13px;
  font-weight: 600;
  color: var(--accent-light);
  background: rgba(99, 102, 241, 0.1);
  padding: 2px 8px;
  border-radius: 4px;
  white-space: nowrap;
}
.profile-badge {
  font-size: 11px;
  font-weight: 600;
  color: #10b981;
  background: rgba(16, 185, 129, 0.1);
  border: 1px solid rgba(16, 185, 129, 0.25);
  padding: 1px 7px;
  border-radius: 4px;
  white-space: nowrap;
  letter-spacing: 0.02em;
}
.provider-meta {
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
}
.model-tag {
  font-size: 11px;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  padding: 2px 6px;
  border-radius: 4px;
  font-family: monospace;
  white-space: nowrap;
}
.accordion-body {
  padding: 16px 20px 20px;
  border-top: 1px solid var(--border);
  background: var(--bg-secondary);
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

.btn-sm {
  padding: 6px 12px;
  font-size: 13px;
}

/* Model combobox */
.model-combobox { position: relative; }
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
  font-family: 'Inter', sans-serif;
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
.doc-picker {
  position: relative;
}
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
.doc-tags:focus-within {
  border-color: var(--accent);
}
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
.doc-tag-remove:hover {
  color: var(--error);
}
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
.doc-input::placeholder {
  color: var(--text-dim);
}
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
  font-family: 'Inter', sans-serif;
  cursor: pointer;
  text-align: left;
  transition: background 0.1s;
}
.doc-option:hover {
  background: rgba(99, 102, 241, 0.1);
}
.doc-option-name {
  font-weight: 500;
}
.doc-option-desc {
  font-size: 11px;
  color: var(--text-muted);
}

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

.btn-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 16px;
}
.inline-status {
  font-size: 13px;
  font-weight: 500;
}
.inline-status.success { color: var(--success); }
.inline-status.error { color: var(--error); }

.status-msg {
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 13px;
}
.status-msg.error {
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  color: var(--error);
}

/* Mobile Responsive */
@media (max-width: 768px) {
  .row-4,
  .row-2 {
    grid-template-columns: 1fr;
  }
  .row {
    grid-template-columns: 1fr;
  }
  .model-input-row {
    flex-direction: column;
  }
  .model-input-row .btn {
    width: 100%;
    justify-content: center;
  }
  .accordion-left {
    flex-wrap: wrap;
    gap: 6px;
  }
}
</style>
