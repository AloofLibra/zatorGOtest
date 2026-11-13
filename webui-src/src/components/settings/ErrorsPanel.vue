<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api/client'
import { busyActive, busyButton, withBusy } from '../../stores/busy'
import { showToast } from '../../stores/toast'

const content = ref('')
const available = ref(false)
const path = ref('')
const lines = ref(0)

async function load() {
  try {
    await withBusy('errors-load', async () => {
      const data = await api<{ available?: boolean; content?: string; path?: string; lines?: number }>(
        '/cgi-bin/settings.cgi?setting=nfqws2_errors',
      )
      available.value = !!data.available
      content.value = data.content || ''
      path.value = data.path || ''
      lines.value = data.lines || 0
    })
  } catch (error) {
    showToast((error as Error).message, 'error')
  }
}

onMounted(load)
</script>

<template>
  <section class="panel">
    <div class="panel-header">
      <h2>Ошибки nfqws2</h2>
      <button type="button" class="ghost" :disabled="busyActive"
        :class="{ 'is-busy': busyButton === 'errors-load' }" @click="load">Обновить</button>
    </div>
    <p class="panel-desc">Журнал последнего запуска (пункт 666 CLI).</p>
    <p v-if="!available" class="panel-desc">Журнал пока пуст или файл не найден.</p>
    <template v-else>
      <p class="panel-desc"><code>{{ path }}</code> · строк: {{ lines }}</p>
      <pre class="error-log">{{ content || '(пусто)' }}</pre>
    </template>
  </section>
</template>
