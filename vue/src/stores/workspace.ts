import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useEventStore } from './events'
import type { CommonSettings } from './config'

export interface WorkspaceData extends CommonSettings {
  id: number
  name: string
  path: string
  public_dir: string
  tunnel_host: string
  tunnel_mode: string
  tunnel_hub: string
  last_provider: string
  open_tabs: string
  log_path: string
  log_max_size: number
  log_max_files: number
}

export const useWorkspaceStore = defineStore('workspace', () => {
  const data = ref<Partial<WorkspaceData>>({})
  const loaded = ref(false)

  // Tunnel live state (updated via SSE, not persisted to workspace)
  const tunnelStatus = ref('disconnected')
  const tunnelPaired = ref(false)
  const tunnelHubUrl = ref('')
  const hubUrl = ref('')
  const version = ref('')

  const publicDir = computed(() => data.value.public_dir || '')
  const tunnelUrl = computed(() => data.value.tunnel_host || '')
  const lastProvider = computed(() => data.value.last_provider || '')

  async function load() {
    try {
      const resp = await fetch('/api/workspace-settings', { credentials: 'include' })
      if (resp.ok) {
        const d = await resp.json()
        if (!d.allowed_hosts) d.allowed_hosts = []
        data.value = d.workspace
        hubUrl.value = d.hub_url
        version.value = d.version || 'dev'
        loaded.value = true
      }
    } catch { /* ignore */ }
  }

  async function save(updates: Partial<WorkspaceData>) {
    Object.assign(data.value, updates)
    try {
      await fetch('/api/save-workspace-settings', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data.value),
      })
    } catch { /* ignore */ }
  }

  function patch(updates: Partial<WorkspaceData>) {
    Object.assign(data.value, updates)
  }

  let listening = false
  function listenForUpdates() {
    if (listening) return
    listening = true
    const events = useEventStore()
    events.on('workspace_updated', () => {
      load()
    })
  }

  return { data, loaded, publicDir, tunnelUrl, lastProvider, tunnelStatus, tunnelPaired, tunnelHubUrl, hubUrl, version, load, save, patch, listenForUpdates }
})
