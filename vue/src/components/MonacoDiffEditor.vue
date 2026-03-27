<script setup lang="ts">
import { VueMonacoDiffEditor } from '@guolao/vue-monaco-editor'

defineProps<{
  original: string
  modified: string
  language?: string
  theme?: string
}>()

const editorOptions = {
  automaticLayout: true,
  readOnly: true,
  minimap: { enabled: false },
  fontFamily: 'Consolas, "Courier New", monospace',
  fontSize: 14,
  renderSideBySide: true,
}
</script>

<template>
  <div class="diff-container">
    <VueMonacoDiffEditor
      :original="original"
      :modified="modified"
      :language="language || 'plaintext'"
      :theme="theme || 'vs-dark'"
      :options="editorOptions"
    />
  </div>
</template>

<style scoped>
.diff-container {
  flex: 1;
  min-height: 0;
  position: relative;
}
</style>
