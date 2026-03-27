<script setup lang="ts">
import ChatPanel from '@/components/ChatPanel.vue'
import CronPanel from '@/components/CronPanel.vue'
import LogPanel from '@/components/LogPanel.vue'
import EditorTabs from '@/components/EditorTabs.vue'
import GitPanel from '@/components/GitPanel.vue'
import MediaPreview from '@/components/MediaPreview.vue'
import MemoryPanel from '@/components/MemoryPanel.vue'
import ModulesPanel from '@/components/ModulesPanel.vue'
import CsvPreview from '@/components/CsvPreview.vue'
import MonacoDiffEditor from '@/components/MonacoDiffEditor.vue'
import MonacoEditor from '@/components/MonacoEditor.vue'
import SearchPanel from '@/components/SearchPanel.vue'
import SecretPanel from '@/components/SecretPanel.vue'
import ProvidersPanel from '@/components/ProvidersPanel.vue'
import ProviderConfigPage from '@/pages/ProviderConfigPage.vue'
import ModuleDetailPage from '@/pages/ModuleDetailPage.vue'
import SecurityPage from '@/pages/SecurityPage.vue'
import SettingsPage from '@/pages/SettingsPage.vue'
import TokenUsagePage from '@/pages/TokenUsagePage.vue'
import TunnelPage from '@/pages/TunnelPage.vue'
import WorkspacePage from '@/pages/WorkspacePage.vue'
import { useAuthStore } from '@/stores/auth'
import { useEditorStore } from '@/stores/editor'
import { useEventStore } from '@/stores/events'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAppModeStore } from '@/stores/appMode'
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'

// ── Script Runner ─────────────────────────────────────────────────
const scriptOutput = ref<{ type: string; content: string }[]>([])
const scriptRunning = ref(false)
const scriptPath = ref('')
let scriptEs: EventSource | null = null

function runScript(path: string) {
  // Cancel any in-flight run
  if (scriptEs) { scriptEs.close(); scriptEs = null }
  scriptPath.value = path
  scriptOutput.value = []
  scriptRunning.value = true

  const es = new EventSource('/api/run-script?path=' + encodeURIComponent(path))
  scriptEs = es
  es.onmessage = (e) => {
    try {
      const msg = JSON.parse(e.data) as { type: string; content: string }
      if (msg.type === 'done') {
        scriptRunning.value = false
        es.close(); scriptEs = null
      } else {
        scriptOutput.value.push(msg)
      }
    } catch { /* ignore */ }
  }
  es.onerror = () => {
    scriptRunning.value = false
    es.close(); scriptEs = null
  }
}

function clearScriptOutput() {
  scriptOutput.value = []
  scriptPath.value = ''
  if (scriptEs) { scriptEs.close(); scriptEs = null }
  scriptRunning.value = false
}

interface ChatEntry {
  id: number
  title: string
  created: string
  modified: string
}

const routerApp = useRouter()
const editorStore = useEditorStore()
const appMode = useAppModeStore()
const authStore = useAuthStore()

const currentActivity = ref('chats')
const agentMenuOpen = ref(false)
const agentMenuRef = ref<HTMLElement | null>(null)
const agentActivities = ['cron', 'modules', 'memory', 'secrets', 'git']
const providersPanelRef = ref<InstanceType<typeof ProvidersPanel> | null>(null)

function toggleActivity(act: string) {
  if (currentActivity.value === act) {
    currentActivity.value = ''
  } else {
    currentActivity.value = act
  }
  // Close agent menu when switching to a non-agent activity
  if (!agentActivities.includes(act)) {
    agentMenuOpen.value = false
  }
}
function selectAgentTool(act: string) {
  toggleActivity(act)
  // Keep menu open when agent tool is active, close if deselected
  if (currentActivity.value === '') {
    agentMenuOpen.value = false
  }
}

function handleMainAreaClick() {
  if (window.innerWidth <= 768) {
    if (currentActivity.value !== '') {
      currentActivity.value = ''
    }
    if (agentMenuOpen.value) {
      agentMenuOpen.value = false
    }
  }
}

const workspaceRef = ref<InstanceType<typeof WorkspacePage> | null>(null)


// Chat sidebar state
const chatList = ref<ChatEntry[]>([])
const hasMoreChats = ref(false)
let chatCursor = ''

async function loadChats(append = false) {
  try {
    let url = '/api/chats?limit=30'
    if (append && chatCursor) url += '&cursor=' + encodeURIComponent(chatCursor)
    const resp = await fetch(url)
    if (resp.ok) {
      const data = await resp.json()
      const list: ChatEntry[] = data.chats || []
      if (append) {
        chatList.value = [...chatList.value, ...list]
      } else {
        chatList.value = list
      }
      chatCursor = data.cursor || ''
      hasMoreChats.value = chatCursor !== ''
    }
  } catch { /* ignore */ }
}

function loadMoreChats() {
  if (hasMoreChats.value) loadChats(true)
}

function newChat() {
  // Open a chat tab with ID 0 (server will create on first message)
  editorStore.openChatTab(0, 'New Chat')
}

function openChat(chat: ChatEntry) {
  editorStore.openChatTab(chat.id, chat.title || `Chat #${chat.id}`)
}

async function deleteChat(chat: ChatEntry) {
  try {
    await fetch('/api/chats/' + chat.id, { method: 'DELETE' })
    chatList.value = chatList.value.filter(c => c.id !== chat.id)
    // Close the tab if open
    const tabPath = `special://chat-${chat.id}`
    editorStore.closeFile(tabPath)
  } catch { /* ignore */ }
}

// Extract chatId from virtual path like "special://chat-42" or "special://chat-0"
function chatIdFromPath(path: string): number {
  const m = path.match(/^special:\/\/chat-(\d+)$/)
  return m?.[1] ? parseInt(m[1], 10) : 0
}

// Compute all chat tabs currently open
const chatTabs = computed(() => {
  return editorStore.getTabs().filter(t => t.type === 'chat')
})

function handleChatCreated(chatId: number, tabPath: string) {
  // Update the tab in-place (e.g. chat-0 → chat-{id}) without unmounting the component
  const newPath = `special://chat-${chatId}`
  editorStore.updateTabPath(tabPath, newPath, `Chat #${chatId}`)
  loadChats() // refresh sidebar
}

const eventStore = useEventStore()
const wsStore = useWorkspaceStore()

onMounted(async () => {
  // Intercept fetch to auto-logout on 401 Unauthorized (browser mode only).
  // In Wails GUI mode, auth is bypassed server-side so we never redirect to login.
  if (!appMode.isWails) {
    const originalFetch = window.fetch
    window.fetch = async (...args: Parameters<typeof fetch>) => {
      const resp = await originalFetch(...args)
      if (resp.status === 401) {
        const url = typeof args[0] === 'string' ? args[0] : (args[0] as Request).url
        // Don't redirect on login endpoint to avoid loops
        if (!url.includes('/api/login')) {
          authStore.logout()
        }
      }
      return resp
    }
  }

  // Load workspace state first
  await wsStore.load()
  // Restore saved tabs
  await editorStore.loadTabs()
  
  // Open a new chat tab only if no tabs were restored
  if (editorStore.getTabs().length === 0) {
    newChat()
  }
  loadChats()

  // Connect SSE event bus
  eventStore.connect()

  // Subscribe to file system events
  eventStore.on('file_changed', (evt: any) => {
    if (evt.path) editorStore.handleDiskChange(evt.path)
  })
  eventStore.on('file_created', (evt: any) => {
    if (evt.path) workspaceRef.value?.refreshTree(evt.path)
  })
  eventStore.on('file_deleted', (evt: any) => {
    if (evt.path) workspaceRef.value?.refreshTree(evt.path)
  })
  eventStore.on('tunnel_status', (evt: any) => {
    setTimeout(() => {
      nextTick(() => {
        wsStore.tunnelStatus = evt.status || 'disconnected'
        wsStore.tunnelPaired = evt.paired || false
        wsStore.tunnelHubUrl = evt.hub_url || ''
        if (evt.url !== undefined) {
          wsStore.patch({ tunnel_host: evt.url })
        }
      })
    }, 500)
  })
  eventStore.on('chat_list_updated', () => {
    chatCursor = ''
    loadChats()
  })

  window.addEventListener('keydown', handleGlobalKeydown)

  // Close agent menu on outside click
  document.addEventListener('click', handleAgentMenuOutsideClick)
})

onUnmounted(() => {
  eventStore.disconnect()
  window.removeEventListener('keydown', handleGlobalKeydown)
  document.removeEventListener('click', handleAgentMenuOutsideClick)
})

function handleAgentMenuOutsideClick(e: MouseEvent) {
  if (agentMenuRef.value && !agentMenuRef.value.contains(e.target as Node)) {
    agentMenuOpen.value = false
  }
}

function handleGlobalKeydown(e: KeyboardEvent) {
  // Ignore shortcuts if we are typing inside an input/textarea
  const target = e.target as HTMLElement
  if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA') && (!e.metaKey && !e.ctrlKey)) {
    return
  }

  // Cmd/Ctrl + P -> Search Panel
  if ((e.metaKey || e.ctrlKey) && e.key === 'p') {
    e.preventDefault()
    currentActivity.value = 'search'
    nextTick(() => {
      window.dispatchEvent(new CustomEvent('focus-search'))
    })
  }

  // Cmd/Ctrl + Shift + F -> Search Panel
  if ((e.metaKey || e.ctrlKey) && e.shiftKey && (e.key === 'f' || e.key === 'F')) {
    e.preventDefault()
    currentActivity.value = 'search'
    nextTick(() => {
      window.dispatchEvent(new CustomEvent('focus-search'))
    })
  }

  // Cmd/Ctrl + J -> Focus Chat
  if ((e.metaKey || e.ctrlKey) && (e.key === 'j' || e.key === 'J')) {
    e.preventDefault()
    currentActivity.value = 'chats'
    const hasChat = editorStore.getTabs().some(t => t.type === 'chat')
    if (!hasChat) {
      newChat()
    } else {
      // Find the first chat tab and activate it if we aren't already on one
      const active = editorStore.getActiveFile()
      if (active?.type !== 'chat') {
        const firstChat = editorStore.getTabs().find(t => t.type === 'chat')
        if (firstChat) editorStore.activateTab(firstChat.path)
      }
    }
    setTimeout(() => {
      window.dispatchEvent(new CustomEvent('focus-chat-input'))
    }, 100)
  }

  // Cmd/Ctrl + L -> Logs Panel
  if ((e.metaKey || e.ctrlKey) && (e.key === 'l' || e.key === 'L')) {
    e.preventDefault()
    openTab('logs', 'Logs')
  }

  // Alt + W -> Close Tab
  if (e.altKey && (e.key === 'w' || e.key === 'W')) {
    const activeFile = editorStore.getActiveFile()
    if (activeFile) {
      e.preventDefault()
      editorStore.closeFile(activeFile.path)
    }
  }

  // Cmd/Ctrl + ] -> Next Tab
  if ((e.metaKey || e.ctrlKey) && e.key === ']') {
    e.preventDefault()
    const tabs = editorStore.getTabs()
    if (tabs.length > 1) {
      const activeFile = editorStore.getActiveFile()
      const idx = tabs.findIndex(t => t.path === activeFile?.path)
      if (idx !== -1) {
        const nextIdx = (idx + 1) % tabs.length
        const nextTab = tabs[nextIdx]
        if (nextTab) editorStore.activateTab(nextTab.path)
      }
    }
  }

  // Cmd/Ctrl + [ -> Previous Tab
  if ((e.metaKey || e.ctrlKey) && e.key === '[') {
    e.preventDefault()
    const tabs = editorStore.getTabs()
    if (tabs.length > 1) {
      const activeFile = editorStore.getActiveFile()
      const idx = tabs.findIndex(t => t.path === activeFile?.path)
      if (idx !== -1) {
        const prevIdx = (idx - 1 + tabs.length) % tabs.length
        const prevTab = tabs[prevIdx]
        if (prevTab) editorStore.activateTab(prevTab.path)
      }
    }
  }
}

function openTab(type: 'chat' | 'config' | 'security' | 'ws-settings' | 'tunnel' | 'cron' | 'memory' | 'token-usage' | 'settings' | 'providers' | 'provider' | 'logs', label: string) {
  editorStore.openSpecialTab(type, label)
}

async function logout() {
  await fetch('/api/auth', { method: 'DELETE' })
  authStore.logout()
}

function getLanguage(path: string | null) {
  if (!path) return 'plaintext'
  if (path.startsWith('memory://entry-')) return 'markdown'
  if (path.startsWith('memory://cron-') || path.startsWith('module://')) return 'javascript'
  if (path.endsWith('.go')) return 'go'
  if (path.endsWith('.js') || path.endsWith('.ts')) return 'javascript'
  if (path.endsWith('.vue')) return 'html'
  if (path.endsWith('.json')) return 'json'
  if (path.endsWith('.md')) return 'markdown'
  if (path.endsWith('.css')) return 'css'
  if (path.endsWith('.html')) return 'html'
  if (path.endsWith('.yaml') || path.endsWith('.yml')) return 'yaml'
  return 'plaintext'
}
</script>

<template>
  <div class="ide-layout">
    <!-- Activity Bar -->
    <nav class="activity-bar">
      <div class="activity-icons">
        <!-- Brand Logo -->
        <div class="brand-logo" :title="'Altclaw ' + wsStore.version">
          <img src="/altclaw.svg" alt="AltClaw Logo" />
        </div>

        <!-- Core Navigation -->
        <button 
          class="activity-btn" 
          :class="{ active: currentActivity === 'chats' }"
          @click="toggleActivity('chats')"
          title="Chats (Cmd+J)"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
        </button>
        <button 
          class="activity-btn" 
          :class="{ active: currentActivity === 'workspace' }"
          @click="toggleActivity('workspace')"
          title="Explorer"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
        </button>
        <button 
          class="activity-btn" 
          :class="{ active: currentActivity === 'search' }"
          @click="toggleActivity('search')"
          title="Search (Cmd+P)"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        </button>

        <div class="activity-separator"></div>

        <!-- Agent Tools (grouped) -->
        <div class="activity-group" ref="agentMenuRef">
          <button 
            class="activity-btn" 
            :class="{ active: agentActivities.includes(currentActivity), open: agentMenuOpen }"
            @click="agentMenuOpen = !agentMenuOpen"
            title="Agent Tools"
          >
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="4" width="14" height="10" rx="2"/><circle cx="9" cy="9" r="1.5" fill="currentColor" stroke="none"/><circle cx="15" cy="9" r="1.5" fill="currentColor" stroke="none"/><path d="M9 14v3"/><path d="M15 14v3"/><path d="M12 1v3"/><path d="M5 8H3"/><path d="M21 8h-2"/></svg>
          </button>
          <transition name="submenu-slide">
            <div v-if="agentMenuOpen" class="activity-submenu">
              <button 
                class="activity-btn sub" 
                :class="{ active: currentActivity === 'cron' }"
                @click="selectAgentTool('cron')"
                title="Cron Jobs"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
              </button>
              <button 
                class="activity-btn sub" 
                :class="{ active: currentActivity === 'modules' }"
                @click="selectAgentTool('modules')"
                title="Modules"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>
              </button>
              <button 
                class="activity-btn sub" 
                :class="{ active: currentActivity === 'memory' }"
                @click="selectAgentTool('memory')"
                title="Memory"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a7 7 0 0 1 7 7c0 2.38-1.19 4.47-3 5.74V17a2 2 0 0 1-2 2h-4a2 2 0 0 1-2-2v-2.26C6.19 13.47 5 11.38 5 9a7 7 0 0 1 7-7z"/><line x1="10" y1="22" x2="14" y2="22"/><line x1="9" y1="17" x2="15" y2="17"/></svg>
              </button>
              <button 
                class="activity-btn sub" 
                :class="{ active: currentActivity === 'secrets' }"
                @click="selectAgentTool('secrets')"
                title="Secrets"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/></svg>
              </button>
              <button 
                class="activity-btn sub" 
                :class="{ active: currentActivity === 'git' }"
                @click="selectAgentTool('git')"
                title="History"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="18" r="3"/><circle cx="12" cy="6" r="3"/><path d="M12 9v6"/></svg>
              </button>
            </div>
          </transition>
        </div>

        <div class="activity-separator"></div>

        <!-- Configuration -->
        <button 
          class="activity-btn" 
          :class="{ active: currentActivity === 'providers' }"
          @click="toggleActivity('providers')"
          title="Providers"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
        </button>
        <button 
          class="activity-btn" 
          @click="openTab('tunnel', 'Tunnel')"
          title="Tunnel"
          :class="{ active: editorStore.getActiveFile()?.type === 'tunnel' }"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
        </button>
        <button 
          class="activity-btn" 
          @click="openTab('settings', 'Settings')"
          title="Settings"
          :class="{ active: editorStore.getActiveFile()?.type === 'settings' }"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
        </button>
        <button 
          class="activity-btn" 
          @click="openTab('security', 'Security')"
          title="Security"
          :class="{ active: editorStore.getActiveFile()?.type === 'security' }"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
        </button>
        <button 
          class="activity-btn" 
          @click="openTab('token-usage', 'Token Usage')"
          title="Token Usage"
          :class="{ active: editorStore.getActiveFile()?.type === 'token-usage' }"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="6" y1="20" x2="6" y2="14"/></svg>
        </button>
        <button 
          class="activity-btn" 
          @click="openTab('logs', 'Logs')"
          title="Logs (Cmd+L)"
          :class="{ active: editorStore.getActiveFile()?.type === 'logs' }"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3v4a1 1 0 0 0 1 1h4"/><path d="M17 21H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h7l5 5v11a2 2 0 0 1-2 2z"/><line x1="9" y1="9" x2="10" y2="9"/><line x1="9" y1="13" x2="15" y2="13"/><line x1="9" y1="17" x2="15" y2="17"/></svg>
        </button>
      </div>
      
      <div class="activity-bottom">

        <div 
          class="sse-dot"
          :class="{ connected: eventStore.connected }"
          :title="eventStore.connected ? 'Server connected' : 'Server disconnected'"
        ></div>
        <button v-if="!appMode.isWails" class="activity-btn logout" @click="logout" title="Sign Out">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>
        </button>
      </div>
    </nav>

    <!-- Primary Sidebar -->
    <aside class="sidebar" v-show="currentActivity === 'workspace' || currentActivity === 'search' || currentActivity === 'chats' || currentActivity === 'cron' || currentActivity === 'modules' || currentActivity === 'memory' || currentActivity === 'secrets' || currentActivity === 'git' || currentActivity === 'providers'">
      <WorkspacePage ref="workspaceRef" v-show="currentActivity === 'workspace'" />
      <SearchPanel v-show="currentActivity === 'search'" />
      <CronPanel v-show="currentActivity === 'cron'" />
      <ModulesPanel v-show="currentActivity === 'modules'" />
      <MemoryPanel v-show="currentActivity === 'memory'" />
      <SecretPanel v-show="currentActivity === 'secrets'" />
      <ProvidersPanel ref="providersPanelRef" v-show="currentActivity === 'providers'" />
      <GitPanel v-show="currentActivity === 'git'" @open-diff="(data: any) => editorStore.openDiffTab(data.path, data.path.split('/').pop() || data.path, data.old_content, data.new_content)" />
      <div v-show="currentActivity === 'chats'" class="chats-sidebar">
        <div class="chats-header">
          <span class="chats-title">Chats</span>
          <button class="new-chat-btn" @click="newChat" title="New Chat">+ New</button>
        </div>
        <div class="chats-list">
          <div
            v-for="chat in chatList"
            :key="chat.id"
            class="chat-item"
            @click="openChat(chat)"
          >
            <span class="chat-item-title">{{ chat.title || 'Untitled' }}</span>
            <button class="chat-item-delete" @click.stop="deleteChat(chat)" title="Delete">✕</button>
          </div>
          <div v-if="chatList.length === 0" class="chats-empty">No chats yet</div>
          <button v-if="hasMoreChats && chatList.length" class="load-more-chats" @click="loadMoreChats">Load More</button>
        </div>
      </div>
    </aside>

    <!-- Center Editor Area (Tabs + Content) -->
    <main class="editor-area" @pointerdownCapture="handleMainAreaClick">
      <EditorTabs 
        :tabs="editorStore.getTabs()" 
        :active-tab="editorStore.activeFilePath || ''" 
        :public-dir="wsStore.publicDir"
        :tunnel-url="wsStore.tunnelUrl"
        @select="path => editorStore.activateTab(path)"
        @close="path => editorStore.closeFile(path)"
        @close-others="path => editorStore.closeOthers(path)"
        @close-all="editorStore.closeAll()"
        @reorder="(from, to) => editorStore.moveTab(from, to)"
        @run-js="runScript"
      />
      <div class="editor-content-wrapper">
        <template v-if="editorStore.getActiveFile()?.type === 'file'">
          <div class="editor-stack">
            <MonacoEditor 
              :model-value="editorStore.getActiveFile()?.content || ''"
              @update:model-value="val => editorStore.updateContent(editorStore.activeFilePath!, val)"
              @save="editorStore.saveActiveFile()"
              @run="editorStore.activeFilePath && runScript(editorStore.activeFilePath)"
              :language="getLanguage(editorStore.activeFilePath)"
              :style="scriptOutput.length > 0 || scriptRunning ? { height: 'calc(100% - 220px)' } : {}"
            />
            <!-- Script Output Panel -->
            <transition name="output-slide">
              <div v-if="scriptOutput.length > 0 || scriptRunning" class="script-output-panel">
                <div class="script-output-header">
                  <span class="script-output-title">
                    <span v-if="scriptRunning" class="script-spinner">⟳</span>
                    <span v-else class="script-done">✓</span>
                    {{ scriptPath.split('/').pop() }}
                  </span>
                  <div class="script-output-actions">
                    <button @click="runScript(scriptPath)" :disabled="scriptRunning" title="Re-run">↺</button>
                    <button @click="clearScriptOutput" title="Close">✕</button>
                  </div>
                </div>
                <div class="script-output-body">
                  <div
                    v-for="(line, i) in scriptOutput"
                    :key="i"
                    class="script-line"
                    :class="'script-line-' + line.type"
                  >{{ line.content }}</div>
                  <div v-if="scriptRunning" class="script-line script-line-running">Running...</div>
                </div>
              </div>
            </transition>
          </div>
        </template>
        <template v-else-if="editorStore.getActiveFile()?.type === 'media'">
          <MediaPreview :path="editorStore.activeFilePath || ''" />
        </template>
        <template v-else-if="editorStore.getActiveFile()?.type === 'csv'">
          <CsvPreview :path="editorStore.activeFilePath || ''" />
        </template>
        <template v-else-if="editorStore.getActiveFile()?.type === 'diff'">
          <MonacoDiffEditor
            :original="editorStore.getDiffData(editorStore.activeFilePath!)?.original || ''"
            :modified="editorStore.getDiffData(editorStore.activeFilePath!)?.modified || ''"
            :language="getLanguage(editorStore.activeFilePath?.replace('diff://', '') || null)"
          />
        </template>
        <template v-else-if="!editorStore.getActiveFile()">
          <!-- No open tab: shows nothing (chat opens by default) -->
        </template>

        <!-- Use v-show for special tabs so they don't unmount and lose state when switching back to files -->
        <template v-for="(tab, idx) in chatTabs" :key="'chat-' + idx">
          <div v-show="editorStore.activeFilePath === tab.path" class="tab-pane chat-pane">
            <ChatPanel
              :chat-id="chatIdFromPath(tab.path)"
              @chat-created="(id: number) => handleChatCreated(id, tab.path)"
              @open-settings="openTab('settings', 'Settings')"
            />
          </div>
        </template>
        <div v-show="editorStore.getActiveFile()?.type === 'config' || editorStore.getActiveFile()?.type === 'settings' || editorStore.getActiveFile()?.type === 'ws-settings'" class="tab-pane boxed">
          <SettingsPage />
        </div>
        <div v-show="editorStore.getActiveFile()?.type === 'security'" class="tab-pane boxed">
          <SecurityPage />
        </div>
        <div v-show="editorStore.getActiveFile()?.type === 'tunnel'" class="tab-pane boxed">
          <TunnelPage />
        </div>
        <div v-show="editorStore.getActiveFile()?.type === 'token-usage'" class="tab-pane boxed">
          <TokenUsagePage />
        </div>
        <div v-show="editorStore.getActiveFile()?.type === 'logs'" class="tab-pane">
          <LogPanel />
        </div>
        <!-- Provider config tabs (one per open provider) -->
        <template v-for="tab in editorStore.getTabs().filter(t => t.type === 'provider')" :key="tab.path">
          <div v-show="editorStore.activeFilePath === tab.path" class="tab-pane boxed">
            <ProviderConfigPage
              :provider-id="parseInt(tab.path.replace('special://provider-', ''), 10)"
              @saved="providersPanelRef?.reload()"
              @deleted="() => { editorStore.closeFile(tab.path); providersPanelRef?.reload() }"
            />
          </div>
        </template>
        <!-- Module detail tabs (one per open module:// path) -->
        <template v-for="tab in editorStore.getTabs().filter(t => t.type === 'module')" :key="tab.path">
          <div v-show="editorStore.activeFilePath === tab.path" class="tab-pane">
            <ModuleDetailPage :path="tab.path" />
          </div>
        </template>
      </div>
    </main>
  </div>
</template>

<style>
.ide-layout {
  display: flex;
  height: 100vh;
  height: 100dvh;
  width: 100vw;
  background: var(--bg-primary);
  color: var(--text-primary);
}

/* Activity Bar (VS Code left strip) */
.activity-bar {
  width: 48px;
  min-width: 48px;
  height: 100%;
  background: var(--bg-secondary);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  align-items: center;
  padding: 8px 0;
  z-index: 10;
}

.activity-icons, .activity-bottom {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}
.brand-logo {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 48px;
  width: 100%;
}
.brand-logo img {
  width: 28px;
  height: 28px;
  object-fit: contain;
}
.activity-separator {
  width: 24px;
  height: 1px;
  background: var(--border);
  margin: 4px 0;
  opacity: 0.5;
}
.activity-group {
  position: relative;
  width: 100%;
}
.activity-submenu {
  display: flex;
  flex-direction: column;
  width: 100%;
  background: var(--bg-tertiary, var(--bg-secondary));
  overflow: hidden;
}
.submenu-slide-enter-active,
.submenu-slide-leave-active {
  transition: max-height 0.2s ease, opacity 0.15s ease;
  max-height: 240px;
}
.submenu-slide-enter-from,
.submenu-slide-leave-to {
  max-height: 0;
  opacity: 0;
}
.activity-btn.sub {
  height: 36px;
  opacity: 0.8;
}
.activity-btn.sub:hover {
  opacity: 1;
}
.activity-btn.open {
  color: var(--accent);
}
.activity-btn {
  background: transparent;
  border: none;
  border-left: 2px solid transparent;
  width: 100%;
  height: 48px;
  display: flex;
  justify-content: center;
  align-items: center;
  color: var(--text-muted);
  cursor: pointer;
  transition: color 0.2s, border-left-color 0.2s;
}

.activity-btn:hover {
  color: var(--text-primary);
}

.activity-btn.active {
  color: var(--accent);
  border-left-color: var(--accent);
}

.activity-btn.logout:hover {
  color: var(--error);
}

.sse-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--error);
  margin: 4px auto;
  animation: sse-pulse 2s ease-in-out infinite;
  transition: background 0.3s;
}
.sse-dot.connected {
  background: var(--success);
  animation: none;
}
@keyframes sse-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
.activity-version {
  font-size: 9px;
  color: var(--text-muted);
  text-align: center;
  line-height: 1;
  opacity: 0.6;
  user-select: none;
  cursor: default;
}

/* Primary Sidebar (Explorer, Settings) */
.sidebar {
  width: 300px;
  min-width: 200px;
  max-width: 600px;
  height: 100%;
  background: var(--bg-primary);
  border-right: 1px solid var(--border);
  overflow-y: auto;
  resize: horizontal;
}

/* Editor Area */
.editor-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: var(--bg-primary);
}

.editor-content-wrapper {
  flex: 1;
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}
/* Wraps monaco + script output panel in a column */
.editor-stack {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

/* Script Output Panel */
.script-output-panel {
  height: 220px;
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  font-size: 12px;
}
.script-output-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 10px;
  background: var(--bg-tertiary, var(--bg-secondary));
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.script-output-title {
  font-size: 12px;
  color: var(--text-muted);
  display: flex;
  align-items: center;
  gap: 6px;
}
.script-spinner {
  display: inline-block;
  animation: spin 1s linear infinite;
  color: var(--accent);
}
.script-done {
  color: #a6e3a1;
}
@keyframes spin { to { transform: rotate(360deg); } }
.script-output-actions {
  display: flex;
  gap: 4px;
}
.script-output-actions button {
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
  padding: 2px 6px;
  border-radius: 4px;
  line-height: 1;
}
.script-output-actions button:hover {
  background: var(--bg-primary);
  color: var(--text-primary);
}
.script-output-actions button:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}
.script-output-body {
  flex: 1;
  overflow-y: auto;
  padding: 6px 12px;
}
.script-line {
  white-space: pre-wrap;
  word-break: break-all;
  line-height: 1.5;
  color: var(--text-primary);
}
.script-line-error {
  color: #f38ba8;
}
.script-line-running {
  color: var(--text-muted);
  font-style: italic;
  opacity: 0.6;
}

/* Slide-up transition for output panel */
.output-slide-enter-active,
.output-slide-leave-active {
  transition: height 0.2s ease, opacity 0.2s ease;
  overflow: hidden;
}
.output-slide-enter-from,
.output-slide-leave-to {
  height: 0 !important;
  opacity: 0;
}
.tab-pane {
  flex: 1;
  overflow-y: auto;
}
.tab-pane.chat-pane {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.tab-pane.boxed {
  padding: 24px;
}
/* Clean up old page containers */
.sidebar .container {
  max-width: 100%;
  padding: 12px;
}

/* Chats Sidebar */
.chats-sidebar {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.chats-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px;
  border-bottom: 1px solid var(--border);
}
.chats-title {
  font-weight: 600;
  font-size: 13px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-muted);
}
.new-chat-btn {
  background: var(--accent);
  color: #fff;
  border: none;
  padding: 4px 10px;
  border-radius: 4px;
  font-size: 12px;
  cursor: pointer;
  transition: opacity 0.2s;
}
.new-chat-btn:hover { opacity: 0.85; }
.chats-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}
.chat-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.15s;
}
.chat-item:hover { background: var(--bg-secondary); }
.chat-item-title {
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  margin-right: 8px;
}
.chat-item-delete {
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
.chat-item:hover .chat-item-delete { opacity: 1; }
.chat-item-delete:hover { color: var(--error); }
.chats-empty {
  text-align: center;
  color: var(--text-muted);
  padding: 24px;
  font-size: 13px;
}
.load-more-chats {
  display: block;
  width: calc(100% - 16px);
  margin: 4px 8px 8px;
  padding: 6px 0;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  transition: color 0.15s, border-color 0.15s;
}
.load-more-chats:hover {
  color: var(--text-primary);
  border-color: var(--accent);
}

/* Mobile Responsive */
@media (max-width: 768px) {
  .ide-layout {
    flex-direction: column-reverse;
  }
  .activity-bar {
    width: 100%;
    height: 60px;
    min-height: 60px;
    flex-direction: row;
    border-right: none;
    border-top: 1px solid var(--border);
    padding: 0 4px;
  }
  .activity-icons {
    flex-direction: row;
    height: 100%;
    width: auto;
    overflow-x: auto;
    -webkit-overflow-scrolling: touch;
    scrollbar-width: none;
  }
  .activity-icons::-webkit-scrollbar {
    display: none;
  }
  .activity-icons .activity-btn {
    flex-shrink: 0;
  }
  .activity-bottom {
    flex-direction: row;
    height: 100%;
    width: auto;
    align-items: center;
    justify-content: flex-end;
  }
  .activity-btn {
    height: 100%;
    width: 50px;
    border-left: none;
    border-top: 2px solid transparent;
  }
  .activity-btn.active {
    border-left-color: transparent;
    border-top-color: var(--accent);
  }
  .sidebar {
    width: 100%;
    max-width: 100%;
    height: 35vh; /* Fixed proportion for mobile sidebar */
    resize: none;
    border-right: none;
    border-bottom: 1px solid var(--border);
  }
  .editor-area {
    flex: 1;
    min-height: 0;
  }
  .activity-separator {
    width: 1px;
    height: 24px;
    margin: 0 4px;
  }
  .activity-group {
    position: static;
  }
  .activity-submenu {
    position: fixed;
    bottom: 60px;
    left: 0;
    right: 0;
    flex-direction: row;
    justify-content: center;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
    z-index: 20;
    padding: 4px 0;
  }
  .activity-btn.sub {
    height: 48px;
    width: 50px;
    flex-shrink: 0;
  }
}
</style>
