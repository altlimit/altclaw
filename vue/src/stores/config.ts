import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useEventStore } from './events'

export interface CommonSettings {
  rate_limit: number
  daily_prompt_cap: number
  daily_completion_cap: number
  show_thinking: boolean
  message_window: number
  log_level: string
  confirm_mod_install: boolean
  ignore_restricted: boolean
  ip_whitelist: string[]
  allowed_hosts: string[]
  max_iterations: number
  serverjs_timeout: number
  cron_timeout: number
  agent_timeout: number
  run_timeout: number
}

export interface AppConfigData extends CommonSettings {
  executor: string
  docker_image: string
  provider_concurrency: number
  local_whitelist: string[]
}

export const useConfigStore = defineStore('config', () => {
  const data = ref<Partial<AppConfigData>>({})
  const loaded = ref(false)

  async function load() {
    try {
      const resp = await fetch('/api/config')
      if (resp.ok) {
        data.value = await resp.json()
        loaded.value = true
      }
    } catch { /* ignore */ }
  }

  async function save(updates: Partial<AppConfigData>) {
    Object.assign(data.value, updates)
    try {
      await fetch('/api/save-config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data.value),
      })
    } catch { /* ignore */ }
  }

  let listening = false
  function listenForUpdates() {
    if (listening) return
    listening = true
    const events = useEventStore()
    events.on('config_updated', () => {
      load()
    })
  }

  return { data, loaded, load, save, listenForUpdates }
})
