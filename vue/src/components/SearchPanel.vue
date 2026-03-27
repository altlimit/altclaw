<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useEditorStore } from '@/stores/editor'

interface SearchResult {
  path: string
  snippet: string
}

const editorStore = useEditorStore()
const query = ref('')
const results = ref<SearchResult[]>([])
const loading = ref(false)
const searchInputRef = ref<HTMLInputElement | null>(null)
const selectedIndex = ref(-1)
const visibleCount = ref(20)
let searchTimeout: ReturnType<typeof setTimeout> | null = null

watch(query, (newVal) => {
  if (searchTimeout) {
    clearTimeout(searchTimeout)
  }
  if (!newVal.trim()) {
    results.value = []
    loading.value = false
    return
  }
  loading.value = true
  searchTimeout = setTimeout(() => {
    performSearch()
  }, 1000)
})

async function performSearch() {
  selectedIndex.value = -1
  visibleCount.value = 20
  if (!query.value.trim()) {
    results.value = []
    return
  }
  loading.value = true
  try {
    const resp = await fetch('/api/search?q=' + encodeURIComponent(query.value))
    const data = await resp.json()
    results.value = data.results || []
    if (results.value.length > 0) {
      selectedIndex.value = 0
    }
  } catch {
    results.value = []
  } finally {
    loading.value = false
  }
}

function showMore() {
  visibleCount.value += 20
}

function openResult(res: SearchResult) {
  editorStore.openFile(res.path, res.path.split('/').pop() || res.path)
}

function attachResult(res: SearchResult) {
  window.dispatchEvent(new CustomEvent('attach-file-to-chat', { detail: res.path }))
}

function handleDragStart(e: DragEvent, res: SearchResult) {
  if (e.dataTransfer) {
    e.dataTransfer.setData('application/x-workspace-file', res.path)
    e.dataTransfer.effectAllowed = 'copyMove'
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'ArrowDown') {
    if (results.value.length === 0) return
    e.preventDefault()
    selectedIndex.value = Math.min(selectedIndex.value + 1, results.value.length - 1)
    scrollToSelected()
  } else if (e.key === 'ArrowUp') {
    if (results.value.length === 0) return
    e.preventDefault()
    selectedIndex.value = Math.max(selectedIndex.value - 1, 0)
    scrollToSelected()
  } else if (e.key === 'Enter') {
    e.preventDefault()
    if (selectedIndex.value >= 0 && selectedIndex.value < results.value.length) {
      const selected = results.value[selectedIndex.value]
      if (selected) {
        if (e.shiftKey) {
          attachResult(selected)
        } else {
          openResult(selected)
        }
      }
    } else {
      performSearch()
    }
  }
}

function scrollToSelected() {
  nextTick(() => {
    const activeEl = document.querySelector('.search-panel .result-item.selected')
    if (activeEl) {
      activeEl.scrollIntoView({ block: 'nearest' })
    }
  })
}

function focusInput() {
  if (searchInputRef.value) {
    searchInputRef.value.focus()
    searchInputRef.value.select()
  }
}

onMounted(() => {
  window.addEventListener('focus-search', focusInput)
})

onUnmounted(() => {
  window.removeEventListener('focus-search', focusInput)
})
</script>

<template>
  <div class="search-panel">
    <div class="search-header">SEARCH</div>
    <div class="search-input-container">
      <input 
        ref="searchInputRef"
        v-model="query" 
        @keydown="onKeydown"
        type="text" 
        class="search-input" 
        placeholder="Search workspace... (Cmd+P)" 
        aria-label="Search Workspace"
      />
    </div>
    
    <div class="search-results">
      <div v-if="loading" class="loading">Searching...</div>
      <div v-else-if="results.length === 0 && query" class="empty">No results found for '{{ query }}'</div>
      
      <div 
        v-for="(res, index) in results.slice(0, visibleCount)" 
        :key="res.path + res.snippet"
        class="result-item"
        :class="{ selected: index === selectedIndex }"
        @click="openResult(res)"
        @mouseenter="selectedIndex = index"
        draggable="true"
        @dragstart="handleDragStart($event, res)"
      >
        <div class="result-header">
          <div class="result-path">{{ res.path }}</div>
          <button class="attach-btn" @click.stop="attachResult(res)" title="Attach exactly to chat (Shift+Enter)">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          </button>
        </div>
        <div class="result-snippet">{{ res.snippet }}</div>
      </div>
      <button v-if="results.length > visibleCount" class="view-more-btn" @click="showMore">
        View More ({{ results.length - visibleCount }} remaining)
      </button>
    </div>
  </div>
</template>

<style scoped>
.search-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.search-header {
  font-size: 11px;
  font-weight: 600;
  padding: 10px 16px;
  color: var(--text-muted);
  letter-spacing: 0.5px;
}
.search-input-container {
  padding: 0 16px 12px;
}
.search-input {
  width: 100%;
  padding: 6px 8px;
  background: var(--bg-tertiary);
  border: 1px solid var(--border);
  color: var(--text-primary);
  border-radius: 4px;
  font-size: 13px;
}
.search-input:focus {
  outline: none;
  border-color: var(--accent);
}
.search-results {
  flex: 1;
  overflow-y: auto;
}
.loading, .empty {
  padding: 16px;
  color: var(--text-muted);
  font-size: 13px;
  font-style: italic;
}
.result-item {
  padding: 8px 16px;
  cursor: pointer;
  border-bottom: 1px solid var(--border);
  transition: background 0.1s;
}
.result-item:hover, .result-item.selected {
  background: rgba(168, 85, 247, 0.1);
}
.result-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 4px;
}
.result-path {
  font-size: 12px;
  color: var(--text-primary);
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
}
.attach-btn {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 4px;
  opacity: 0;
  transition: opacity 0.15s, background 0.15s, color 0.15s;
  display: flex;
  align-items: center;
}
.result-item:hover .attach-btn, .result-item.selected .attach-btn {
  opacity: 1;
}
.attach-btn:hover {
  background: rgba(168, 85, 247, 0.2);
  color: #c084fc;
}
.result-snippet {
  font-size: 12px;
  color: var(--text-secondary);
  white-space: pre-wrap;
  word-break: break-all;
  opacity: 0.8;
}
.view-more-btn {
  display: block;
  width: 100%;
  padding: 8px 12px;
  background: transparent;
  border: none;
  border-top: 1px solid var(--border);
  color: var(--accent);
  font-size: 12px;
  cursor: pointer;
  transition: background 0.15s;
}
.view-more-btn:hover {
  background: var(--bg-secondary);
}
</style>
