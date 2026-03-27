<script setup lang="ts">
import { useEditorStore } from '@/stores/editor';
import { useWorkspaceStore } from '@/stores/workspace';
import { computed, ref, watch, onActivated } from 'vue';

const props = defineProps<{ path: string }>()
const editorStore = useEditorStore()
const workspaceStore = useWorkspaceStore()

// Parse path: module://workspace/slug, module://user/slug, module://market/slug:version
const parsed = computed(() => {
  const m = props.path.match(/^module:\/\/(workspace|user|market)\/(.+?)(?::(.+))?$/)
  if (!m) return null
  return { scope: m[1], slug: m[2], version: m[3] || '' }
})

const isMarket = computed(() => parsed.value?.scope === 'market')

// State
const readmeHtml = ref('')
const hubStatus = ref<any>(null)
const hubStatusLoading = ref(false)
const publishing = ref(false)
const installing = ref(false)
const actionError = ref('')
const marketVersion = ref<any>(null)
const marketReadmeHtml = ref('')
const liveVersion = ref('')

// ── Loaders ───────────────────────────────────────────────────────────
async function loadReadme() {
  if (!parsed.value || isMarket.value) return
  const { slug, scope } = parsed.value
  try {
    const r = await fetch(
      `/api/module-readme?id=${encodeURIComponent(slug!)}&scope=${scope}`,
      { credentials: 'include' }
    )
    if (r.ok) {
      const text = await r.json()
      readmeHtml.value = text.html
    }
  } catch {}
}

async function loadHubStatus() {
  if (!parsed.value) return
  const { slug, scope } = parsed.value
  hubStatusLoading.value = true
  try {
    const lookupScope = isMarket.value ? 'user' : scope
    const r = await fetch(
      `/api/module-hub-status?id=${encodeURIComponent(slug!)}&scope=${lookupScope}`,
      { credentials: 'include' }
    )
    if (r.ok) hubStatus.value = await r.json()
  } catch {} finally {
    hubStatusLoading.value = false
  }
}

async function loadMarketVersion() {
  if (!parsed.value || !isMarket.value) return
  const { slug } = parsed.value
  try {
    if (!workspaceStore.loaded) {
      await workspaceStore.load()
    }
    const hubURL = (workspaceStore.data.tunnel_hub || '').replace(/\/$/, '')
    if (!hubURL) return
    const r = await fetch(`${hubURL}/api/modules?q=${encodeURIComponent(slug!)}`)
    if (r.ok) {
      const list = await r.json() || []
      const ver = list.find((v: any) => v.slug === slug) || list[0] || null
      marketVersion.value = ver
      if (ver?.readmeContent) {
        const mdResp = await fetch('/api/render-markdown', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ markdown: ver.readmeContent }),
        })
        if (mdResp.ok) {
          const data = await mdResp.json()
          marketReadmeHtml.value = data.html || ''
        }
      }
    }
  } catch {}
}

// ── Computed ──────────────────────────────────────────────────────────
const localVersion = computed(() => liveVersion.value || parsed.value?.version || '')

async function loadLocalVersion() {
  if (!parsed.value || isMarket.value) return
  try {
    const r = await fetch('/api/modules', { credentials: 'include' })
    if (r.ok) {
      const all = await r.json()
      const mod = all.find((m: any) => m.id === parsed.value!.slug && m.scope === parsed.value!.scope)
      if (mod && mod.version) {
        liveVersion.value = mod.version
      }
    }
  } catch {}
}

const canPublish = computed(() => {
  if (!hubStatus.value || !localVersion.value) return false
  if (hubStatus.value.error) return false
  if (hubStatus.value.pending) return false
  if (!hubStatus.value.found) return true
  if (!hubStatus.value.is_owner) return false
  if (isLocalVersionRejected.value) return true
  return compareVersions(localVersion.value, hubStatus.value.latest?.id || '') > 0
})

const isLocalVersionRejected = computed(() => {
  if (!hubStatus.value?.versions || !localVersion.value) return false
  return hubStatus.value.versions.some((v: any) => v.id === localVersion.value && v.status === "rejected")
})

const versionAlreadyExists = computed(() => {
  if (!hubStatus.value?.versions || !localVersion.value) return false
  const activeVersions = hubStatus.value.versions.filter((v: any) => v.status !== "rejected")
  return activeVersions.some((v: any) => v.id === localVersion.value)
})

const updateAvailable = computed(() => {
  if (!hubStatus.value?.latest?.id || !localVersion.value) return false
  return compareVersions(hubStatus.value.latest.id, localVersion.value) > 0
})

function compareVersions(a: string, b: string) {
  const pa = a.replace(/^v/, '').split('.').map(Number)
  const pb = b.replace(/^v/, '').split('.').map(Number)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const diff = (pa[i] || 0) - (pb[i] || 0)
    if (diff !== 0) return diff
  }
  return 0
}


// ── Actions ───────────────────────────────────────────────────────────
async function publish() {
  if (!parsed.value) return
  publishing.value = true
  actionError.value = ''
  try {
    const r = await fetch('/api/publish-module', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: parsed.value.slug, scope: parsed.value.scope })
    })
    const data = await r.json()
    if (!r.ok) actionError.value = data?.error || 'Publish failed'
    else await loadHubStatus()
  } catch (e: any) {
    actionError.value = e.message
  } finally {
    publishing.value = false
  }
}

const allLocalModules = ref<any[]>([])

async function fetchLocalModules() {
  try {
    const r = await fetch('/api/modules', { credentials: 'include' })
    if (r.ok) allLocalModules.value = await r.json()
  } catch {}
}

const isInstalledWorkspace = computed(() => {
  return allLocalModules.value.some(m => m.id === parsed.value?.slug && m.scope === 'workspace')
})
const isInstalledUser = computed(() => {
  return allLocalModules.value.some(m => m.id === parsed.value?.slug && m.scope === 'user')
})

async function installMarketplace(scope: string) {
  if (!marketVersion.value) return
  installing.value = true
  actionError.value = ''
  try {
    const r = await fetch('/api/install-marketplace-module', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: parsed.value!.slug, scope, version: marketVersion.value.id })
    })
    const data = await r.json()
    if (!r.ok) { actionError.value = data?.error || 'Install failed'; return }
    await fetchLocalModules()
    window.dispatchEvent(new CustomEvent('module_installed'))
  } catch (e: any) {
    actionError.value = e.message
  } finally {
    installing.value = false
  }
}

async function uninstallMarketplace(scope: string) {
  if (!parsed.value) return
  actionError.value = ''
  installing.value = true
  try {
    const r = await fetch('/api/delete-module', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: parsed.value.slug, scope })
    })
    if (r.ok) {
      await fetchLocalModules()
      window.dispatchEvent(new CustomEvent('module_deleted', { detail: props.path }))
    } else {
      const d = await r.json()
      actionError.value = d?.error || 'Delete failed'
    }
  } catch (e: any) {
    actionError.value = e.message
  } finally {
    installing.value = false
  }
}

async function installUpdate() {
  if (!parsed.value || !hubStatus.value?.latest?.id) return
  const { slug, scope } = parsed.value
  installing.value = true
  actionError.value = ''
  try {
    const r = await fetch('/api/install-marketplace-module', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: slug, scope, version: hubStatus.value.latest.id })
    })
    const data = await r.json()
    if (!r.ok) { actionError.value = data?.error || 'Install failed'; return }
    await loadHubStatus()
    window.dispatchEvent(new CustomEvent('module_updated'))
  } catch (e: any) {
    actionError.value = e.message
  } finally {
    installing.value = false
  }
}

async function deleteModule() {
  if (!parsed.value) return
  actionError.value = ''
  const r = await fetch('/api/delete-module', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: parsed.value.slug, scope: parsed.value.scope })
  })
  if (r.ok) {
    window.dispatchEvent(new CustomEvent('module_deleted', { detail: props.path }))
    editorStore.closeFile(props.path)
  }
  else { const d = await r.json(); actionError.value = d?.error || 'Delete failed' }
}

function load() {
  readmeHtml.value = ''
  marketVersion.value = null
  marketReadmeHtml.value = ''
  hubStatus.value = null
  actionError.value = ''
  if (isMarket.value) {
    loadMarketVersion()
    fetchLocalModules()
    loadHubStatus()
  } else {
    loadLocalVersion()
    loadReadme()
    loadHubStatus()
  }
}

async function deleteMarketplaceVersion(versionId: string) {
  if (!parsed.value) return
  if (!confirm(`Are you sure you want to permanently delete version ${versionId} from the marketplace?`)) return
  
  actionError.value = ''
  const r = await fetch('/api/delete-module-version', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: parsed.value.slug, version: versionId })
  })
  if (r.ok) {
    await loadHubStatus()
  } else {
    const d = await r.json()
    actionError.value = d?.error || 'Delete failed'
  }
}

watch(() => props.path, load, { immediate: true })
onActivated(() => load())
</script>

<template>
  <div class="module-detail">
    <!-- Header -->
    <div class="mod-header">
      <div class="mod-title-row">
        <span class="mod-slug">{{ parsed?.slug }}</span>
        <span v-if="localVersion && !isMarket" class="mod-ver">v{{ localVersion }}</span>
        <span v-if="isMarket" class="scope-badge market">marketplace</span>
        <span v-else class="scope-badge" :class="parsed?.scope">{{ parsed?.scope }}</span>
        <button class="btn btn-ghost" style="margin-left: auto; padding: 2px 6px; font-size: 11px;" @click="load" title="Refresh">
          ↻ Refresh
        </button>
      </div>

      <!-- Market version info -->
      <div v-if="isMarket && marketVersion" class="mod-desc">{{ marketVersion.description }}</div>
    </div>

    <div class="mod-body">
      <!-- ── Installed module ── -->
      <template v-if="!isMarket">
        <!-- Hub status -->
        <div v-if="hubStatusLoading" class="hub-status loading">Checking marketplace…</div>
        <div v-else-if="hubStatus" class="hub-status">
          <span v-if="hubStatus.is_owner" class="badge owner">✓ You own this</span>
          <span v-else-if="hubStatus.found" class="badge published">Published</span>
          <span v-else class="badge unpublished">Not on marketplace</span>

          <span v-if="hubStatus.pending" class="badge pending">⏳ Pending Approval</span>
        </div>

        <!-- Owned Versions List -->
        <div v-if="hubStatus?.is_owner && hubStatus?.versions?.length" class="versions-section">
          <div class="versions-header">Published Versions</div>
          <div class="versions-list">
            <div v-for="v in hubStatus.versions" :key="v.id" class="version-item">
              <span class="v-name">v{{ v.id }}</span>
              <span class="v-status" :class="v.status">{{ v.status }}</span>
              <span class="v-date">{{ new Date(v.createdAt).toLocaleDateString() }}</span>
              <button class="btn-danger-ghost" @click="deleteMarketplaceVersion(v.id)">Delete</button>
            </div>
          </div>
        </div>

        <!-- Publish -->
        <div class="action-row">
          <button v-if="!versionAlreadyExists" class="btn-primary" :disabled="publishing || !canPublish || (hubStatus?.found && !hubStatus?.is_owner) || hubStatus?.error" @click="publish">
            {{ publishing ? 'Publishing…' : hubStatus?.found ? 'Publish Update' : 'Publish to Marketplace' }}
          </button>
          <span v-if="hubStatus?.error" class="error-msg" style="padding:4px 8px; font-size:11px;">{{ hubStatus.error }}</span>
          <span v-else-if="hubStatus?.found && !hubStatus?.is_owner" class="error-msg" style="padding:4px 8px; font-size:11px;">Module slug already taken on marketplace</span>
          <span v-else-if="!localVersion" class="error-msg" style="padding:4px 8px; font-size:11px;">package.json requires a version to publish</span>
          <span v-else-if="hubStatus?.found && !canPublish && hubStatus?.is_owner" class="hint" style="font-size:11px; color:#fbbf24;">Bump version in package.json to publish update</span>
        </div>

        <!-- Update available -->
        <div v-if="updateAvailable" class="action-row update-row">
          <span class="update-notice">Update available: v{{ hubStatus?.latest?.id }}</span>
          <button class="btn-secondary" :disabled="installing" @click="installUpdate">
            {{ installing ? 'Installing…' : 'Install Update' }}
          </button>
        </div>

        <!-- Delete -->
        <div class="action-row">
          <button class="btn-danger" @click="deleteModule">Remove Module</button>
        </div>

        <!-- README -->
        <div class="readme-section">
          <div v-if="readmeHtml" class="readme" v-html="readmeHtml" />
          <div v-else class="empty-readme">No README found</div>
        </div>
      </template>

      <!-- ── Marketplace module ── -->
      <template v-else>
        <div v-if="!marketVersion" class="empty-readme">Loading…</div>
        <template v-else>
          <!-- Manage info -->
          <div v-if="hubStatus?.is_owner" class="hub-status" style="margin-bottom: 12px;">
            <span class="badge owner">✓ You own this module</span>
            <span class="hint" style="font-size: 11px;">You can seamlessly publish updates from your local workspace copy.</span>
          </div>

          <div class="action-row">
            <!-- Workspace operations -->
            <button v-if="!isInstalledWorkspace" class="btn-primary" :disabled="installing" @click="installMarketplace('workspace')">
               {{ installing ? 'Installing…' : 'Install (Workspace)' }}
            </button>
            <button v-else class="btn-danger-ghost" :disabled="installing" @click="uninstallMarketplace('workspace')">
               Remove (Workspace)
            </button>
            
            <!-- User operations -->
            <button v-if="!isInstalledUser" class="btn-secondary" :disabled="installing" @click="installMarketplace('user')">
               {{ installing ? 'Installing…' : 'Install (User)' }}
            </button>
            <button v-else class="btn-danger-ghost" :disabled="installing" @click="uninstallMarketplace('user')">
               Remove (User)
            </button>

            <span class="mod-ver" style="margin-left:auto">v{{ marketVersion.id }}</span>
          </div>
          <div v-if="marketReadmeHtml" class="readme-section">
            <div class="readme" v-html="marketReadmeHtml" />
          </div>
          <div v-else class="empty-readme">No README</div>
        </template>
      </template>

      <div v-if="actionError" class="error-msg">{{ actionError }}</div>
    </div>
  </div>
</template>

<style scoped>
.module-detail {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  font-size: 13px;
  color: var(--text-primary);
}
.mod-header {
  padding: 20px 24px 14px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-secondary);
  flex-shrink: 0;
}
.mod-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 6px;
}
.mod-slug {
  font-size: 18px;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: -.01em;
}
.mod-ver {
  font-size: 12px;
  color: var(--text-muted);
  background: var(--bg-tertiary);
  padding: 2px 7px;
  border-radius: 10px;
}
.scope-badge {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: .04em;
  padding: 2px 8px;
  border-radius: 10px;
  background: var(--bg-tertiary);
  color: var(--text-muted);
}
.scope-badge.workspace { background: rgba(99,102,241,.15); color: #818cf8; }
.scope-badge.user { background: rgba(16,185,129,.12); color: #34d399; }
.scope-badge.market { background: rgba(245,158,11,.12); color: #fbbf24; }
.mod-desc {
  font-size: 13px;
  color: var(--text-secondary);
  margin-top: 4px;
}
.mod-body {
  flex: 1;
  overflow-y: auto;
  padding: 20px 24px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.hub-status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--text-muted);
}
.hub-status.loading { font-style: italic; }
.badge {
  display: inline-block;
  padding: 3px 10px;
  border-radius: 12px;
  font-size: 11px;
  font-weight: 500;
  letter-spacing: .02em;
}
.badge.owner { background: rgba(16,185,129,.15); color: #34d399; }
.badge.published { background: rgba(99,102,241,.15); color: #818cf8; }
.badge.unpublished { background: var(--bg-tertiary); color: var(--text-muted); }
.badge.pending { background: rgba(245,158,11,.15); color: #fbbf24; }
.action-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.update-row { padding: 8px 12px; background: rgba(245,158,11,.08); border-radius: 6px; border: 1px solid rgba(245,158,11,.2); }
.update-notice { font-size: 12px; color: #fbbf24; }
.btn-primary, .btn-secondary, .btn-danger {
  padding: 6px 16px;
  border: none;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  transition: opacity .15s;
}
.btn-primary { background: #3b82f6; color: #fff; }
.btn-primary:hover:not(:disabled) { background: #2563eb; }
.btn-secondary { background: rgba(16,185,129,.2); color: #34d399; }
.btn-secondary:hover:not(:disabled) { background: rgba(16,185,129,.3); }
.btn-danger { background: rgba(239,68,68,.12); color: #f87171; }
.btn-danger:hover:not(:disabled) { background: rgba(239,68,68,.2); }
button:disabled { opacity: .45; cursor: not-allowed; }
.readme-section {
  border-top: 1px solid var(--border);
  padding-top: 16px;
  margin-top: 4px;
}
.readme {
  font-size: 13px;
  line-height: 1.7;
  color: var(--text-secondary);
}
.readme :deep(h1), .readme :deep(h2), .readme :deep(h3) {
  color: var(--text-primary);
  margin: 16px 0 8px;
  padding-bottom: 4px;
  border-bottom: 1px solid var(--border);
}
.readme :deep(h1) { font-size: 18px; }
.readme :deep(h2) { font-size: 15px; }
.readme :deep(h3) { font-size: 13px; border-bottom: none; }
.readme :deep(code) {
  background: var(--bg-tertiary);
  padding: 1px 5px;
  border-radius: 3px;
  font-size: 12px;
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
}
.readme :deep(pre) {
  background: var(--bg-secondary);
  padding: 12px;
  border-radius: 6px;
  overflow-x: auto;
  font-size: 12px;
}
.readme :deep(pre code) { background: none; padding: 0; }
.readme :deep(a) { color: #60a5fa; }
.readme :deep(ul), .readme :deep(ol) { padding-left: 20px; }
.empty-readme { font-size: 12px; color: var(--text-muted); font-style: italic; }
.error-msg {
  color: #f87171;
  font-size: 12px;
  padding: 8px 12px;
  background: rgba(239,68,68,.08);
  border-radius: 6px;
  border: 1px solid rgba(239,68,68,.2);
}
.versions-section { margin-top: 12px; border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }
.versions-header { background: var(--bg-tertiary); padding: 8px 12px; font-size: 11px; font-weight: 600; text-transform: uppercase; color: var(--text-muted); border-bottom: 1px solid var(--border); }
.versions-list { display: flex; flex-direction: column; }
.version-item { display: grid; grid-template-columns: 80px 80px 1fr auto; align-items: center; padding: 6px 12px; border-bottom: 1px solid var(--border); font-size: 12px; }
.version-item:last-child { border-bottom: none; }
.v-name { font-weight: 600; }
.v-status { font-size: 10px; text-transform: uppercase; padding: 2px 6px; border-radius: 4px; background: var(--bg-tertiary); text-align: center; }
.v-status.approved { background: rgba(16,185,129,.15); color: #34d399; }
.v-status.pending { background: rgba(245,158,11,.15); color: #fbbf24; }
.v-status.rejected { background: rgba(239, 68, 68, 0.15); color: #f87171; }
.v-date { color: var(--text-muted); font-size: 11px; }
.btn-danger-ghost { background: transparent; color: #f87171; border: 1px solid transparent; border-radius: 4px; padding: 4px 8px; cursor: pointer; font-size: 11px; transition: all 0.2s; }
.btn-danger-ghost:hover { background: rgba(239, 68, 68, 0.1); border-color: rgba(239, 68, 68, 0.3); }
</style>
