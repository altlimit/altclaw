<script setup lang="ts">
import { ref, shallowRef, watch } from 'vue'
import { VueMonacoEditor, useMonaco } from '@guolao/vue-monaco-editor'

const props = defineProps<{
  modelValue: string
  language?: string
  theme?: string
  readOnly?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'save'): void
  (e: 'run'): void
}>()

const editorOptions = {
  automaticLayout: true,
  formatOnType: true,
  formatOnPaste: true,
  minimap: { enabled: false },
  wordWrap: 'on',
  fontFamily: 'Consolas, "Courier New", monospace',
  fontSize: 14,
  readOnly: props.readOnly || false,
}

const editorRef = shallowRef()

const { monacoRef } = useMonaco()

function handleMount(editor: any) {
  editorRef.value = editor
  
  // Bind Cmd+S or Ctrl+S to save
  if (monacoRef.value) {
    editor.addCommand(monacoRef.value.KeyMod.CtrlCmd | monacoRef.value.KeyCode.KeyS, () => {
      emit('save')
    })

    // Alt+Z to toggle word wrap
    editor.addCommand(monacoRef.value.KeyMod.Alt | monacoRef.value.KeyCode.KeyZ, () => {
      const current = editor.getOption(monacoRef.value!.editor.EditorOption.wordWrap)
      editor.updateOptions({ wordWrap: current === 'on' ? 'off' : 'on' })
    })

    // Alt+R to run script
    editor.addCommand(monacoRef.value.KeyMod.Alt | monacoRef.value.KeyCode.KeyR, () => {
      emit('run')
    })
  }
}

function handleChange(value: string | undefined) {
  emit('update:modelValue', value || '')
}

</script>

<template>
  <div class="monaco-container">
    <VueMonacoEditor
      v-if="modelValue !== undefined"
      :language="language || 'plaintext'"
      :theme="theme || 'vs-dark'"
      :options="editorOptions"
      :value="modelValue"
      @mount="handleMount"
      @change="handleChange"
    />
    <div v-else class="empty-state">
      <div class="logo-watermark">🐾</div>
    </div>
  </div>
</template>

<style scoped>
.monaco-container {
  flex: 1;
  min-height: 0;
  position: relative;
}
.empty-state {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-primary);
}
.logo-watermark {
  font-size: 15vw;
  opacity: 0.05;
  user-select: none;
}
</style>
