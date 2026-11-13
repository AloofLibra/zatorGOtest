<script setup lang="ts">
import { toast, showToast } from '../stores/toast'

function dismiss() {
  showToast('', 'info')
  toast.value = null
}
</script>

<template>
  <div class="toast-region" aria-live="polite">
    <div v-if="toast" class="toast" :class="toast.type" @click="dismiss">
      {{ toast.message }}
    </div>
  </div>
</template>
