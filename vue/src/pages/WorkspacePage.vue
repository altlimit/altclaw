<script setup lang="ts">
import { ref, onMounted, nextTick } from 'vue'
import { useEditorStore } from '@/stores/editor'
import { useWorkspaceStore } from '@/stores/workspace'
import FileTreeItem from '@/components/FileTreeItem.vue'

interface FileEntry {
  name: string
  is_dir: boolean
  size?: number
}

const editorStore = useEditorStore()
const wsStore = useWorkspaceStore()
const entries = ref<FileEntry[]>([])
const selectedItems = ref<Set<string>>(new Set())
const rootDragOver = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

async function loadRoot() {
  try {
    const resp = await fetch('/api/files?path=')
    const data = await resp.json()
    const list = data.entries || []
    list.sort((a: FileEntry, b: FileEntry) => {
      if (a.is_dir && !b.is_dir) return -1
      if (!a.is_dir && b.is_dir) return 1
      return a.name.localeCompare(b.name)
    })
    entries.value = list
  } catch {
    entries.value = []
  }
}

function handleFileOpen(path: string, name: string) {
  editorStore.openFile(path, name)
}

function handleToggleSelect(path: string, multi: boolean) {
  const s = new Set(multi ? selectedItems.value : [])
  if (s.has(path)) {
    s.delete(path)
  } else {
    s.add(path)
  }
  selectedItems.value = s
}

function clearSelection() {
  selectedItems.value = new Set()
}

// --- Create file/folder ---
const showCreateInput = ref(false)
const createType = ref<'file' | 'folder'>('file')
const createName = ref('')
const createInput = ref<HTMLInputElement | null>(null)

function startCreate(type: 'file' | 'folder') {
  createType.value = type
  createName.value = ''
  showCreateInput.value = true
  nextTick(() => createInput.value?.focus())
}

async function confirmCreate() {
  const name = createName.value.trim()
  if (!name) {
    showCreateInput.value = false
    return
  }
  try {
    if (createType.value === 'file') {
      await fetch('/api/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: name, content: '' })
      })
      editorStore.openFile(name, name.split('/').pop() || name)
    } else {
      await fetch('/api/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: name + '/.gitkeep', content: '' })
      })
    }
  } catch (e) {
    console.error('Create failed:', e)
  }
  showCreateInput.value = false
  loadRoot()
}

function cancelCreate() {
  showCreateInput.value = false
}

// --- Upload ---
function triggerUpload() {
  fileInput.value?.click()
}

async function handleUpload(e: Event) {
  const input = e.target as HTMLInputElement
  if (!input.files?.length) return
  const form = new FormData()
  form.append('path', '')
  for (const file of input.files) {
    form.append('files', file)
  }
  try {
    await fetch('/api/upload', { method: 'POST', body: form })
  } catch (err) {
    console.error('Upload failed:', err)
  }
  input.value = ''
  loadRoot()
}

// --- Root drop zone ---
function onRootDragOver(e: DragEvent) {
  if (!e.dataTransfer) return
  const types = Array.from(e.dataTransfer.types)
  if (types.includes('application/x-workspace-move') || types.includes('Files')) {
    e.preventDefault()
    e.dataTransfer.dropEffect = types.includes('Files') ? 'copy' : 'move'
    rootDragOver.value = true
  }
}

function onRootDragLeave() {
  rootDragOver.value = false
}

async function onRootDrop(e: DragEvent) {
  rootDragOver.value = false
  if (!e.dataTransfer) return
  e.preventDefault()

  // Handle workspace move to root
  const moveData = e.dataTransfer.getData('application/x-workspace-move')
  if (moveData) {
    const paths: string[] = JSON.parse(moveData)
    for (const oldPath of paths) {
      const name = oldPath.split('/').pop()
      if (!name || !oldPath.includes('/')) continue // Already at root
      try {
        await fetch('/api/rename-file', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ old_path: oldPath, new_path: name })
        })
        editorStore.updateTabPath(oldPath, name, name)
      } catch (err) {
        console.error('Move failed:', err)
      }
    }
    loadRoot()
    return
  }

  // Handle native file upload to root
  if (e.dataTransfer.files.length > 0) {
    const form = new FormData()
    form.append('path', '')
    for (const file of e.dataTransfer.files) {
      form.append('files', file)
    }
    try {
      await fetch('/api/upload', { method: 'POST', body: form })
    } catch (err) {
      console.error('Upload failed:', err)
    }
    loadRoot()
  }
}

// Refresh the tree when files are created/deleted
function refreshTree(changedPath: string) {
  loadRoot()
  window.dispatchEvent(new CustomEvent('workspace-refresh', { detail: changedPath }))
}

defineExpose({ refreshTree })

onMounted(() => loadRoot())
</script>

<template>
  <div class="explorer" @click.self="clearSelection">
    <div class="explorer-header">
      <span>EXPLORER</span>
      <div class="header-actions">
        <button class="header-btn" @click="loadRoot" title="Refresh">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
        </button>
        <button class="header-btn" @click="triggerUpload" title="Upload Files">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
        </button>
        <button class="header-btn" @click="startCreate('file')" title="New File">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="12" y1="18" x2="12" y2="12"/><line x1="9" y1="15" x2="15" y2="15"/></svg>
        </button>
        <button class="header-btn" @click="startCreate('folder')" title="New Folder">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><line x1="12" y1="11" x2="12" y2="17"/><line x1="9" y1="14" x2="15" y2="14"/></svg>
        </button>
      </div>
    </div>
    <input ref="fileInput" type="file" multiple style="display:none" @change="handleUpload" />
    <div v-if="showCreateInput" class="create-input-row">
      <span class="create-icon">{{ createType === 'folder' ? '📁' : '📄' }}</span>
      <input
        ref="createInput"
        v-model="createName"
        class="create-input"
        :placeholder="createType === 'folder' ? 'folder name' : 'filename.ext'"
        @keyup.enter="confirmCreate"
        @keyup.escape="cancelCreate"
        @blur="confirmCreate"
      />
    </div>
    <div
      class="file-list"
      :class="{ 'root-drag-over': rootDragOver }"
      @dragover="onRootDragOver"
      @dragleave="onRootDragLeave"
      @drop="onRootDrop"
      @click.self="clearSelection"
    >
      <FileTreeItem 
        v-for="item in entries"
        :key="item.name"
        :item="item"
        basePath=""
        :selected="selectedItems"
        :publicDir="wsStore.publicDir"
        @open-file="handleFileOpen"
        @refresh="loadRoot"
        @toggle-select="handleToggleSelect"
      />
      <div v-if="!entries.length && !showCreateInput" class="empty">
        Drop files here or click 📤 to upload
      </div>
    </div>
  </div>
</template>

<style scoped>
.explorer {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.explorer-header {
  font-size: 11px;
  font-weight: 600;
  padding: 10px 16px;
  color: var(--text-muted);
  letter-spacing: 0.5px;
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.header-actions {
  display: flex;
  gap: 2px;
}
.header-btn {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 4px;
  display: flex;
  align-items: center;
}
.header-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
.create-input-row {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px 4px 20px;
}
.create-icon {
  font-size: 13px;
  opacity: 0.8;
}
.create-input {
  flex: 1;
  background: var(--bg-secondary);
  border: 1px solid var(--accent);
  color: var(--text-primary);
  font-size: 13px;
  padding: 2px 6px;
  border-radius: 3px;
  outline: none;
}
.file-list {
  flex: 1;
  overflow-y: auto;
  padding: 0 8px 8px 8px;
  transition: background 0.15s;
}
.file-list.root-drag-over {
  background: rgba(166, 227, 161, 0.08);
}
.empty {
  padding: 16px;
  color: var(--text-muted);
  font-size: 13px;
  font-style: italic;
  text-align: center;
}
</style>
