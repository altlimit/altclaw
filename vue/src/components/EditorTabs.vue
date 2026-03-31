<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import type { OpenFile } from '@/stores/editor'

const props = defineProps<{
  tabs: OpenFile[]
  activeTab: string
  publicDir: string
  tunnelUrl: string
}>()

function publicRoute(path: string): string | null {
  if (!props.publicDir || !path || path.startsWith('special://')) return null
  const prefix = props.publicDir + '/'
  if (!path.startsWith(prefix)) return null
  let route = path.slice(prefix.length)
  if (route.endsWith('.server.js')) {
    route = route.replace(/\.server\.js$/, '')
  }
  if (props.tunnelUrl) {
    const proto = globalThis.location?.protocol || 'https:'
    return `${proto}//${props.tunnelUrl}/${route}`
  }
  return '/' + route
}

const emit = defineEmits<{
  (e: 'select', tab: string): void
  (e: 'close', tab: string): void
  (e: 'closeOthers', tab: string): void
  (e: 'closeAll'): void
  (e: 'reorder', from: number, to: number): void
  (e: 'run-js', path: string): void
  (e: 'open-sub-agents', chatId: number): void
}>()

function isScript(path: string): boolean {
  return !path.startsWith('special://') && path.endsWith('.js')
}

function chatIdFromTab(tab: { path: string; type: string }): number | null {
  if (tab.type !== 'chat') return null
  const m = tab.path.match(/^special:\/\/chat-(\d+)$/)
  return m?.[1] ? parseInt(m[1], 10) : null
}

// ── Context Menu ──────────────────────────────────────────────────
const contextMenu = ref<{ x: number; y: number; path: string } | null>(null)

function showContextMenu(e: MouseEvent, path: string) {
  e.preventDefault()
  contextMenu.value = { x: e.clientX, y: e.clientY, path }
}

function closeContextMenu() {
  contextMenu.value = null
}

function ctxClose() {
  if (contextMenu.value) emit('close', contextMenu.value.path)
  closeContextMenu()
}
function ctxCloseOthers() {
  if (contextMenu.value) emit('closeOthers', contextMenu.value.path)
  closeContextMenu()
}
function ctxCloseAll() {
  emit('closeAll')
  closeContextMenu()
}

const tabsContainerRef = ref<HTMLElement | null>(null)
const isOverflowing = ref(false)
const isMobile = ref(false)
const dropdownOpen = ref(false)
const hiddenTabs = ref<OpenFile[]>([])

function toggleDropdown() {
  if (dropdownOpen.value) {
    dropdownOpen.value = false
    return
  }
  
  if (tabsContainerRef.value) {
    const container = tabsContainerRef.value
    const containerRect = container.getBoundingClientRect()
    const children = Array.from(container.children) as HTMLElement[]
    
    hiddenTabs.value = props.tabs.filter((tab, i) => {
      const el = children[i]
      if (!el) return true
      const rect = el.getBoundingClientRect()
      // Use 1px tolerance
      return Math.round(rect.left) < Math.round(containerRect.left) - 1 || Math.round(rect.right) > Math.round(containerRect.right) + 1
    })
  } else {
    hiddenTabs.value = props.tabs
  }
  
  dropdownOpen.value = true
}

function checkOverflow() {
  isMobile.value = window.innerWidth <= 768
  if (tabsContainerRef.value) {
    const el = tabsContainerRef.value
    // Provide a small 1-2px epsilon
    isOverflowing.value = el.scrollWidth > el.clientWidth + 2
  }
}

let ro: ResizeObserver | null = null

function onDocClick(e?: MouseEvent) {
  closeContextMenu()
  if (e) {
    const target = e.target as HTMLElement
    if (target && target.closest && !target.closest('.tabs-mobile-menu')) {
      dropdownOpen.value = false
    }
  }
}

onMounted(() => {
  document.addEventListener('click', onDocClick as any)
  checkOverflow()
  window.addEventListener('resize', checkOverflow)
  if (tabsContainerRef.value) {
    ro = new ResizeObserver(checkOverflow)
    ro.observe(tabsContainerRef.value)
  }
})

onUnmounted(() => {
  document.removeEventListener('click', onDocClick as any)
  window.removeEventListener('resize', checkOverflow)
  if (ro) ro.disconnect()
})

watch(() => props.tabs, () => {
  nextTick(checkOverflow)
}, { deep: true })

// ── Drag & Drop ──────────────────────────────────────────────────
const dragIndex = ref(-1)
const dropTarget = ref(-1)

function onDragStart(e: DragEvent, index: number) {
  dragIndex.value = index
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(index))
  }
}
function onDragOver(e: DragEvent, index: number) {
  e.preventDefault()
  dropTarget.value = index
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
}
function onDragLeave() {
  dropTarget.value = -1
}
function onDrop(e: DragEvent, toIndex: number) {
  e.preventDefault()
  if (dragIndex.value >= 0 && dragIndex.value !== toIndex) {
    emit('reorder', dragIndex.value, toIndex)
  }
  dragIndex.value = -1
  dropTarget.value = -1
}
function onDragEnd() {
  dragIndex.value = -1
  dropTarget.value = -1
}

</script>

<template>
  <div class="editor-tabs-root" v-if="tabs.length > 0">
    <div class="tabs-container" ref="tabsContainerRef" @scroll="dropdownOpen = false">
      <div 
        v-for="(tab, i) in tabs" 
        :key="tab.path"
        class="tab"
        :class="{ 
          active: tab.path === activeTab,
          dragging: dragIndex === i,
          'drop-before': dropTarget === i && dragIndex > i,
          'drop-after': dropTarget === i && dragIndex < i,
          'is-dirty': tab.isDirty
        }"
        @click="emit('select', tab.path)"
        @contextmenu="showContextMenu($event, tab.path)"
        draggable="true"
        @dragstart="onDragStart($event, i)"
        @dragover="onDragOver($event, i)"
        @dragleave="onDragLeave"
        @drop="onDrop($event, i)"
        @dragend="onDragEnd"
      >
        <span class="tab-name">{{ tab.name }}</span>
        <a
          v-if="tab.path === activeTab && publicRoute(tab.path)"
          class="public-route-link"
          :href="publicRoute(tab.path)!"
          target="_blank"
          @click.stop
          title="Open in browser"
        >🌐</a>
        <button
          v-if="tab.path === activeTab && isScript(tab.path)"
          class="run-btn"
          @click.stop="emit('run-js', tab.path)"
          title="Run script"
        >▶</button>
        <button
          v-if="tab.path === activeTab && chatIdFromTab(tab)"
          class="sub-agents-btn"
          @click.stop="emit('open-sub-agents', chatIdFromTab(tab)!)"
          title="Sub-Agent History"
        >🤖</button>
        <div class="tab-actions">
          <div class="dirty-dot"></div>
          <button class="close-btn" @click.stop="emit('close', tab.path)">×</button>
        </div>
      </div>
    </div>

    <div class="tabs-mobile-menu" v-if="isOverflowing">
      <button class="tabs-menu-btn" @click.stop="toggleDropdown" title="Open Tabs">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/></svg>
      </button>
      <transition name="dropdown-slide">
        <div class="tabs-dropdown" v-if="dropdownOpen">
          <div 
            v-for="tab in hiddenTabs" 
            :key="'dd-' + tab.path"
            class="tabs-dropdown-item"
            :class="{ active: tab.path === activeTab }"
            @click="emit('select', tab.path); dropdownOpen = false"
          >
            <span class="tabs-dropdown-name">{{ tab.name }}</span>
            <div class="dirty-dot" v-if="tab.isDirty"></div>
            <button class="tabs-dropdown-close" @click.stop="emit('close', tab.path)">×</button>
          </div>
        </div>
      </transition>
    </div>
  </div>
  <div v-else class="tabs-empty"></div>

  <!-- Context Menu -->
  <Teleport to="body">
    <div
      v-if="contextMenu"
      class="ctx-menu"
      :style="{ left: contextMenu.x + 'px', top: contextMenu.y + 'px' }"
      @click.stop
    >
      <button @click="ctxClose">Close</button>
      <button @click="ctxCloseOthers">Close Others</button>
      <button @click="ctxCloseAll">Close All</button>
    </div>
  </Teleport>
</template>

<style scoped>
.editor-tabs-root {
  display: flex;
  width: 100%;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
}
.tabs-container {
  display: flex;
  flex: 1;
  overflow-x: auto;
  scrollbar-width: none;
}
.tabs-container::-webkit-scrollbar {
  display: none;
}
.tabs-empty {
  height: 35px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
}
.tab {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 12px;
  height: 35px;
  background: transparent;
  border-right: 1px solid var(--border);
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
  min-width: 120px;
  border-top: 2px solid transparent;
  user-select: none;
  transition: opacity 0.15s;
}
.tab:hover {
  background: var(--bg-primary);
}
.tab.active {
  background: var(--bg-primary);
  color: var(--text-primary);
  border-top-color: var(--accent);
}
.tab.dragging {
  opacity: 0.4;
}
.tab.drop-before {
  box-shadow: inset 2px 0 0 var(--accent);
}
.tab.drop-after {
  box-shadow: inset -2px 0 0 var(--accent);
}
.tab-name {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tab-actions {
  position: relative;
  width: 18px;
  height: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-left: 4px;
}
.dirty-dot {
  display: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-primary);
  opacity: 0.8;
}
.tab.is-dirty .dirty-dot {
  display: block;
}
.close-btn {
  position: absolute;
  top: 0; left: 0;
  width: 100%; height: 100%;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 16px;
  line-height: 1;
  border-radius: 4px;
  cursor: pointer;
  visibility: hidden;
}
.tab:hover .close-btn, .tab.active .close-btn {
  visibility: visible;
}
.close-btn:hover {
  background: var(--bg-tertiary);
  color: var(--text-primary);
}
.tab.is-dirty .close-btn {
  visibility: hidden;
}
.tab.is-dirty:hover .close-btn {
  visibility: visible;
}
.tab.is-dirty:hover .dirty-dot {
  display: none;
}
.public-route-link {
  font-size: 12px;
  color: var(--accent);
  text-decoration: none;
  opacity: 0.7;
  transition: opacity 0.15s;
  flex-shrink: 0;
}
.public-route-link:hover {
  opacity: 1;
}
.run-btn {
  font-size: 11px;
  color: #a6e3a1;
  background: transparent;
  border: none;
  padding: 0 2px;
  cursor: pointer;
  opacity: 0.7;
  flex-shrink: 0;
  transition: opacity 0.15s, transform 0.1s;
  line-height: 1;
}
.run-btn:hover {
  opacity: 1;
  transform: scale(1.2);
}
.sub-agents-btn {
  font-size: 12px;
  background: transparent;
  border: none;
  padding: 0 2px;
  cursor: pointer;
  opacity: 0.7;
  flex-shrink: 0;
  transition: opacity 0.15s, transform 0.1s;
  line-height: 1;
}
.sub-agents-btn:hover {
  opacity: 1;
  transform: scale(1.2);
}

/* Context Menu */
.ctx-menu {
  position: fixed;
  z-index: 9999;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0,0,0,0.3);
  padding: 4px 0;
  min-width: 160px;
}
.ctx-menu button {
  display: block;
  width: 100%;
  padding: 6px 16px;
  background: transparent;
  border: none;
  color: var(--text-primary);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}
.ctx-menu button:hover {
  background: var(--accent);
  color: #fff;
}

/* Mobile Tabs Dropdown */
.tabs-mobile-menu {
  position: relative;
  display: flex;
  align-items: center;
  border-left: 1px solid var(--border);
  background: var(--bg-secondary);
  z-index: 5;
}
.tabs-menu-btn {
  background: transparent;
  border: none;
  width: 35px;
  height: 35px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-muted);
  cursor: pointer;
}
.tabs-menu-btn:hover {
  color: var(--text-primary);
}
.tabs-dropdown {
  position: absolute;
  top: 100%;
  right: 0;
  width: 200px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 0 0 4px 4px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.2);
  max-height: 50vh;
  overflow-y: auto;
  z-index: 100;
}
.tabs-dropdown-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  cursor: pointer;
  color: var(--text-muted);
  border-bottom: 1px solid var(--border);
}
.tabs-dropdown-item:last-child {
  border-bottom: none;
}
.tabs-dropdown-item:hover {
  background: var(--bg-tertiary);
}
.tabs-dropdown-item.active {
  color: var(--text-primary);
  background: var(--bg-primary);
  border-left: 2px solid var(--accent);
}
.tabs-dropdown-name {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 13px;
}
.tabs-dropdown-close {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 16px;
  line-height: 1;
  padding: 0 4px;
  border-radius: 4px;
}
.tabs-dropdown-close:hover {
  color: var(--text-primary);
  background: var(--bg-secondary);
}
.tabs-dropdown-item .dirty-dot {
  display: block;
}

.dropdown-slide-enter-active,
.dropdown-slide-leave-active {
  transition: opacity 0.2s, transform 0.2s;
  transform-origin: top right;
}
.dropdown-slide-enter-from,
.dropdown-slide-leave-to {
  opacity: 0;
  transform: scaleY(0.9);
}
</style>
