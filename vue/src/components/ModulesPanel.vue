<template>
  <div class="modules-panel">
    <!-- Header -->
    <div class="panel-header">
      <span class="panel-title">Modules</span>
      <div class="header-actions">
        <input
          v-model="search"
          type="text"
          placeholder="Search…"
          class="search-input"
          @input="onSearch"
        />
        <button class="refresh-btn" @click="refresh" :disabled="loading" title="Refresh">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>
          </svg>
        </button>
      </div>
    </div>

    <div class="panel-list">
      <div v-if="loading" class="panel-empty">Loading…</div>
      <template v-else>

        <!-- Workspace Modules -->
        <div v-if="workspaceMods.length > 0" class="mod-section">
          <div class="section-label">Workspace</div>
          <div
            v-for="mod in workspaceMods"
            :key="'ws:' + mod.id"
            class="mod-item"
            :class="{ active: isActive(mod.scope, mod.id) }"
            @click="selectInstalled(mod)"
          >
            <div class="mod-top">
              <span class="mod-name">{{ mod.id }}</span>
              <span v-if="mod.version" class="mod-ver">v{{ mod.version }}</span>
            </div>
          </div>
        </div>

        <!-- User Modules -->
        <div v-if="userMods.length > 0" class="mod-section">
          <div class="section-label">User</div>
          <div
            v-for="mod in userMods"
            :key="'user:' + mod.id"
            class="mod-item"
            :class="{ active: isActive(mod.scope, mod.id) }"
            @click="selectInstalled(mod)"
          >
            <div class="mod-top">
              <span class="mod-name">{{ mod.id }}</span>
              <span v-if="mod.version" class="mod-ver">v{{ mod.version }}</span>
            </div>
          </div>
        </div>

        <!-- Empty installed -->
        <div v-if="filteredInstalled.length === 0 && !search" class="panel-empty">
          <span>No installed modules</span>
          <span class="panel-hint">Install from marketplace or right-click a folder</span>
        </div>
        <div v-else-if="filteredInstalled.length === 0 && search" class="panel-empty">
          <span>No matches</span>
        </div>

        <!-- Marketplace -->
        <div class="mod-section mod-section-market">
          <div class="section-label-row">
            <span class="section-label market-label">Marketplace</span>
            <button class="refresh-btn small" @click="loadMarketplace(search)" :disabled="marketplaceLoading" title="Refresh marketplace">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>
              </svg>
            </button>
          </div>
          <div v-if="marketplaceLoading" class="panel-empty small">Loading…</div>
          <div v-else-if="filteredMarketplace.length === 0" class="panel-empty small">No modules found</div>
          <div
            v-else
            v-for="ver in filteredMarketplace.slice(0, visibleMarketCount)"
            :key="ver.slug + ver.id"
            class="mod-item"
            :class="{ active: isMarketActive(ver.slug) }"
            @click="selectMarket(ver)"
          >
            <div class="mod-top">
              <span class="mod-name">{{ ver.name || ver.slug }}</span>
              <span class="mod-ver market">v{{ ver.id }}</span>
            </div>
            <div class="mod-meta">{{ ver.slug }}</div>
          </div>
          <button v-if="filteredMarketplace.length > visibleMarketCount" class="view-more-btn" @click="visibleMarketCount += 10">
            View More ({{ filteredMarketplace.length - visibleMarketCount }} remaining)
          </button>
        </div>

      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useEditorStore } from '@/stores/editor'
import { useWorkspaceStore } from '@/stores/workspace'
import { computed, onMounted, ref } from 'vue'

const editorStore = useEditorStore()
const workspaceStore = useWorkspaceStore()

const search = ref('')
const installed = ref<any[]>([])
const marketplace = ref<any[]>([])
const marketplaceLoading = ref(false)
const loading = ref(false)
const visibleMarketCount = ref(10)
let searchTimeout: any = null

// ── Filtering ─────────────────────────────────────────────────────────
const filteredInstalled = computed(() => {
  if (!search.value.trim()) return installed.value
  const q = search.value.toLowerCase()
  return installed.value.filter(m => m.id.toLowerCase().includes(q))
})

const workspaceMods = computed(() => filteredInstalled.value.filter(m => m.scope === 'workspace'))
const userMods = computed(() => filteredInstalled.value.filter(m => m.scope === 'user'))

const filteredMarketplace = computed(() => {
  if (!search.value.trim()) return marketplace.value
  const q = search.value.toLowerCase()
  return marketplace.value.filter(m =>
    (m.name || m.slug || '').toLowerCase().includes(q) ||
    (m.description || '').toLowerCase().includes(q)
  )
})

// ── Active state ──────────────────────────────────────────────────────
function isActive(scope: string, id: string) {
  return editorStore.activeFilePath?.startsWith(`module://${scope}/${id}`) ?? false
}
function isMarketActive(slug: string) {
  return editorStore.activeFilePath === `module://market/${slug}`
}

// ── Loaders ───────────────────────────────────────────────────────────
async function loadInstalled() {
  try {
    const r = await fetch('/api/modules', { credentials: 'include' })
    if (r.ok) installed.value = await r.json()
  } catch {}
}

async function loadMarketplace(q = '') {
  marketplaceLoading.value = true
  marketplace.value = []
  visibleMarketCount.value = 10
  try {
    if (!workspaceStore.loaded) {
      await workspaceStore.load()
    }
    const hubURL = (workspaceStore.hubUrl || '').replace(/\/$/, '')
    if (!hubURL) return
    const url = new URL(hubURL + '/api/modules')
    if (q) url.searchParams.set('q', q)
    else url.searchParams.set('limit', '20')
    const r = await fetch(url.toString())
    if (r.ok) marketplace.value = await r.json() || []
  } catch {} finally {
    marketplaceLoading.value = false
  }
}

async function refresh() {
  loading.value = true
  await loadInstalled()
  loading.value = false
  loadMarketplace(search.value)
}

function selectInstalled(mod: any) {
  editorStore.openModuleTab(`module://${mod.scope}/${mod.id}`, `📦 ${mod.id}`)
}

function selectMarket(ver: any) {
  editorStore.openModuleTab(`module://market/${ver.slug}`, `🛒 ${ver.name || ver.slug}`)
}

function onSearch() {
  if (searchTimeout) clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    loadMarketplace(search.value)
  }, 800)
}

onMounted(() => {
  loadInstalled()
  loadMarketplace()
})

window.addEventListener('module_installed', () => loadInstalled())
window.addEventListener('module_deleted', () => loadInstalled())
window.addEventListener('module_updated', () => loadInstalled())
</script>

<style scoped>
.modules-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
}

/* ── Header ─────────────────────────────────────────────────────────── */
.panel-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 12px 8px;
  border-bottom: 1px solid var(--border);
  gap: 8px;
  flex-shrink: 0;
}
.panel-title {
  font-weight: 600;
  font-size: 13px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted);
  white-space: nowrap;
}
.header-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  justify-content: flex-end;
}
.search-input {
  width: 100%;
  max-width: 140px;
  padding: 3px 8px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border);
  border-radius: 4px;
  font-size: 12px;
  outline: none;
  transition: border-color 0.15s;
}
.search-input:focus {
  border-color: var(--accent);
}
.search-input::placeholder {
  color: var(--text-dim);
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
  flex-shrink: 0;
}
.refresh-btn:hover:not(:disabled) {
  color: var(--text-primary);
  background: var(--bg-secondary);
}
.refresh-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.refresh-btn.small { padding: 2px 3px; }

/* ── List ────────────────────────────────────────────────────────────── */
.panel-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}
.panel-empty {
  text-align: center;
  color: var(--text-muted);
  padding: 20px 12px;
  font-size: 13px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.panel-empty.small {
  padding: 8px 12px;
  font-size: 12px;
}
.panel-hint {
  font-size: 11px;
  color: var(--text-dim);
}

.mod-section { margin-bottom: 4px; }
.mod-section-market { border-top: 1px solid var(--border); padding-top: 4px; margin-top: 8px; }
.section-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-dim);
  padding: 8px 12px 4px;
}
.section-label-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-right: 12px;
}
.market-label { padding-right: 0; }

.mod-item {
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.15s;
  border-bottom: 1px solid var(--border);
}
.mod-item:last-child {
  border-bottom: none;
}
.mod-item:hover, .mod-item.active {
  background: var(--bg-secondary);
}
.mod-item.active {
  border-left: 2px solid var(--accent);
  padding-left: 10px;
}
.mod-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 6px;
}
.mod-name {
  font-size: 13px;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  transition: color 0.15s;
}
.mod-ver {
  font-size: 10px;
  color: var(--text-dim);
  white-space: nowrap;
  background: var(--bg-tertiary);
  padding: 1px 5px;
  border-radius: 3px;
}
.mod-ver.market {
  background: rgba(245, 158, 11, 0.1);
  color: #f59e0b;
}
.mod-meta {
  font-size: 10px;
  color: var(--text-dim);
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
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
