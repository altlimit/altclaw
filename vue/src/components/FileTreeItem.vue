<script setup lang="ts">
import { ref, nextTick, computed, onMounted, onUnmounted } from 'vue'
import { useEditorStore } from '@/stores/editor'

interface FileEntry {
  name: string
  is_dir: boolean
  size?: number
}

const props = defineProps<{
  item: FileEntry
  basePath: string
  selected: Set<string>
  publicDir?: string
}>()

const editorStore = useEditorStore()

const emit = defineEmits<{
  (e: 'open-file', path: string, name: string): void
  (e: 'refresh'): void
  (e: 'toggle-select', path: string, multi: boolean): void
}>()

const isOpen = ref(false)
const children = ref<FileEntry[]>([])
const loading = ref(false)
const dragOver = ref(false)
const isDragging = ref(false)
let dragStartPos = { x: 0, y: 0 }

// Context menu
const showMenu = ref(false)
const menuX = ref(0)
const menuY = ref(0)

// Inline rename
const isRenaming = ref(false)
const renameValue = ref('')
const renameInput = ref<HTMLInputElement | null>(null)

// Install as module dialog
const showInstallDialog = ref(false)
const installScope = ref<'workspace' | 'user'>('workspace')
const installStatus = ref('')
const installError = ref('')

function fullPath() {
  return props.basePath ? props.basePath + '/' + props.item.name : props.item.name
}

function formatSize(bytes?: number): string {
  if (!bytes) return ''
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / 1024 / 1024).toFixed(1) + ' MB'
}

function hoverTitle(): string {
  const parts = [fullPath()]
  if (!props.item.is_dir && props.item.size) {
    parts.push(formatSize(props.item.size))
  }
  return parts.join(' — ')
}

const isSelected = computed(() => props.selected.has(fullPath()))

function onClick(e: MouseEvent) {
  if (isRenaming.value) return
  // Ctrl/Cmd click = toggle selection
  if (e.ctrlKey || e.metaKey) {
    emit('toggle-select', fullPath(), true)
    return
  }

  if (!props.item.is_dir) {
    emit('open-file', fullPath(), props.item.name)
    return
  }

  isOpen.value = !isOpen.value
  if (isOpen.value && children.value.length === 0) {
    loadChildren()
  }
}

function onMouseDown(e: MouseEvent) {
  if (e.button !== 0) return
  isDragging.value = false
  dragStartPos = { x: e.clientX, y: e.clientY }
  document.addEventListener('mousemove', onMouseMoveDrag)
  document.addEventListener('mouseup', onMouseUpDrag, { once: true })
}

function onMouseMoveDrag(e: MouseEvent) {
  const dx = Math.abs(e.clientX - dragStartPos.x)
  const dy = Math.abs(e.clientY - dragStartPos.y)
  if (dx > 4 || dy > 4) {
    isDragging.value = true
    document.removeEventListener('mousemove', onMouseMoveDrag)
  }
}

function onMouseUpDrag() {
  document.removeEventListener('mousemove', onMouseMoveDrag)
  isDragging.value = false
}

async function loadChildren() {
  loading.value = true
  try {
    const resp = await fetch('/api/files?path=' + encodeURIComponent(fullPath()))
    const data = await resp.json()
    const entries = data.entries || []
    entries.sort((a: FileEntry, b: FileEntry) => {
      if (a.is_dir && !b.is_dir) return -1
      if (!a.is_dir && b.is_dir) return 1
      return a.name.localeCompare(b.name)
    })
    children.value = entries
  } catch {
    children.value = []
  } finally {
    loading.value = false
  }
}

function handleChildOpen(path: string, name: string) {
  emit('open-file', path, name)
}

function onContextMenu(e: MouseEvent) {
  e.preventDefault()
  e.stopPropagation()
  // If right-clicking an unselected item, select it (replacing others)
  if (!isSelected.value) {
    emit('toggle-select', fullPath(), false)
  }
  menuX.value = e.clientX
  menuY.value = e.clientY
  showMenu.value = true
  setTimeout(() => {
    document.addEventListener('click', closeMenu, { once: true })
  }, 0)
}

function closeMenu() {
  showMenu.value = false
}

function startRename() {
  closeMenu()
  renameValue.value = props.item.name
  isRenaming.value = true
  nextTick(() => {
    if (renameInput.value) {
      renameInput.value.focus()
      if (!props.item.is_dir) {
        const dotIdx = props.item.name.lastIndexOf('.')
        if (dotIdx > 0) {
          renameInput.value.setSelectionRange(0, dotIdx)
        } else {
          renameInput.value.select()
        }
      } else {
        renameInput.value.select()
      }
    }
  })
}

async function confirmRename() {
  const newName = renameValue.value.trim()
  isRenaming.value = false
  if (!newName || newName === props.item.name) return

  const oldPath = fullPath()
  const newPath = props.basePath ? props.basePath + '/' + newName : newName

  try {
    await fetch('/api/rename-file', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ old_path: oldPath, new_path: newPath })
    })
    editorStore.updateTabPath(oldPath, newPath, newName)
    emit('refresh')
  } catch (e) {
    console.error('Rename failed:', e)
  }
}

function cancelRename() {
  isRenaming.value = false
}

async function deleteSelected() {
  closeMenu()
  const paths = props.selected.size > 0 ? Array.from(props.selected) : [fullPath()]
  const count = paths.length
  const label = count === 1 ? `"${paths[0]!.split('/').pop() ?? paths[0]}"` : `${count} items`
  if (!confirm(`Delete ${label}?`)) return

  for (const p of paths) {
    try {
      await fetch('/api/delete-file', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: p })
      })
    } catch (e) {
      console.error('Delete failed:', e)
    }
  }
  emit('refresh')
}

function attachToChat() {
  closeMenu()
  const paths = props.selected.size > 0
    ? Array.from(props.selected).filter(p => !p.endsWith('/'))
    : [fullPath()]
  for (const p of paths) {
    window.dispatchEvent(new CustomEvent('attach-file-to-chat', { detail: p }))
  }
}

function handleChildRefresh() {
  if (isOpen.value) loadChildren()
  emit('refresh')
}

function handleChildSelect(path: string, multi: boolean) {
  emit('toggle-select', path, multi)
}

function openInstallDialog() {
  closeMenu()
  installScope.value = 'workspace'
  installStatus.value = ''
  installError.value = ''
  showInstallDialog.value = true
}

async function confirmInstall() {
  installStatus.value = 'Installing...'
  installError.value = ''
  try {
    const resp = await fetch('/api/install-folder-as-module', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        path: fullPath(),
        scope: installScope.value,
      })
    })
    const data = await resp.json()
    if (!resp.ok) {
      installError.value = data.error || 'Install failed'
      installStatus.value = ''
    } else {
      installStatus.value = `✓ Installed as "${data.id}"`
    }
  } catch (e: any) {
    installError.value = String(e)
    installStatus.value = ''
  }
}

function closeInstallDialog() {
  showInstallDialog.value = false
}

// --- Drag & Drop ---

function onDragStart(e: DragEvent) {
  if (!isDragging.value || !e.dataTransfer) {
    e.preventDefault()
    return
  }
  const fp = fullPath()
  // If dragging a selected item, drag all selected
  const paths = isSelected.value && props.selected.size > 1
    ? Array.from(props.selected)
    : [fp]
  e.dataTransfer.setData('application/x-workspace-move', JSON.stringify(paths))
  // Also set file path for chat drop
  if (!props.item.is_dir) {
    e.dataTransfer.setData('application/x-workspace-file', fp)
  }
  e.dataTransfer.effectAllowed = 'move'
}

function onDragOver(e: DragEvent) {
  if (!props.item.is_dir || !e.dataTransfer) return
  // Accept workspace moves and native file uploads
  const types = Array.from(e.dataTransfer.types)
  if (types.includes('application/x-workspace-move') || types.includes('Files')) {
    e.preventDefault()
    e.stopPropagation()
    e.dataTransfer.dropEffect = types.includes('Files') ? 'copy' : 'move'
    dragOver.value = true
  }
}

function onDragLeave(e: DragEvent) {
  dragOver.value = false
}

async function onDrop(e: DragEvent) {
  dragOver.value = false
  if (!props.item.is_dir || !e.dataTransfer) return
  e.preventDefault()
  e.stopPropagation()

  const targetDir = fullPath()

  // Handle workspace move
  const moveData = e.dataTransfer.getData('application/x-workspace-move')
  if (moveData) {
    const paths: string[] = JSON.parse(moveData)
    for (const oldPath of paths) {
      const name = oldPath.split('/').pop()
      const newPath = targetDir + '/' + name
      if (oldPath === newPath || oldPath === targetDir) continue
      try {
        await fetch('/api/rename-file', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ old_path: oldPath, new_path: newPath })
        })
        editorStore.updateTabPath(oldPath, newPath, name)
      } catch (err) {
        console.error('Move failed:', err)
      }
    }
    emit('refresh')
    return
  }

  // Handle native file upload
  if (e.dataTransfer.files.length > 0) {
    const form = new FormData()
    form.append('path', targetDir)
    for (const file of e.dataTransfer.files) {
      form.append('files', file)
    }
    try {
      await fetch('/api/upload', { method: 'POST', body: form })
    } catch (err) {
      console.error('Upload failed:', err)
    }
    // Auto-open the folder to show uploaded files
    if (!isOpen.value) {
      isOpen.value = true
    }
    loadChildren()
    emit('refresh')
  }
}

function handleGlobalRefresh(e: Event) {
  const evt = e as CustomEvent
  // If we wanted we could check evt.detail (the path) to optimize, but this is fine
  if (props.item.is_dir && isOpen.value) {
    loadChildren()
  }
}

onMounted(() => {
  window.addEventListener('workspace-refresh', handleGlobalRefresh)
})

onUnmounted(() => {
  window.removeEventListener('workspace-refresh', handleGlobalRefresh)
})
</script>

<template>
  <div class="tree-node">
    <div
      class="tree-item"
      :class="{ 'is-dir': item.is_dir, 'is-selected': isSelected, 'drag-over': dragOver }"
      :title="hoverTitle()"
      @click="onClick"
      @mousedown="onMouseDown"
      @contextmenu="onContextMenu"
      draggable="true"
      @dragstart="onDragStart"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <span class="chevron" :class="{ open: isOpen }" v-if="item.is_dir">▸</span>
      <span class="spacer" v-else></span>
      <span class="icon">{{ item.is_dir ? (fullPath() === publicDir ? '🌐' : '📁') : '📄' }}</span>
      <template v-if="isRenaming">
        <input
          ref="renameInput"
          v-model="renameValue"
          class="rename-input"
          @keyup.enter="confirmRename"
          @keyup.escape="cancelRename"
          @blur="confirmRename"
          @click.stop
        />
      </template>
      <span v-else class="name">{{ item.name }}</span>
    </div>

    <!-- Context menu -->
    <Teleport to="body">
      <div v-if="showMenu" class="ctx-menu" :style="{ left: menuX + 'px', top: menuY + 'px' }">
        <div class="ctx-item" @click="startRename">Rename</div>
        <div class="ctx-item" @click="attachToChat">Attach to Chat</div>
        <template v-if="item.is_dir || item.name.endsWith('.js')">
          <div class="ctx-sep"></div>
          <div class="ctx-item ctx-module" @click="openInstallDialog">📦 Install as Module</div>
        </template>
        <div class="ctx-sep"></div>
        <div class="ctx-item ctx-danger" @click="deleteSelected">
          Delete{{ selected.size > 1 ? ` (${selected.size})` : '' }}
        </div>
      </div>
    </Teleport>

    <!-- Install as Module dialog -->
    <Teleport to="body">
      <div v-if="showInstallDialog" class="install-overlay" @click.self="closeInstallDialog">
        <div class="install-dialog">
          <div class="install-title">📦 Install as Module</div>
          <div class="install-path">{{ fullPath() }}</div>
          <div class="install-hint">Module slug: <code>{{ item.name }}</code></div>

          <label class="install-label">Scope</label>
          <div class="install-scope-row">
            <label class="install-scope-opt" :class="{ active: installScope === 'workspace' }">
              <input type="radio" v-model="installScope" value="workspace" /> Workspace
            </label>
            <label class="install-scope-opt" :class="{ active: installScope === 'user' }">
              <input type="radio" v-model="installScope" value="user" /> User (global)
            </label>
          </div>

          <div v-if="installStatus" class="install-status">{{ installStatus }}</div>
          <div v-if="installError" class="install-error">{{ installError }}</div>

          <div class="install-actions">
            <button class="install-btn-cancel" @click="closeInstallDialog">Cancel</button>
            <button class="install-btn-ok" @click="confirmInstall">Install</button>
          </div>
        </div>
      </div>
    </Teleport>

    <div class="children" v-if="item.is_dir && isOpen">
      <div v-if="loading" class="loading">Loading...</div>
      <FileTreeItem 
        v-for="child in children" 
        :key="child.name" 
        :item="child"
        :basePath="basePath ? basePath + '/' + item.name : item.name"
        :selected="selected"
        :publicDir="publicDir"
        @open-file="handleChildOpen"
        @refresh="handleChildRefresh"
        @toggle-select="handleChildSelect"
      />
    </div>
  </div>
</template>

<style scoped>
.tree-node {
  display: flex;
  flex-direction: column;
}
.tree-item {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 0;
  cursor: pointer;
  user-select: none;
  font-size: 13px;
  color: var(--text-secondary);
  border-radius: 3px;
  transition: background 0.1s;
}
.tree-item:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
.tree-item.is-selected {
  background: rgba(137, 180, 250, 0.15);
  color: var(--text-primary);
}
.tree-item.drag-over {
  background: rgba(166, 227, 161, 0.2);
  outline: 1px dashed var(--accent);
  outline-offset: -1px;
}
.chevron {
  display: inline-block;
  width: 12px;
  text-align: center;
  font-size: 11px;
  transition: transform 0.1s;
}
.chevron.open {
  transform: rotate(90deg);
}
.spacer {
  width: 12px;
}
.icon {
  font-size: 13px;
  opacity: 0.8;
}
.name {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.rename-input {
  flex: 1;
  min-width: 0;
  background: var(--bg-secondary);
  border: 1px solid var(--accent);
  color: var(--text-primary);
  font-size: 13px;
  padding: 1px 4px;
  border-radius: 3px;
  outline: none;
}
.children {
  padding-left: 12px;
  border-left: 1px solid var(--border);
  margin-left: 5px;
}
.loading {
  padding: 4px 16px;
  font-size: 12px;
  color: var(--text-muted);
  font-style: italic;
}
</style>

<style>
/* Context menu - not scoped so Teleport works */
.ctx-menu {
  position: fixed;
  z-index: 9999;
  background: var(--bg-secondary, #1e1e2e);
  border: 1px solid var(--border, #333);
  border-radius: 6px;
  padding: 4px 0;
  min-width: 140px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.4);
}
.ctx-item {
  padding: 6px 14px;
  font-size: 13px;
  cursor: pointer;
  color: var(--text-primary, #cdd6f4);
}
.ctx-item:hover {
  background: var(--bg-hover, #313244);
}
.ctx-item.ctx-danger:hover {
  background: #e64553;
  color: #fff;
}
.ctx-item.ctx-module:hover {
  background: rgba(99, 102, 241, 0.15);
  color: var(--accent, #89b4fa);
}
.ctx-sep {
  height: 1px;
  background: var(--border, #333);
  margin: 4px 8px;
}

/* Install as Module dialog */
.install-overlay {
  position: fixed;
  inset: 0;
  z-index: 10000;
  background: rgba(0,0,0,0.5);
  display: flex;
  align-items: center;
  justify-content: center;
}
.install-dialog {
  background: var(--bg-secondary, #1e1e2e);
  border: 1px solid var(--border, #333);
  border-radius: 10px;
  padding: 20px 24px;
  min-width: 320px;
  max-width: 440px;
  box-shadow: 0 8px 32px rgba(0,0,0,0.5);
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.install-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}
.install-path {
  font-size: 11px;
  color: var(--text-muted);
  font-family: monospace;
  word-break: break-all;
}
.install-hint {
  font-size: 12px;
  color: var(--text-secondary);
}
.install-hint code {
  background: var(--bg-tertiary, #181825);
  padding: 1px 5px;
  border-radius: 3px;
  font-family: monospace;
  color: var(--accent-light, #89b4fa);
}
.install-label {
  font-size: 12px;
  color: var(--text-secondary);
  font-weight: 500;
}
.install-input {
  background: var(--bg-primary, #181825);
  border: 1px solid var(--border, #333);
  border-radius: 6px;
  color: var(--text-primary);
  font-size: 13px;
  padding: 6px 10px;
  outline: none;
  width: 100%;
  box-sizing: border-box;
  transition: border-color 0.15s;
}
.install-input:focus { border-color: var(--accent, #89b4fa); }
.install-scope-row {
  display: flex;
  gap: 16px;
}
.install-scope-opt {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--text-secondary);
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  border: 1px solid transparent;
  transition: all 0.1s;
}
.install-scope-opt.active {
  border-color: var(--accent, #89b4fa);
  color: var(--text-primary);
  background: rgba(99,102,241,0.08);
}
.install-scope-opt input { display: none; }
.install-status {
  font-size: 12px;
  color: #a6e3a1;
}
.install-error {
  font-size: 12px;
  color: #f38ba8;
}
.install-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}
.install-btn-cancel {
  background: transparent;
  border: 1px solid var(--border, #333);
  color: var(--text-secondary);
  padding: 5px 14px;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
}
.install-btn-ok {
  background: var(--accent, #89b4fa);
  border: none;
  color: #1e1e2e;
  padding: 5px 14px;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.1s;
}
.install-btn-ok:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
