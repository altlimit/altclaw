import { defineStore } from 'pinia'
import { ref } from 'vue'

type EventCallback = (evt: any) => void

interface EventSubscription {
  type: string
  chatId?: number
  callback: EventCallback
}

export const useEventStore = defineStore('events', () => {
  const connected = ref(false)
  const subscriptions: EventSubscription[] = []
  let eventSource: EventSource | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null

  function connect() {
    if (eventSource) return

    eventSource = new EventSource('/api/events')

    eventSource.onopen = () => {
      connected.value = true
    }

    eventSource.onmessage = (e) => {
      if (e.data === '[DONE]') return
      try {
        const evt = JSON.parse(e.data)
        dispatch(evt)
      } catch { /* ignore parse errors */ }
    }

    eventSource.onerror = () => {
      connected.value = false
      eventSource?.close()
      eventSource = null
      // Reconnect after delay
      if (!reconnectTimer) {
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null
          connect()
        }, 2000)
      }
    }
  }

  function disconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    eventSource?.close()
    eventSource = null
    connected.value = false
  }

  function dispatch(evt: any) {
    for (const sub of subscriptions) {
      if (sub.type !== evt.type) continue
      // If subscription is scoped to a chat_id, only deliver matching events
      if (sub.chatId !== undefined && evt.chat_id && sub.chatId !== evt.chat_id) continue
      sub.callback(evt)
    }
    // Forward panel-update events to window so components using
    // window.addEventListener (e.g. ModulesPanel) receive them.
    const windowTypes = ['module_updated', 'cron_updated', 'memory_updated']
    if (windowTypes.includes(evt.type)) {
      window.dispatchEvent(new CustomEvent(evt.type, { detail: evt }))
    }
  }

  function on(type: string, callback: EventCallback, chatId?: number) {
    subscriptions.push({ type, chatId, callback })
  }

  function off(type: string, callback: EventCallback) {
    const idx = subscriptions.findIndex(s => s.type === type && s.callback === callback)
    if (idx >= 0) subscriptions.splice(idx, 1)
  }

  // Update the chatId filter for a callback
  function updateChatId(callback: EventCallback, chatId: number) {
    for (const sub of subscriptions) {
      if (sub.callback === callback) {
        sub.chatId = chatId
      }
    }
  }

  return { connected, connect, disconnect, on, off, updateChatId }
})
