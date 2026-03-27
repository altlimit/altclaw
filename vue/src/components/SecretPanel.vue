<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed } from 'vue'
import { useEditorStore } from '@/stores/editor'
import { useEventStore } from '@/stores/events'
import { useToast } from '@/composables/useToast'

const editorStore = useEditorStore()
const eventStore = useEventStore()
const toast = useToast()

interface Secret {
  id: string
  workspace: string
  value: string
  in_memory?: boolean
  created: string
}

const secrets = ref<Secret[]>([])
const loading = ref(true)
const newSecretName = ref('')
const isCreating = ref(false)

async function loadSecrets() {
  loading.value = true
  try {
    const resp = await fetch('/api/secrets')
    if (!resp.ok) throw new Error(await parseError(resp))
    secrets.value = await resp.json()
  } catch (e: any) {
    toast.error(e.message)
  } finally {
    loading.value = false
  }
}

const workspaceSecrets = computed(() => secrets.value.filter(s => !s.in_memory))
const profileSecrets = computed(() => secrets.value.filter(s => s.in_memory))

async function saveSecret(id: string, value: string, workspace: string) {
  try {
    const resp = await fetch('/api/save-secret', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, value, workspace })
    })
    if (!resp.ok) throw new Error(await parseError(resp))
    await loadSecrets()
    isCreating.value = false
    newSecretName.value = ''
  } catch (e: any) {
    toast.error('Save failed: ' + e.message)
  }
}

function generateRandomValue() {
  const arr = new Uint8Array(16)
  crypto.getRandomValues(arr)
  return Array.from(arr).map(b => b.toString(16).padStart(2, '0')).join('')
}

async function createSecret() {
  if (!newSecretName.value) return
  const name = newSecretName.value.trim()

  // If a secret with this name already exists (local or profile), just open it
  const existing = secrets.value.find(s => s.id === name)
  if (existing) {
    openSecret(existing)
    isCreating.value = false
    newSecretName.value = ''
    return
  }

  // Generate a random placeholder value — backend requires a non-empty value for new secrets.
  // The user can immediately edit it in the opened editor tab.
  const initialValue = generateRandomValue()
  await saveSecret(name, initialValue, "ws")
  // After saving, open the secret editor so the user can set the real value.
  const created = secrets.value.find(s => s.id === name)
  if (created) openSecret(created)
}

async function deleteSecret(secret: Secret) {
  try {
    const resp = await fetch('/api/delete-secret', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: secret.id, workspace: secret.workspace })
    })
    if (!resp.ok) throw new Error(await parseError(resp))
    secrets.value = secrets.value.filter(s => !(s.id === secret.id && s.workspace === secret.workspace))
    editorStore.closeFile(`secret://${secret.workspace ? 'ws' : 'user'}/${secret.id}`)
  } catch (e: any) {
    toast.error('Delete failed: ' + e.message)
  }
}

function openSecret(secret: Secret) {
  const path = `secret://ws/${secret.id}`
  const label = secret.id
  let displayValue = secret.value
  // Provide UX hint if secret is empty
  if (displayValue === '***' || !displayValue) {
    displayValue = ''
  }
  editorStore.openSecretTab(path, label, displayValue, secret.workspace)
}

function onSecretEvent(evt: any) {
  loadSecrets()
}

async function parseError(resp: Response): Promise<string> {
  const text = await resp.text()
  try {
    const json = JSON.parse(text)
    return json.error || json.message || text
  } catch {
    return text || resp.statusText
  }
}

onMounted(() => {
  loadSecrets()
  eventStore.on('secret_updated', onSecretEvent)
})

onUnmounted(() => {
  eventStore.off('secret_updated', onSecretEvent)
})
</script>

<template>
  <div class="secrets-panel">
    <div class="panel-header">
      <span class="panel-title">Secrets</span>
      <div style="display: flex; gap: 4px;">
        <button class="icon-btn" @click="isCreating = !isCreating" title="New Secret">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        </button>
        <button class="icon-btn refresh-btn" @click="loadSecrets" :disabled="loading" title="Refresh">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>
          </svg>
        </button>
      </div>
    </div>

    <div v-if="isCreating" class="create-form">
      <input 
        v-model="newSecretName" 
        placeholder="SECRET_NAME" 
        @keyup.enter="createSecret"
        @keyup.escape="isCreating = false"
        autofocus
      />
      <button @click="createSecret" :disabled="!newSecretName">Add</button>
    </div>

    <div class="panel-list">
      <div v-if="loading" class="panel-empty">Loading…</div>
      <template v-else>
        <!-- Profile Secrets -->
        <div v-if="profileSecrets.length > 0" class="secret-section">
          <div class="section-label">Profile</div>
          <div
            v-for="secret in profileSecrets"
            :key="'profile-' + secret.id"
            class="secret-item"
            @click="openSecret(secret)"
          >
            <div class="secret-top">
              <span class="secret-name">{{ secret.id }}</span>
              <span class="profile-badge">synced</span>
            </div>
            <div class="secret-value">{{ secret.value }}</div>
          </div>
        </div>

        <!-- Workspace Secrets -->
        <div v-if="workspaceSecrets.length > 0" class="secret-section">
          <div class="section-label">Workspace</div>
          <div
            v-for="secret in workspaceSecrets"
            :key="'ws-' + secret.id"
            class="secret-item"
            @click="openSecret(secret)"
          >
            <div class="secret-top">
              <span class="secret-name">{{ secret.id }}</span>
              <button class="secret-delete" @click.stop="deleteSecret(secret)" title="Delete">✕</button>
            </div>
            <div class="secret-value">{{ secret.value }}</div>
          </div>
        </div>



        <div v-if="secrets.length === 0" class="panel-empty">
          <span>No secrets found</span>
          <span class="panel-hint">Use secrets via &#123;&#123;secrets.KEY&#125;&#125; in fetch/sys bridges</span>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.secrets-panel {
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
.icon-btn {
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
.icon-btn:hover {
  color: var(--text-primary);
  background: var(--bg-secondary);
}
.create-form {
  padding: 8px 12px;
  display: flex;
  gap: 8px;
  background: var(--bg-tertiary);
  border-bottom: 1px solid var(--border);
}
.create-form input {
  flex: 1;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  color: var(--text-primary);
  border-radius: 4px;
  padding: 4px 8px;
  font-size: 12px;
  font-family: var(--font-mono);
}
.create-form button {
  background: var(--accent);
  color: #fff;
  border: none;
  border-radius: 4px;
  padding: 4px 8px;
  font-size: 12px;
  cursor: pointer;
  opacity: 0.9;
}
.create-form button:hover { opacity: 1; }
.create-form button:disabled { opacity: 0.5; cursor: not-allowed; }

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
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.panel-hint {
  font-size: 11px;
  color: var(--text-dim);
}
.panel-error {
  padding: 12px;
  font-size: 12px;
  color: var(--error);
}
.secret-section {
  margin-bottom: 4px;
}
.section-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-dim);
  padding: 8px 12px 4px;
}
.secret-item {
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.15s;
}
.secret-item:hover {
  background: var(--bg-secondary);
}
.secret-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.secret-name {
  font-size: 13px;
  font-weight: 500;
  font-family: var(--font-mono);
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  margin-right: 8px;
}
.secret-value {
  font-size: 11px;
  font-family: var(--font-mono);
  color: var(--text-muted);
  margin-top: 2px;
}
.secret-delete {
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
.secret-item:hover .secret-delete {
  opacity: 1;
}
.secret-delete:hover {
  color: var(--error);
}
.profile-badge {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--accent);
  background: color-mix(in srgb, var(--accent) 15%, transparent);
  padding: 1px 6px;
  border-radius: 3px;
  flex-shrink: 0;
}
</style>
