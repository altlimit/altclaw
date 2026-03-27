import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useToast } from '@/composables/useToast'

export interface OpenFile {
  path: string
  name: string
  content: string
  isDirty: boolean
  type: 'file' | 'chat' | 'config' | 'security' | 'media' | 'ws-settings' | 'tunnel' | 'cron' | 'memory' | 'token-usage' | 'diff' | 'module' | 'csv' | 'settings' | 'providers' | 'provider' | 'logs'
}

const mediaExtensions = ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico', 'mp4', 'webm', 'ogg', 'mp3', 'wav', 'pdf']

let saveTimer: ReturnType<typeof setTimeout> | null = null

export const useEditorStore = defineStore('editor', () => {
  const tabs = ref<OpenFile[]>([])
  const activeFilePath = ref<string | null>(null)
  const diffMeta = new Map<string, { original: string, modified: string }>()

  const getTabs = () => tabs.value

  const findTab = (path: string) => tabs.value.find(t => t.path === path)

  const getActiveFile = () => {
    if (!activeFilePath.value) return null
    return findTab(activeFilePath.value) || null
  }

  const openFile = async (path: string, name: string) => {
    if (!findTab(path)) {
      const ext = path.split('.').pop()?.toLowerCase() || ''
      const isMedia = mediaExtensions.includes(ext)

      if (isMedia) {
        tabs.value.push({
          path,
          name,
          content: '',
          isDirty: false,
          type: 'media'
        })
        activeFilePath.value = path
        debounceSaveTabs()
        return
      }

      const isCsv = ext === 'csv'
      try {
        const resp = await fetch('/api/files?path=' + encodeURIComponent(path))
        if (!resp.ok) throw new Error('Failed to load')
        const data = await resp.json()

        tabs.value.push({
          path,
          name,
          content: data.content || '',
          isDirty: false,
          type: isCsv ? 'csv' : 'file'
        })
      } catch (e) {
        console.error("Failed to fetch file:", e)
        return
      }
    }
    activeFilePath.value = path
    debounceSaveTabs()
  }

  const openSpecialTab = (type: 'chat' | 'config' | 'security' | 'ws-settings' | 'tunnel' | 'cron' | 'memory' | 'token-usage' | 'settings' | 'providers' | 'provider' | 'logs', name: string, id?: number) => {
    const virtualPath = id ? `special://${type}-${id}` : `special://${type}`
    if (!findTab(virtualPath)) {
      tabs.value.push({
        path: virtualPath,
        name,
        content: '',
        isDirty: false,
        type
      })
    }
    activeFilePath.value = virtualPath
    debounceSaveTabs()
    return virtualPath
  }

  const openChatTab = (chatId: number, title?: string) => {
    return openSpecialTab('chat', title || `Chat #${chatId}`, chatId)
  }

  // Open a memory entry editor tab
  const memoryEntryMeta = new Map<string, { id: number, scope: string }>() // path -> entry meta
  const openMemoryEntryTab = (path: string, label: string, content: string, id: number, scope: string) => {
    memoryEntryMeta.set(path, { id, scope })
    const existing = findTab(path)
    if (existing) {
      if (!existing.isDirty) existing.content = content
    } else {
      tabs.value.push({
        path,
        name: label,
        content,
        isDirty: false,
        type: 'file' // renders via Monaco
      })
    }
    activeFilePath.value = path
    debounceSaveTabs()
  }

  // Open a module editor tab
  const skillWorkspaces = new Map<string, string>() // path -> workspace ns
  const openSkillTab = (path: string, label: string, code: string, workspace: string) => {
    skillWorkspaces.set(path, workspace)
    const existing = findTab(path)
    if (existing) {
      if (!existing.isDirty) existing.content = code
    } else {
      tabs.value.push({
        path,
        name: label,
        content: code,
        isDirty: false,
        type: 'file' // renders via Monaco
      })
    }
    activeFilePath.value = path
    debounceSaveTabs()
  }

  // Open a module detail tab (installed or marketplace)
  // path: module://workspace/slug, module://user/slug, or module://market/slug
  const openModuleTab = (path: string, label: string) => {
    const existing = findTab(path)
    if (!existing) {
      tabs.value.push({
        path,
        name: label,
        content: '',
        isDirty: false,
        type: 'module'
      })
    }
    activeFilePath.value = path
    debounceSaveTabs()
  }

  // Open a diff viewer tab
  const openDiffTab = (path: string, label: string, original: string, modified: string) => {
    const virtualPath = `diff://${path}`
    diffMeta.set(virtualPath, { original, modified })
    const existing = findTab(virtualPath)
    if (existing) {
      existing.content = modified
    } else {
      tabs.value.push({
        path: virtualPath,
        name: `Δ ${label}`,
        content: modified,
        isDirty: false,
        type: 'diff'
      })
    }
    activeFilePath.value = virtualPath
    debounceSaveTabs()
  }

  const getDiffData = (path: string) => diffMeta.get(path) || null

  // Open a read-only virtual tab (e.g., cron job instructions)
  const openVirtualTab = (path: string, label: string, content: string, _lang?: string) => {
    const existing = findTab(path)
    if (existing) {
      existing.content = content
    } else {
      tabs.value.push({
        path,
        name: label,
        content,
        isDirty: false,
        type: 'file' // renders via Monaco
      })
    }
    activeFilePath.value = path
    debounceSaveTabs()
  }

  // Open a secret editor tab
  const secretWorkspaces = new Map<string, string>() // path -> workspace ns
  const openSecretTab = (path: string, label: string, value: string, workspace: string) => {
    secretWorkspaces.set(path, workspace)
    const existing = findTab(path)
    if (existing) {
      if (!existing.isDirty) existing.content = value
    } else {
      tabs.value.push({
        path,
        name: label,
        content: value,
        isDirty: false,
        type: 'file' // renders via Monaco
      })
    }
    activeFilePath.value = path
    debounceSaveTabs()
  }

  const updateTabPath = (oldPath: string, newPath: string, newName?: string) => {
    const tab = findTab(oldPath)
    if (tab) {
      tab.path = newPath
      if (newName) tab.name = newName
      if (activeFilePath.value === oldPath) {
        activeFilePath.value = newPath
      }
      debounceSaveTabs()
    }
  }

  const closeFile = (path: string) => {
    const idx = tabs.value.findIndex(t => t.path === path)
    if (idx !== -1) {
      tabs.value.splice(idx, 1)
      if (activeFilePath.value === path) {
        // Activate the nearest tab (prefer right neighbor, then left)
        const newIdx = Math.min(idx, tabs.value.length - 1)
        activeFilePath.value = newIdx >= 0 ? (tabs.value[newIdx]?.path ?? null) : null
      }
      debounceSaveTabs()
    }
  }

  const closeOthers = (path: string) => {
    tabs.value = tabs.value.filter(t => t.path === path)
    activeFilePath.value = path
    debounceSaveTabs()
  }

  const closeAll = () => {
    tabs.value = []
    activeFilePath.value = null
    debounceSaveTabs()
  }

  const moveTab = (fromIndex: number, toIndex: number) => {
    if (fromIndex === toIndex) return
    if (fromIndex < 0 || fromIndex >= tabs.value.length) return
    if (toIndex < 0 || toIndex >= tabs.value.length) return
    const removed = tabs.value.splice(fromIndex, 1)
    if (removed[0]) tabs.value.splice(toIndex, 0, removed[0])
    debounceSaveTabs()
  }

  const activateTab = (path: string) => {
    if (findTab(path)) {
      activeFilePath.value = path
    }
  }

  const forceTextMode = async (path: string) => {
    const file = findTab(path)
    if (file && (file.type === 'media' || file.type === 'csv')) {
      try {
        if (file.type === 'media') {
          const resp = await fetch('/api/files?path=' + encodeURIComponent(path))
          const data = await resp.json()
          file.content = data.content || ''
        }
        file.type = 'file'
      } catch (e) {
        console.error("Failed to fetch file:", e)
      }
    }
  }

  const updateContent = (path: string, content: string, fromDisk: boolean = false) => {
    const file = findTab(path)
    if (file) {
      file.content = content
      if (!fromDisk) {
        file.isDirty = true
      } else {
        file.isDirty = false
      }
    }
  }

  const saveActiveFile = async () => {
    const file = getActiveFile()
    if (!file || !file.isDirty) return

    // Memory entry tabs save via the memory entry API
    if (file.path.startsWith('memory://entry-')) {
      const meta = memoryEntryMeta.get(file.path)
      if (!meta) return
      try {
        const resp = await fetch('/api/save-memory-entry', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: meta.id, content: file.content, scope: meta.scope })
        })
        if (resp.ok) {
          file.isDirty = false
          useToast().success('Memory entry saved')
        } else {
          useToast().error('Failed to save memory entry')
        }
      } catch (e) {
        useToast().error('Failed to save memory entry')
      }
      return
    }
    // Cron virtual tabs are read-only
    if (file.path.startsWith('memory://cron-')) return

    // Module tabs save via the module API
    if (file.path.startsWith('module://')) {
      const m = file.path.match(/^module:\/\/(ws|user)\/(.+)$/)
      if (!m) return
      const workspace = skillWorkspaces.get(file.path) || ''
      const id = m[2]
      try {
        const resp = await fetch('/api/save-module', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id, code: file.content, workspace })
        })
        if (resp.ok) {
          file.isDirty = false
          useToast().success('Module saved')
        } else {
          useToast().error('Failed to save module')
        }
      } catch (e) {
        useToast().error('Failed to save module')
      }
      return
    }

    // Secret tabs save via the secret API
    if (file.path.startsWith('secret://')) {
      const m = file.path.match(/^secret:\/\/(ws|user)\/(.+)$/)
      if (!m) return
      const workspace = m[1] === 'ws' ? secretWorkspaces.get(file.path) || '' : ''
      const name = m[2]
      try {
        const resp = await fetch('/api/save-secret', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: name, value: file.content, workspace })
        })
        if (resp.ok) {
          file.isDirty = false
          useToast().success('Secret saved')
        } else {
          const text = await resp.text()
          let msg = text
          try { msg = JSON.parse(text).error || JSON.parse(text).message || text } catch {}
          useToast().error(msg)
        }
      } catch (e) {
        useToast().error('Failed to save secret')
      }
      return
    }

    try {
      const resp = await fetch('/api/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: file.path, content: file.content })
      })
      if (resp.ok) {
        file.isDirty = false
        useToast().success('File saved')
      } else {
        useToast().error('Failed to save file')
      }
    } catch (e) {
      useToast().error('Failed to save file')
    }
  }

  // Handle SSE file changes from the backend
  const handleDiskChange = async (path: string) => {
    if (findTab(path)) {
      try {
        const resp = await fetch('/api/files?path=' + encodeURIComponent(path))
        if (resp.ok) {
          const data = await resp.json()
          updateContent(path, data.content || '', true)
        }
      } catch (e) {
        console.error("Failed to sync file change from disk:", e)
      }
    }
  }

  // ── Tab persistence ─────────────────────────────────────────────────
  const debounceSaveTabs = () => {
    if (saveTimer) clearTimeout(saveTimer)
    saveTimer = setTimeout(saveTabs, 1000)
  }

  const saveTabs = async () => {
    try {
      await fetch('/api/patch-open-tabs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          open_tabs: {
            tabs: tabs.value.map(t => ({ path: t.path, name: t.name, type: t.type })),
            active: activeFilePath.value
          }
        })
      })
    } catch { /* ignore */ }
  }

  const loadTabs = async () => {
    try {
      const resp = await fetch('/api/open-tabs')
      if (!resp.ok) return

      const saved = await resp.json()
      if (!saved || !saved.tabs || !Array.isArray(saved.tabs)) return

      for (const t of saved.tabs) {
        if (!t.path || findTab(t.path)) continue
        if (t.type === 'file' || t.type === 'media' || t.type === 'csv') {
          await openFile(t.path, t.name || t.path.split('/').pop() || t.path)
        } else if (t.type === 'chat') {
          const m = t.path.match(/^special:\/\/chat-(\d+)$/)
          if (m) openChatTab(parseInt(m[1], 10), t.name || `Chat #${m[1]}`)
        } else if (t.type === 'module') {
          openModuleTab(t.path, t.name || t.path)
        } else if (t.type && t.type !== 'file' && t.type !== 'diff') {
          openSpecialTab(t.type as any, t.name || t.type)
        }
      }

      if (saved.active && findTab(saved.active)) {
        activeFilePath.value = saved.active
      }
    } catch { /* ignore */ }
  }

  return {
    openFiles: tabs, // alias for backward compat
    activeFilePath,
    getTabs,
    getActiveFile,
    openFile,
    openSpecialTab,
    openChatTab,
    openMemoryEntryTab,
    openSkillTab,
    openModuleTab,
    openSecretTab,
    openVirtualTab,
    openDiffTab,
    getDiffData,
    activateTab,
    forceTextMode,
    closeFile,
    closeOthers,
    closeAll,
    moveTab,
    updateTabPath,
    updateContent,
    saveActiveFile,
    handleDiskChange,
    saveTabs,
    loadTabs
  }
})
