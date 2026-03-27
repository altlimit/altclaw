import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useWorkspaceStore } from './workspace'
import { useEventStore } from './events'

export interface ProviderInfo {
  id: number
  name: string
  provider: string
  model: string
}

export const useProviderStore = defineStore('providers', () => {
  const providers = ref<ProviderInfo[]>([])
  const selectedProvider = ref('')
  const loaded = ref(false)

  const noProviders = computed(() => providers.value.length === 0)

  const selectedProviderLabel = computed(() => {
    const p = providers.value.find(p => p.name === selectedProvider.value)
    return p ? p.name : selectedProvider.value || ''
  })

  async function fetchProviders() {
    if (loaded.value) return
    try {
      const wsStore = useWorkspaceStore()
      if (!wsStore.loaded) await wsStore.load()

      const provResp = await fetch('/api/providers')
      if (provResp.ok) {
        const data = await provResp.json()
        if (Array.isArray(data) && data.length > 0) {
          providers.value = data
        }
      }
      const lastProv = wsStore.lastProvider
      if (lastProv && providers.value.some(p => p.name === lastProv)) {
        selectedProvider.value = lastProv
      }
      if (!selectedProvider.value && providers.value.length > 0) {
        selectedProvider.value = providers.value[0]?.name ?? ''
      }
      loaded.value = true
    } catch { /* ignore */ }
  }

  function selectProvider(name: string) {
    selectedProvider.value = name
    fetch('/api/patch-last-provider', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ last_provider: name }),
    }).catch(() => {})
  }

  // Force reload (e.g. after config change)
  function reload() {
    loaded.value = false
    selectedProvider.value = ''
    return fetchProviders()
  }

  let listening = false
  function listenForUpdates() {
    if (listening) return
    listening = true
    const events = useEventStore()
    events.on('config_updated', () => {
      reload()
    })
    events.on('provider_updated', () => {
      reload()
    })
  }

  return { providers, selectedProvider, noProviders, selectedProviderLabel, loaded, fetchProviders, selectProvider, reload, listenForUpdates }
})
