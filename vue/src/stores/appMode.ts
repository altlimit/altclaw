import { defineStore } from 'pinia'
import { ref } from 'vue'

/**
 * Detects if the app is running inside a Wails native window (WebView2).
 * Check `isWails` anywhere to conditionally hide/show UI elements.
 */
export const useAppModeStore = defineStore('appMode', () => {
  const isWails = ref(
    !!(window as any).chrome?.webview
  )

  return { isWails }
})
