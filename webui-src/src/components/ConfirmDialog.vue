<script setup lang="ts">
import { confirmState, settleConfirm } from '../stores/confirm'
</script>

<template>
  <div v-if="confirmState" class="modal-overlay" @click.self="settleConfirm(false)">
    <div class="modal-card" role="dialog" aria-modal="true">
      <h3>{{ confirmState.title }}</h3>
      <template v-if="Array.isArray(confirmState.message)">
        <p v-for="(m, i) in confirmState.message" :key="i">{{ m }}</p>
      </template>
      <p v-else-if="confirmState.message">{{ confirmState.message }}</p>
      <div class="actions" style="margin-top:1rem">
        <button v-if="!confirmState.info" type="button" class="ghost" @click="settleConfirm(false)">
          {{ confirmState.cancelText || 'Отмена' }}
        </button>
        <button type="button" class="primary" :class="{ danger: confirmState.danger }" @click="settleConfirm(true)">
          {{ confirmState.confirmText || (confirmState.info ? 'Понятно' : 'Подтвердить') }}
        </button>
      </div>
    </div>
  </div>
</template>
