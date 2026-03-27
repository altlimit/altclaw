<script setup lang="ts">
import { computed, ref } from 'vue'
import { useEditorStore } from '@/stores/editor'

const props = defineProps<{
  path: string
}>()

const editorStore = useEditorStore()

// Derive a secure local URL from the backend API for rendering media
const srcUrl = computed(() => {
  return `/api/download?path=${encodeURIComponent(props.path)}`
})

const ext = computed(() => {
  const parts = props.path.split('.')
  return parts.length > 1 ? parts.pop()?.toLowerCase() : ''
})

const isImage = computed(() => ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico'].includes(ext.value || ''))
const isVideo = computed(() => ['mp4', 'webm', 'ogg'].includes(ext.value || ''))
const isAudio = computed(() => ['mp3', 'wav', 'ogg'].includes(ext.value || ''))
const isPdf = computed(() => ext.value === 'pdf')

const isExpanded = ref(false)

function toggleExpand() {
  if (isImage.value) {
    isExpanded.value = !isExpanded.value
  }
}

function forceTextEdit() {
  editorStore.forceTextMode(props.path)
}
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

    <div class="preview-content">
      <img v-if="isImage" :src="srcUrl" alt="Media Preview" class="preview-image" :class="{ 'expanded': isExpanded }" @click="toggleExpand" :title="isExpanded ? 'Zoom out' : 'Zoom in'" />
      <video v-else-if="isVideo" controls :src="srcUrl" class="preview-video"></video>
      <div v-else-if="isAudio" class="audio-wrapper">
        <div class="audio-icon">🎵</div>
        <audio controls :src="srcUrl" class="preview-audio"></audio>
      </div>
      <embed v-else-if="isPdf" :src="srcUrl" type="application/pdf" class="preview-pdf" />
      <div v-else class="unsupported">
        <div class="unsupported-icon">📄</div>
        <div>Preview not available for this file type.</div>
        <button class="text-btn outline mt-4" @click="forceTextEdit">Open in Editor</button>
      </div>
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
.text-btn.outline {
  border-color: var(--border);
}
.text-btn.outline:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.mt-4 { margin-top: 16px; }

.preview-content {
  flex: 1;
  display: flex;
  padding: 24px;
  overflow: auto;
}
.preview-content > * {
  margin: auto;
}
.preview-image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  box-shadow: 0 4px 12px rgba(0,0,0,0.1);
  cursor: zoom-in;
  transition: max-width 0.2s, max-height 0.2s;
}
.preview-image.expanded {
  max-width: none;
  max-height: none;
  cursor: zoom-out;
}
.preview-video {
  max-width: 100%;
  max-height: 100%;
  outline: none;
  border-radius: 8px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.1);
}
.audio-wrapper {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  padding: 32px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.1);
}
.audio-icon {
  font-size: 48px;
}
.preview-audio {
  width: 300px;
  outline: none;
}
.preview-pdf {
  width: 100%;
  height: 100%;
  border: none;
  background: white;
  border-radius: 8px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.1);
}
.unsupported {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
}
.unsupported-icon {
  font-size: 48px;
  margin-bottom: 16px;
  opacity: 0.5;
}
</style>
