<script setup lang="ts">
import { ref, onMounted } from 'vue'
import router from '@/router'
import ToastContainer from '@/components/ToastContainer.vue'

const ready = ref(false)

onMounted(() => {
  router.isReady().then(() => {
    ready.value = true
  })
})
</script>

<template>
  <template v-if="ready">
    <RouterView />
  </template>
  <ToastContainer />
</template>

<style>
/* Global resets only */
html, body, #app {
  height: 100%;
  margin: 0;
  padding: 0;
  overflow: hidden;
}
.login-layout {
  height: 100vh;
  width: 100vw;
}
</style>
