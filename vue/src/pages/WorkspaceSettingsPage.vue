<script setup lang="ts">
import {
  isPushSupported,
  isSubscribedToPush,
  subscribeToPush,
  unsubscribeFromPush,
} from '@/stores/push'
import { useWorkspaceStore, type WorkspaceData } from '@/stores/workspace'
import { computed, onMounted, ref } from 'vue'
import { useToast } from '@/composables/useToast'

const wsStore = useWorkspaceStore()
const toast = useToast()

const settings = computed({
  get: () => wsStore.data as WorkspaceData,
  set: (v: WorkspaceData) => wsStore.patch(v),
})

// Push notification state
const pushSupported = ref(false)
const pushEnabled = ref(false)
const pushBusy = ref(false)

// Tag input state
const hostInput = ref('')
const hostInputEl = ref<HTMLInputElement | null>(null)

async function loadSettings() {
  try {
    await wsStore.load()
  } catch (e: any) {
    toast.error('Load failed: ' + e.message)
  }
}

async function saveSettings() {
  try {
    const resp = await fetch('/api/save-workspace-settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ params: settings.value }),
    })
    if (!resp.ok) throw new Error(await parseError(resp))
    toast.success('Workspace settings saved!')
  } catch (e: any) {
    toast.error('Save failed: ' + e.message)
  }
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

async function togglePush() {
  pushBusy.value = true
  try {
    if (pushEnabled.value) {
      const ok = await unsubscribeFromPush()
      if (ok) {
        pushEnabled.value = false
        toast.success('Push notifications disabled.')
      } else {
        toast.error('Failed to unsubscribe.')
      }
    } else {
      const ok = await subscribeToPush()
      if (ok) {
        pushEnabled.value = true
        toast.success('Push notifications enabled!')
      } else {
        toast.error('Failed to enable notifications. Check browser permissions.')
      }
    }
  } finally {
    pushBusy.value = false
  }
}

function addHost() {
  const raw = hostInput.value.trim().toLowerCase()
  if (!raw) return
  const hosts = raw.split(/[\s,]+/).filter(Boolean)
  if (!settings.value.allowed_hosts) settings.value.allowed_hosts = []
  for (const h of hosts) {
    if (!settings.value.allowed_hosts.includes(h)) {
      settings.value.allowed_hosts.push(h)
    }
  }
  hostInput.value = ''
}

function removeHost(index: number) {
  settings.value.allowed_hosts.splice(index, 1)
}

function handleHostKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault()
    addHost()
  }
  if (e.key === 'Backspace' && hostInput.value === '' && settings.value.allowed_hosts?.length > 0) {
    settings.value.allowed_hosts.pop()
  }
}

function focusHostInput() {
  hostInputEl.value?.focus()
}

onMounted(async () => {
  wsStore.listenForUpdates()
  loadSettings()
  pushSupported.value = await isPushSupported()
  if (pushSupported.value) {
    pushEnabled.value = await isSubscribedToPush()
  }
})
</script>


<template>
  <div class="container">
    <h2 class="page-title">Workspace Settings</h2>

    <form @submit.prevent="saveSettings">
      <div class="section">
        <div class="section-title">General</div>
        <div class="row">
          <div class="field">
            <label>Workspace Name</label>
            <input v-model="settings.name" placeholder="My Project" />
            <div class="hint">Display name for this workspace.</div>
          </div>
          <div class="field">
            <label>Path</label>
            <input :value="settings.path" disabled class="disabled-input" />
            <div class="hint">Workspace directory (read-only, set via CLI).</div>
          </div>
        </div>
        <div class="row">
          <div class="field" style="flex: 2">
            <label>Public Directory</label>
            <input v-model="settings.public_dir" placeholder="(empty = disabled)" />
            <div class="hint">Serve files from this directory at the root URL. Relative paths are resolved from the workspace. Leave empty to disable.</div>
          </div>
        </div>
      </div>

      <div class="section">
        <div class="section-title">Logging</div>
        <div class="row">
          <div class="field" style="flex: 2">
            <label>Log File Path</label>
            <input v-model="settings.log_path" placeholder="(empty = stdout only)" />
            <div class="hint">Custom log file path. Leave empty to log to stdout only.</div>
          </div>
        </div>
        <div class="row">
          <div class="field">
            <label>Log Level</label>
            <select v-model="settings.log_level">
              <option value="debug">Debug</option>
              <option value="info">Info</option>
              <option value="warn">Warn</option>
              <option value="error">Error</option>
            </select>
            <div class="hint">Minimum log level to output.</div>
          </div>
          <div class="field">
            <label>Max Size (MB)</label>
            <input v-model.number="settings.log_max_size" type="number" min="1" max="500" placeholder="10" />
            <div class="hint">Maximum log file size before rotation.</div>
          </div>
          <div class="field">
            <label>Max Rotated Files</label>
            <input v-model.number="settings.log_max_files" type="number" min="1" max="50" placeholder="3" />
            <div class="hint">Number of rotated log files to keep.</div>
          </div>
        </div>
      </div>

      <div class="section">
        <div class="section-title">Chat</div>
        <div class="row">
          <div class="field">
            <label class="toggle-label">
              <input type="checkbox" v-model="settings.show_thinking" />
              Show Thinking
            </label>
            <div class="hint">When enabled, the AI's code blocks stream to the chat in real-time so you can see its thinking process.</div>
          </div>
          <div class="field">
            <label>Message Window</label>
            <input v-model.number="settings.message_window" type="number" min="1" max="100" placeholder="10" />
            <div class="hint">Number of prior conversation turns to keep in context (default: 10).</div>
          </div>
        </div>
      </div>

      <div class="section" v-if="pushSupported">
        <div class="section-title">Notifications</div>
        <div class="row">
          <div class="field">
            <label class="toggle-label">
              <input type="checkbox" :checked="pushEnabled" @change="togglePush" :disabled="pushBusy" />
              Enable Push Notifications
            </label>
            <div class="hint">Receive browser notifications when the agent completes background tasks, cron jobs, or sends alerts via <code>ui.notify()</code>.</div>
          </div>
        </div>
      </div>

      <div class="section">
        <div class="section-title">Security</div>
        <div class="row">
          <div class="field" style="flex: 2">
            <label>Allowed Hosts (SSRF Protection)</label>
            <div class="tag-input" @click="focusHostInput">
              <span v-for="(host, i) in settings.allowed_hosts" :key="i" class="tag">
                {{ host }}
                <button type="button" class="tag-remove" @click.stop="removeHost(i)">&times;</button>
              </span>
              <input
                ref="hostInputEl"
                v-model="hostInput"
                class="tag-field"
                placeholder="Type a hostname and press Enter..."
                @keydown="handleHostKeydown"
                @blur="addHost"
              />
            </div>
            <div class="hint">
              When empty, the agent can access any host except loopback/private IPs.
              When hosts are listed, <strong>only those hosts</strong> are allowed for outbound requests (fetch, browser).
            </div>
          </div>
          <div class="field">
            <label class="toggle-label">
              <input type="checkbox" v-model="settings.confirm_mod_install" />
              Confirm Module Installs
            </label>
            <div class="hint">When enabled, the AI must request your approval before installing or removing marketplace modules.</div>
          </div>
          <div class="field">
            <label class="toggle-label">
              <input type="checkbox" v-model="settings.ignore_restricted" />
              Disable Hidden &amp; Ignored File Security
            </label>
            <div class="hint">
              By default, the AI must confirm before accessing hidden files (dotfiles like <code>.env</code>) or files
              matched by <code>.gitignore</code> / <code>.agentignore</code>. Check this to allow unrestricted access.
            </div>
          </div>
        </div>
      </div>

      <div class="section">
        <div class="section-title">Limits</div>
        <div class="hint" style="margin-bottom:12px">Leave at 0 to use defaults. Defaults: 10 RPM, 1M input tokens/day, 100k output tokens/day.</div>
        <div class="row">
          <div class="field">
            <label>Rate Limit (requests/min)</label>
            <input v-model.number="settings.rate_limit" type="number" min="0" max="1000" placeholder="10" />
            <div class="hint">Default applies to all providers. Each provider can override in Config.</div>
          </div>
          <div class="field">
            <label>Daily Input Token Cap</label>
            <input v-model.number="settings.daily_prompt_cap" type="number" min="0" placeholder="1000000" />
            <div class="hint">Max input (prompt) tokens per day (0 = default 1,000,000).</div>
          </div>
          <div class="field">
            <label>Daily Output Token Cap</label>
            <input v-model.number="settings.daily_completion_cap" type="number" min="0" placeholder="100000" />
            <div class="hint">Max output (completion) tokens per day (0 = default 100,000).</div>
          </div>
        </div>
      </div>

      <div class="section">
        <div class="section-title">Timeouts</div>
        <div class="hint" style="margin-bottom:12px">Execution time limits per context (seconds). Leave at 0 to use built-in defaults.</div>
        <div class="row">
          <div class="field">
            <label>Agent Timeout (s)</label>
            <input v-model.number="settings.agent_timeout" type="number" min="0" placeholder="300" />
            <div class="hint">Code-block execution per iteration (default: 5 min).</div>
          </div>
          <div class="field">
            <label>Cron Timeout (s)</label>
            <input v-model.number="settings.cron_timeout" type="number" min="0" placeholder="1800" />
            <div class="hint">Cron script runner (default: 30 min).</div>
          </div>
        </div>
        <div class="row">
          <div class="field">
            <label>ServerJS Timeout (s)</label>
            <input v-model.number="settings.serverjs_timeout" type="number" min="0" placeholder="60" />
            <div class="hint">HTTP request handler for .server.js files (default: 60 s).</div>
          </div>
          <div class="field">
            <label>Run Script Timeout (s)</label>
            <input v-model.number="settings.run_timeout" type="number" min="0" placeholder="600" />
            <div class="hint">Running scripts from <span style="color:#a6e3a1">▶</span> (default: 10 min).</div>
          </div>
        </div>
      </div>

      <div class="btn-row">
        <button type="submit" class="btn btn-primary">Save Settings</button>
        <button type="button" class="btn btn-ghost" @click="loadSettings()">Reset</button>
      </div>
    </form>
  </div>
</template>

<style scoped>
.page-title {
  font-size: 20px;
  font-weight: 700;
  margin-bottom: 20px;
}
.disabled-input {
  opacity: 0.6;
  cursor: not-allowed;
}
.btn-row {
  display: flex;
  gap: 12px;
  margin-top: 24px;
}

/* Tag Input */
.tag-input {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  cursor: text;
  min-height: 38px;
}
.tag-input:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px rgba(var(--accent-rgb, 99, 102, 241), 0.15);
}
.tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 4px;
  background: var(--accent-dim, rgba(99, 102, 241, 0.15));
  color: var(--accent);
  font-size: 13px;
  font-family: var(--font-mono, monospace);
  line-height: 1.6;
}
.tag-remove {
  background: none;
  border: none;
  color: var(--accent);
  cursor: pointer;
  font-size: 15px;
  padding: 0 2px;
  line-height: 1;
  opacity: 0.6;
}
.tag-remove:hover {
  opacity: 1;
}
.tag-field {
  flex: 1;
  min-width: 140px;
  border: none;
  outline: none;
  background: transparent;
  font-size: 13px;
  color: var(--text);
  padding: 2px 0;
}
.tag-field::placeholder {
  color: var(--text-muted);
}

/* Mobile Responsive */
@media (max-width: 768px) {
  .row {
    grid-template-columns: 1fr;
  }
}
.toggle-label {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-size: 14px;
}
.toggle-label input[type="checkbox"] {
  width: 18px;
  height: 18px;
  accent-color: var(--accent);
}
</style>
