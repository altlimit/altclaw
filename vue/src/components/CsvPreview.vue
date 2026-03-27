<script setup lang="ts">
import { computed } from 'vue'
import { useEditorStore } from '@/stores/editor'

const props = defineProps<{
  path: string
}>()

const editorStore = useEditorStore()

function forceTextEdit() {
  editorStore.forceTextMode(props.path)
}

const fileContent = computed(() => {
  const file = editorStore.getTabs().find(t => t.path === props.path)
  return file?.content || ''
})

function parseCSV(text: string) {
  const result: string[][] = []
  let row: string[] = []
  let col = ""
  let inQuotes = false
  
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (c === '"') {
      if (inQuotes && text[i+1] === '"') {
        col += '"'
        i++
      } else {
        inQuotes = !inQuotes
      }
    } else if (c === ',' && !inQuotes) {
      row.push(col)
      col = ""
    } else if ((c === '\n' || c === '\r') && !inQuotes) {
      if (c === '\r' && text[i+1] === '\n') {
        i++
      }
      row.push(col)
      result.push(row)
      row = []
      col = ""
    } else {
      col += c
    }
  }
  // If there's remaining data or we are in a row
  if (col !== "" || row.length > 0) {
    row.push(col)
    result.push(row)
  }
  return result
}

const rows = computed(() => {
  return parseCSV(fileContent.value)
})

</script>

<template>
  <div class="media-preview-container">
    <div class="toolbar">
      <span class="filename">{{ path.split('/').pop() }}</span>
      <button class="text-btn" @click="forceTextEdit">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/></svg>
        Edit as Text
      </button>
    </div>

    <div class="preview-content csv-wrapper">
      <div v-if="rows.length === 0" class="empty-csv">No Data</div>
      <table v-else class="csv-table">
        <thead>
          <tr>
            <th v-for="(col, index) in rows[0]" :key="index" class="csv-th">{{ col }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, rIndex) in rows.slice(1)" :key="rIndex" class="csv-tr">
            <td v-for="(col, cIndex) in row" :key="cIndex" class="csv-td">{{ col }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.media-preview-container {
  display: flex;
  flex-direction: column;
  height: 100%;
  width: 100%;
  background: var(--bg-tertiary);
}
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 16px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
}
.filename {
  font-size: 13px;
  color: var(--text-primary);
  font-weight: 600;
}
.text-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  background: transparent;
  color: var(--text-secondary);
  border: 1px solid transparent;
  padding: 4px 10px;
  font-size: 12px;
  cursor: pointer;
  border-radius: 4px;
  transition: all 0.2s;
}
.text-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.preview-content {
  flex: 1;
  display: flex;
  padding: 0;
  overflow: auto;
}

.csv-wrapper {
  overflow: auto;
  align-items: flex-start;
  justify-content: flex-start;
  background: var(--bg-primary);
}

.csv-table {
  border-collapse: collapse;
  min-width: 100%;
  font-size: 13px;
  text-align: left;
}

.csv-th {
  position: sticky;
  top: 0;
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-weight: 600;
  padding: 10px 16px;
  border-right: 1px solid var(--border);
  border-bottom: 2px solid var(--border);
  white-space: nowrap;
  box-shadow: 0 2px 4px rgba(0,0,0,0.05);
  z-index: 10;
}
.csv-th:last-child {
  border-right: none;
}

.csv-td {
  padding: 8px 16px;
  border-right: 1px solid var(--border);
  border-bottom: 1px solid var(--border);
  color: var(--text-secondary);
  white-space: nowrap;
}
.csv-td:last-child {
  border-right: none;
}

.csv-tr:nth-child(even) {
  background: var(--bg-tertiary);
}
.csv-tr:hover {
  background: var(--bg-hover);
}

.empty-csv {
  margin: auto;
  color: var(--text-muted);
  font-size: 14px;
}
</style>
