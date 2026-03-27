import { ref } from 'vue'

export interface Toast {
  id: number
  message: string
  type: 'error' | 'success' | 'info'
}

let nextId = 0
const toasts = ref<Toast[]>([])

export function useToast() {
  function show(message: string, type: Toast['type'] = 'info', duration = 4000) {
    const id = nextId++
    toasts.value.push({ id, message, type })
    setTimeout(() => {
      toasts.value = toasts.value.filter(t => t.id !== id)
    }, duration)
  }

  return {
    toasts,
    show,
    error: (msg: string) => show(msg, 'error', 5000),
    success: (msg: string) => show(msg, 'success'),
    info: (msg: string) => show(msg, 'info'),
  }
}
