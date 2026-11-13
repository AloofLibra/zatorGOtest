<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, formBody } from '../../api/client'
import { busyActive, busyButton, withBusy } from '../../stores/busy'
import { showToast } from '../../stores/toast'
import { confirmDialog } from '../../stores/confirm'

const content = ref('')
const path = ref('')
const loaded = ref(false)
const original = ref('')

async function load() {
  try {
    await withBusy('config-load', async () => {
      const data = await api<{ content?: string; path?: string; error?: string }>(
        '/cgi-bin/settings.cgi?setting=config_raw',
      )
      if (data.error) throw new Error(data.error)
      content.value = data.content || ''
      original.value = content.value
      path.value = data.path || ''
      loaded.value = true
    })
  } catch (error) {
    showToast((error as Error).message, 'error')
  }
}

async function save() {
  if (content.value === original.value) {
    showToast('Нет изменений.', 'info')
    return
  }
  const ok = await confirmDialog({
    title: 'Сохранить config?',
    message: [
      'Будет перезаписан файл zapret2/config.',
      'zapret2 перезапустится. Ошибка в синтаксисе может остановить обход.',
    ],
    danger: true,
  })
  if (!ok) return
  try {
    await withBusy('config-save', async () => {
      await api('/cgi-bin/settings.cgi', formBody({ setting: 'config_raw', value: content.value }))
      original.value = content.value
      showToast('Конфиг сохранён, служба перезапущена.')
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
      <h2>Конфиг zapret2</h2>
      <button type="button" class="ghost" :disabled="busyActive"
        :class="{ 'is-busy': busyButton === 'config-load' }" @click="load">Обновить</button>
    </div>
    <p class="panel-desc">
      Полное редактирование <code>{{ path || '/opt/zapret2/config' }}</code> — аналог пункта 7 меню Z2R.
      Меняйте осторожно: здесь же переменные NFQWS2_OPT, порты, режимы.
    </p>
    <textarea v-if="loaded" v-model="content" class="config-editor" spellcheck="false"
      :disabled="busyActive" rows="18" aria-label="Содержимое config"></textarea>
    <p v-else class="status-loading">Загрузка конфига…</p>
    <div class="actions">
      <button type="button" :disabled="busyActive || !loaded"
        :class="{ 'is-busy': busyButton === 'config-save' }" @click="save">Сохранить и перезапустить</button>
    </div>
  </section>
</template>
