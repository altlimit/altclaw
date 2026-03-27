<script setup lang="ts">
import { useToast } from '@/composables/useToast'

const { toasts } = useToast()
</script>

<template>
  <Teleport to="body">
    <div class="toast-container" v-if="toasts.length">
      <TransitionGroup name="toast">
        <div v-for="t in toasts" :key="t.id" :class="['toast', 'toast-' + t.type]">
          {{ t.message }}
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-container {
  position: fixed;
  bottom: 16px;
  right: 16px;
  z-index: 9999;
  display: flex;
  flex-direction: column-reverse;
  gap: 8px;
  max-width: 380px;
}
.toast {
  padding: 10px 16px;
  border-radius: 6px;
  font-size: 13px;
  line-height: 1.4;
  color: #fff;
  box-shadow: 0 4px 12px rgba(0,0,0,0.3);
  word-break: break-word;
}
.toast-error {
  background: #c0392b;
}
.toast-success {
  background: #27ae60;
}
.toast-info {
  background: #2d3436;
  border: 1px solid rgba(255,255,255,0.1);
}

.toast-enter-active {
  transition: all 0.25s ease-out;
}
.toast-leave-active {
  transition: all 0.2s ease-in;
}
.toast-enter-from {
  opacity: 0;
  transform: translateY(12px);
}
.toast-leave-to {
  opacity: 0;
  transform: translateX(20px);
}
</style>
