<script setup lang="ts">
import { useConfigStore, type CommonSettings } from '@/stores/config'
import { useWorkspaceStore, type WorkspaceData } from '@/stores/workspace'
import { useEventStore } from '@/stores/events'
import {
  isPushSupported,
  isSubscribedToPush,
  subscribeToPush,
  unsubscribeFromPush,
} from '@/stores/push'
import { onMounted, ref, computed } from 'vue'
import { useToast } from '@/composables/useToast'

const configStore = useConfigStore()
const wsStore = useWorkspaceStore()
const eventStore = useEventStore()
const toast = useToast()

// ── User-level state (AppConfig-only fields) ─────────────────────────
const executor = ref('auto')
const dockerImage = ref('alpine:latest')
const localWhitelistText = ref('')
const providerConcurrency = ref(1)

// ── User-level CommonSettings ────────────────────────────────────────
const userSettings = ref<Partial<CommonSettings>>({})

// ── Workspace-level state ────────────────────────────────────────────
const ws = computed({
  get: () => wsStore.data as WorkspaceData,
  set: (v: WorkspaceData) => wsStore.patch(v),
})

// Push notification state
const pushSupported = ref(false)
const pushEnabled = ref(false)
const pushBusy = ref(false)

// Tag input state for IP whitelist (user + workspace)
const userIpInput = ref('')
const wsIpInput = ref('')
// Tag input state for allowed hosts (user + workspace)
const userHostInput = ref('')
const wsHostInput = ref('')

// ── Loaders ──────────────────────────────────────────────────────────
function applyConfigToRefs(cfg: any) {
  executor.value = cfg.executor || 'auto'
  dockerImage.value = cfg.docker_image || 'alpine:latest'
  localWhitelistText.value = (cfg.local_whitelist || []).join(', ')
  providerConcurrency.value = cfg.provider_concurrency || 1
  userSettings.value = {
    rate_limit: cfg.rate_limit || undefined,
    daily_prompt_cap: cfg.daily_prompt_cap || undefined,
    daily_completion_cap: cfg.daily_completion_cap || undefined,
    show_thinking: cfg.show_thinking || false,
    message_window: cfg.message_window || undefined,
    log_level: cfg.log_level || '',
    confirm_mod_install: cfg.confirm_mod_install || false,
    ignore_restricted: cfg.ignore_restricted || false,
    ip_whitelist: cfg.ip_whitelist || [],
    allowed_hosts: cfg.allowed_hosts || [],
    max_iterations: cfg.max_iterations || undefined,
    serverjs_timeout: cfg.serverjs_timeout || undefined,
    cron_timeout: cfg.cron_timeout || undefined,
    agent_timeout: cfg.agent_timeout || undefined,
    run_timeout: cfg.run_timeout || undefined,
  }
}

async function loadUserConfig() {
  try {
    const resp = await fetch('/api/config')
    if (resp.ok) applyConfigToRefs(await resp.json())
  } catch { toast.error('Failed to load user settings') }
}

async function loadWorkspaceSettings() {
  try { await wsStore.load() } catch { toast.error('Failed to load workspace settings') }
}

// ── Savers ───────────────────────────────────────────────────────────
const numericKeys = ['rate_limit','daily_prompt_cap','daily_completion_cap','message_window',
  'max_iterations','serverjs_timeout','cron_timeout','agent_timeout','run_timeout',
  'log_max_size','log_max_files','provider_concurrency']

function zeroEmpty(obj: any) {
  for (const k of numericKeys) {
    if (k in obj && !obj[k]) obj[k] = 0
  }
}

async function saveUserConfig() {
  const whitelist = localWhitelistText.value.split(',').map(s => s.trim()).filter(Boolean)
  const cfg: any = {
    executor: executor.value,
    docker_image: dockerImage.value,
    provider_concurrency: providerConcurrency.value,
    local_whitelist: whitelist,
    ...userSettings.value,
  }
  zeroEmpty(cfg)
  const resp = await fetch('/api/save-config', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ params: cfg }),
  })
  if (!resp.ok) throw new Error(await parseError(resp))
}

async function saveWorkspaceSettings() {
  const data = { ...ws.value }
  zeroEmpty(data)
  const resp = await fetch('/api/save-workspace-settings', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ params: data }),
  })
  if (!resp.ok) throw new Error(await parseError(resp))
}

async function saveAll() {
  try {
    await Promise.all([saveUserConfig(), saveWorkspaceSettings()])
    toast.success('Settings saved!')
  } catch (e: any) { toast.error('Save failed: ' + e.message) }
}

// ── Push toggle ──────────────────────────────────────────────────────
async function togglePush() {
  pushBusy.value = true
  try {
    if (pushEnabled.value) {
      if (await unsubscribeFromPush()) { pushEnabled.value = false; toast.success('Push notifications disabled.') }
      else toast.error('Failed to unsubscribe.')
    } else {
      if (await subscribeToPush()) { pushEnabled.value = true; toast.success('Push notifications enabled!') }
      else toast.error('Failed to enable notifications.')
    }
  } finally { pushBusy.value = false }
}

// ── Tag helpers ──────────────────────────────────────────────────────
function addTag(list: string[] | undefined, input: string): string[] {
  const arr = list || []
  for (const v of input.split(/[\s,]+/).filter(Boolean)) {
    if (!arr.includes(v)) arr.push(v)
  }
  return arr
}

function addIpUser() { if (!userIpInput.value.trim()) return; userSettings.value.ip_whitelist = addTag(userSettings.value.ip_whitelist, userIpInput.value); userIpInput.value = '' }
function addIpWs() { if (!wsIpInput.value.trim()) return; ws.value.ip_whitelist = addTag(ws.value.ip_whitelist, wsIpInput.value); wsIpInput.value = '' }
function removeIpUser(i: number) { userSettings.value.ip_whitelist?.splice(i, 1) }
function removeIpWs(i: number) { ws.value.ip_whitelist?.splice(i, 1) }
function handleIpKeydownUser(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); addIpUser() }
  if (e.key === 'Backspace' && !userIpInput.value && (userSettings.value.ip_whitelist?.length ?? 0) > 0) userSettings.value.ip_whitelist?.pop()
}
function handleIpKeydownWs(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); addIpWs() }
  if (e.key === 'Backspace' && !wsIpInput.value && ws.value.ip_whitelist?.length > 0) ws.value.ip_whitelist.pop()
}

function addHostUser() { if (!userHostInput.value.trim()) return; userSettings.value.allowed_hosts = addTag(userSettings.value.allowed_hosts, userHostInput.value.toLowerCase()); userHostInput.value = '' }
function addHostWs() { if (!wsHostInput.value.trim()) return; ws.value.allowed_hosts = addTag(ws.value.allowed_hosts, wsHostInput.value.toLowerCase()); wsHostInput.value = '' }
function removeHostUser(i: number) { userSettings.value.allowed_hosts?.splice(i, 1) }
function removeHostWs(i: number) { ws.value.allowed_hosts?.splice(i, 1) }
function handleHostKeydownUser(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); addHostUser() }
  if (e.key === 'Backspace' && !userHostInput.value && (userSettings.value.allowed_hosts?.length ?? 0) > 0) userSettings.value.allowed_hosts?.pop()
}
function handleHostKeydownWs(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); addHostWs() }
  if (e.key === 'Backspace' && !wsHostInput.value && ws.value.allowed_hosts?.length > 0) ws.value.allowed_hosts.pop()
}

// ── Helpers ──────────────────────────────────────────────────────────
async function parseError(resp: Response): Promise<string> {
  const text = await resp.text()
  try { const json = JSON.parse(text); return json.error || json.message || text } catch { return text || resp.statusText }
}

async function resetAll() {
  await Promise.allSettled([loadUserConfig(), loadWorkspaceSettings()])
}

onMounted(async () => {
  await Promise.all([loadUserConfig(), loadWorkspaceSettings()])
  pushSupported.value = await isPushSupported()
  if (pushSupported.value) pushEnabled.value = await isSubscribedToPush()
  configStore.listenForUpdates()
  wsStore.listenForUpdates()
  eventStore.on('config_updated', () => loadUserConfig())
  eventStore.on('workspace_updated', () => loadWorkspaceSettings())
})
</script>


<template>
  <div class="settings-page">
    <h2 class="page-title">Settings</h2>

    <!-- ═══════════ TOP PANELS: non-common settings ═══════════ -->
    <div class="panels">
      <!-- User Settings Panel -->
      <div class="panel">
        <div class="panel-header">👤 User Settings</div>
        <div class="panel-body">
          <div class="pfield">
            <label>Executor</label>
            <select v-model="executor">
              <option value="auto">Auto (Docker/Podman)</option>
              <option value="docker">Docker</option>
              <option value="podman">Podman</option>
              <option value="local">Local</option>
            </select>
          </div>
          <div class="pfield" v-if="executor !== 'local'">
            <label>Docker Image</label>
            <input v-model="dockerImage" placeholder="alpine:latest" />
          </div>
          <div class="pfield" v-else>
            <label>Command Whitelist</label>
            <input v-model="localWhitelistText" placeholder="npm, go, ls  (* = all)" />
          </div>
          <div class="pfield">
            <label>Provider Concurrency</label>
            <input v-model.number="providerConcurrency" type="number" min="0" max="20" placeholder="0" />
          </div>
          <div v-if="executor === 'local'" class="warning-badge">
            ⚠️ Local executor gives AI direct system access
          </div>
        </div>
      </div>

      <!-- Workspace Settings Panel -->
      <div class="panel">
        <div class="panel-header">📁 Workspace Settings</div>
        <div class="panel-body">
          <div class="pfield">
            <label>Name</label>
            <input v-model="ws.name" placeholder="My Project" />
          </div>
          <div class="pfield">
            <label>Path</label>
            <input :value="ws.path" disabled class="disabled-input" />
          </div>
          <div class="pfield">
            <label>Public Directory</label>
            <input v-model="ws.public_dir" placeholder="(empty = disabled)" />
            <span class="pfield-hint">Serves static files from this folder at the root URL. Also enables serverjs HTTP handlers.</span>
          </div>
          <div class="pfield">
            <label>Log File</label>
            <input v-model="ws.log_path" placeholder="(stdout)" />
          </div>
          <div class="pfield-row">
            <div class="pfield mini">
              <label>Log Size MB</label>
              <input v-model.number="ws.log_max_size" type="number" min="1" placeholder="10" />
            </div>
            <div class="pfield mini">
              <label>Max Files</label>
              <input v-model.number="ws.log_max_files" type="number" min="1" placeholder="3" />
            </div>
          </div>
          <label v-if="pushSupported" class="toggle-row" @click.prevent>
            <input type="checkbox" :checked="pushEnabled" @change="togglePush" :disabled="pushBusy" />
            Push Notifications
          </label>
        </div>
      </div>
    </div>

    <!-- ═══════════ COMMON SETTINGS TABLE ═══════════ -->
    <form @submit.prevent="saveAll">
      <div class="common-table-wrap">
        <table class="common-table">
          <thead>
            <tr>
              <th class="col-label">Setting</th>
              <th class="col-val">👤 User Default</th>
              <th class="col-val">📁 Workspace Override</th>
            </tr>
          </thead>
          <tbody>
            <!-- ── Preferences ── -->
            <tr class="cat-divider"><td colspan="3">Preferences</td></tr>
            <tr>
              <td class="col-label">Show Thinking <span class="help" title="Stream AI thinking/code blocks to the chat UI in real-time">ⓘ</span></td>
              <td class="col-val"><input type="checkbox" v-model="userSettings.show_thinking" /></td>
              <td class="col-val"><input type="checkbox" v-model="ws.show_thinking" /></td>
            </tr>
            <tr>
              <td class="col-label">Message Window <span class="help" title="Number of conversation turns included as context. 0 = built-in default">ⓘ</span></td>
              <td class="col-val"><input v-model.number="userSettings.message_window" type="number" min="0" placeholder="10" /></td>
              <td class="col-val"><input v-model.number="ws.message_window" type="number" min="0" :placeholder="String(userSettings.message_window || 10)" /></td>
            </tr>
            <tr>
              <td class="col-label">Log Level</td>
              <td class="col-val">
                <select v-model="userSettings.log_level">
                  <option value="">(default)</option>
                  <option value="debug">Debug</option>
                  <option value="info">Info</option>
                  <option value="warn">Warn</option>
                  <option value="error">Error</option>
                </select>
              </td>
              <td class="col-val">
                <select v-model="ws.log_level">
                  <option value="">{{ userSettings.log_level ? '(default: ' + userSettings.log_level + ')' : '(default)' }}</option>
                  <option value="debug">Debug</option>
                  <option value="info">Info</option>
                  <option value="warn">Warn</option>
                  <option value="error">Error</option>
                </select>
              </td>
            </tr>

            <!-- ── Security ── -->
            <tr class="cat-divider"><td colspan="3">Security</td></tr>
            <tr>
              <td class="col-label">Confirm Module Installs <span class="help" title="Require user approval before the AI can install or remove npm/go modules">ⓘ</span></td>
              <td class="col-val"><input type="checkbox" v-model="userSettings.confirm_mod_install" /></td>
              <td class="col-val"><input type="checkbox" v-model="ws.confirm_mod_install" /></td>
            </tr>
            <tr>
              <td class="col-label">Ignore Restricted Files <span class="help" title="Allow the AI unrestricted access to dotfiles and gitignored paths">ⓘ</span></td>
              <td class="col-val"><input type="checkbox" v-model="userSettings.ignore_restricted" /></td>
              <td class="col-val"><input type="checkbox" v-model="ws.ignore_restricted" /></td>
            </tr>
            <tr>
              <td class="col-label">IP Whitelist <span class="help" title="Restrict serverjs/public directory access to these IPs. Both lists are joined. Empty = allow all.">ⓘ</span></td>
              <td class="col-val">
                <div class="tag-input mini" @click="($refs.userIpEl as HTMLInputElement)?.focus()">
                  <span v-for="(ip, i) in userSettings.ip_whitelist || []" :key="i" class="tag">
                    {{ ip }} <button type="button" class="tag-rm" @click.stop="removeIpUser(i)">&times;</button>
                  </span>
                  <input ref="userIpEl" v-model="userIpInput" class="tag-field" placeholder="IP/CIDR…"
                    @keydown="handleIpKeydownUser" @blur="addIpUser" />
                </div>
              </td>
              <td class="col-val">
                <div class="tag-input mini" @click="($refs.wsIpEl as HTMLInputElement)?.focus()">
                  <span v-for="(ip, i) in ws.ip_whitelist || []" :key="i" class="tag">
                    {{ ip }} <button type="button" class="tag-rm" @click.stop="removeIpWs(i)">&times;</button>
                  </span>
                  <input ref="wsIpEl" v-model="wsIpInput" class="tag-field" placeholder="IP/CIDR…"
                    @keydown="handleIpKeydownWs" @blur="addIpWs" />
                </div>
              </td>
            </tr>
            <tr>
              <td class="col-label">Allowed Hosts <span class="help" title="SSRF protection: only allow fetch() to these hosts. Both lists are joined. Empty = any host (except loopback/private).">ⓘ</span></td>
              <td class="col-val">
                <div class="tag-input mini" @click="($refs.userHostEl as HTMLInputElement)?.focus()">
                  <span v-for="(h, i) in userSettings.allowed_hosts || []" :key="i" class="tag">
                    {{ h }} <button type="button" class="tag-rm" @click.stop="removeHostUser(i)">&times;</button>
                  </span>
                  <input ref="userHostEl" v-model="userHostInput" class="tag-field" placeholder="hostname…"
                    @keydown="handleHostKeydownUser" @blur="addHostUser" />
                </div>
              </td>
              <td class="col-val">
                <div class="tag-input mini" @click="($refs.wsHostEl as HTMLInputElement)?.focus()">
                  <span v-for="(h, i) in ws.allowed_hosts || []" :key="i" class="tag">
                    {{ h }} <button type="button" class="tag-rm" @click.stop="removeHostWs(i)">&times;</button>
                  </span>
                  <input ref="wsHostEl" v-model="wsHostInput" class="tag-field" placeholder="hostname…"
                    @keydown="handleHostKeydownWs" @blur="addHostWs" />
                </div>
              </td>
            </tr>

            <!-- ── Limits ── -->
            <tr class="cat-divider"><td colspan="3">Limits</td></tr>
            <tr>
              <td class="col-label">Rate Limit <span class="unit">req/min</span></td>
              <td class="col-val"><input v-model.number="userSettings.rate_limit" type="number" min="0" placeholder="10" /></td>
              <td class="col-val"><input v-model.number="ws.rate_limit" type="number" min="0" :placeholder="String(userSettings.rate_limit || 10)" /></td>
            </tr>
            <tr>
              <td class="col-label">Daily Input Cap <span class="unit">tokens</span></td>
              <td class="col-val"><input v-model.number="userSettings.daily_prompt_cap" type="number" min="0" placeholder="1000000" /></td>
              <td class="col-val"><input v-model.number="ws.daily_prompt_cap" type="number" min="0" :placeholder="String(userSettings.daily_prompt_cap || 1000000)" /></td>
            </tr>
            <tr>
              <td class="col-label">Daily Output Cap <span class="unit">tokens</span></td>
              <td class="col-val"><input v-model.number="userSettings.daily_completion_cap" type="number" min="0" placeholder="100000" /></td>
              <td class="col-val"><input v-model.number="ws.daily_completion_cap" type="number" min="0" :placeholder="String(userSettings.daily_completion_cap || 100000)" /></td>
            </tr>
            <tr>
              <td class="col-label">Max Iterations <span class="help" title="Maximum code execution rounds per prompt before the agent stops">ⓘ</span></td>
              <td class="col-val"><input v-model.number="userSettings.max_iterations" type="number" min="0" placeholder="20" /></td>
              <td class="col-val"><input v-model.number="ws.max_iterations" type="number" min="0" :placeholder="String(userSettings.max_iterations || 20)" /></td>
            </tr>

            <!-- ── Timeouts ── -->
            <tr class="cat-divider"><td colspan="3">Timeouts <span class="unit">seconds</span></td></tr>
            <tr>
              <td class="col-label">Agent <span class="help" title="Per code-block execution timeout during chat (default: 5 min)">ⓘ</span></td>
              <td class="col-val"><input v-model.number="userSettings.agent_timeout" type="number" min="0" placeholder="300" /></td>
              <td class="col-val"><input v-model.number="ws.agent_timeout" type="number" min="0" :placeholder="String(userSettings.agent_timeout || 300)" /></td>
            </tr>
            <tr>
              <td class="col-label">Cron <span class="help" title="Timeout for scheduled cron job scripts (default: 30 min)">ⓘ</span></td>
              <td class="col-val"><input v-model.number="userSettings.cron_timeout" type="number" min="0" placeholder="1800" /></td>
              <td class="col-val"><input v-model.number="ws.cron_timeout" type="number" min="0" :placeholder="String(userSettings.cron_timeout || 1800)" /></td>
            </tr>
            <tr>
              <td class="col-label">ServerJS <span class="help" title="Timeout for HTTP request handlers in server.js (default: 60 s)">ⓘ</span></td>
              <td class="col-val"><input v-model.number="userSettings.serverjs_timeout" type="number" min="0" placeholder="60" /></td>
              <td class="col-val"><input v-model.number="ws.serverjs_timeout" type="number" min="0" :placeholder="String(userSettings.serverjs_timeout || 60)" /></td>
            </tr>
            <tr>
              <td class="col-label">Run Script <span class="help" title="Timeout for scripts executed via the ▶ run button (default: 10 min)">ⓘ</span></td>
              <td class="col-val"><input v-model.number="userSettings.run_timeout" type="number" min="0" placeholder="600" /></td>
              <td class="col-val"><input v-model.number="ws.run_timeout" type="number" min="0" :placeholder="String(userSettings.run_timeout || 600)" /></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="btn-row">
        <button type="submit" class="btn btn-primary">Save Settings</button>
        <button type="button" class="btn btn-ghost" @click="resetAll">Reset</button>
      </div>
    </form>
  </div>
</template>


<style scoped>
.settings-page {
  max-width: 960px;
  margin: 0 auto;
  padding: 24px 20px;
}
.page-title {
  font-size: 20px;
  font-weight: 700;
  margin-bottom: 20px;
}

/* ── Top panels ── */
.panels {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  margin-bottom: 24px;
}
.panel {
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  background: var(--surface);
}
.panel-header {
  padding: 10px 14px;
  font-size: 13px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
}
.panel-body {
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.pfield {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.pfield label {
  font-size: 12px;
  font-weight: 500;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.03em;
}
.pfield input, .pfield select {
  padding: 6px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius, 6px);
  background: var(--bg, #1a1a2e);
  color: var(--text);
  font-size: 13px;
}
.disabled-input { opacity: 0.6; cursor: not-allowed; }
.pfield-row {
  display: flex;
  gap: 10px;
}
.pfield.mini { flex: 1; }
.warning-badge {
  padding: 6px 10px;
  border-radius: 6px;
  font-size: 12px;
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.25);
  color: var(--error, #f87171);
}
.toggle-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  cursor: pointer;
}
.toggle-row input[type="checkbox"] {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}

/* ── Common settings table ── */
.common-table-wrap {
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  background: var(--surface);
}
.common-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.common-table thead th {
  padding: 10px 14px;
  text-align: left;
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
}
.common-table th.col-label { width: 30%; }
.common-table th.col-val { width: 35%; }
.common-table td {
  padding: 8px 14px;
  border-bottom: 1px solid var(--border);
  vertical-align: middle;
}
.common-table tr:last-child td { border-bottom: none; }
.common-table td.col-label {
  font-weight: 500;
  color: var(--text);
  white-space: nowrap;
}
.common-table td.col-val input[type="number"],
.common-table td.col-val input[type="text"],
.common-table td.col-val select {
  width: 100%;
  padding: 5px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius, 6px);
  background: var(--bg, #1a1a2e);
  color: var(--text);
  font-size: 13px;
  box-sizing: border-box;
}
.common-table td.col-val input[type="checkbox"] {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}

/* Category dividers */
.cat-divider td {
  padding: 8px 14px 6px;
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--accent-light, #818cf8);
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
}

.unit {
  font-size: 11px;
  font-weight: 400;
  color: var(--text-muted);
  text-transform: none;
  letter-spacing: 0;
}

/* ── Tag inputs ── */
.tag-input {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius, 6px);
  background: var(--bg, #1a1a2e);
  cursor: text;
  min-height: 32px;
}
.tag-input.mini { min-height: 28px; }
.tag-input:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px rgba(99, 102, 241, 0.12);
}
.tag {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 1px 6px;
  border-radius: 4px;
  background: rgba(99, 102, 241, 0.15);
  color: var(--accent);
  font-size: 12px;
  font-family: var(--font-mono, monospace);
  line-height: 1.5;
}
.tag-rm {
  background: none;
  border: none;
  color: var(--accent);
  cursor: pointer;
  font-size: 14px;
  padding: 0;
  opacity: 0.5;
}
.tag-rm:hover { opacity: 1; }
.tag-field {
  flex: 1;
  min-width: 60px;
  border: none;
  outline: none;
  background: transparent;
  font-size: 12px;
  color: var(--text);
  padding: 2px 0;
}
.tag-field::placeholder { color: var(--text-muted); }

/* ── Buttons ── */
.btn-row {
  display: flex;
  gap: 12px;
  margin-top: 16px;
}
.btn-sm {
  padding: 6px 14px;
  font-size: 12px;
  font-weight: 500;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-secondary);
  color: var(--text);
  cursor: pointer;
  transition: all 0.15s;
  align-self: flex-start;
}
.btn-sm:hover { border-color: var(--accent); color: var(--accent); }

@media (max-width: 768px) {
  .panels { grid-template-columns: 1fr; }
  .common-table th.col-label,
  .common-table th.col-val { width: auto; }
}
.help {
  font-size: 13px;
  color: var(--text-muted);
  cursor: help;
  opacity: 0.5;
  margin-left: 2px;
}
.help:hover { opacity: 1; }
.pfield-hint {
  font-size: 11px;
  color: var(--text-muted);
  line-height: 1.3;
}
</style>
