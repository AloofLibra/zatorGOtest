<script setup lang="ts">
import { ref } from 'vue'
import { api, formBody } from '../../api/client'
import { fetchSetting } from '../../api/endpoints'
import type { UpdateCheckResult } from '../../api/types'
import { busyActive, busyButton, withBusy } from '../../stores/busy'
import { fetchAndApplyState } from '../../stores/state'
import { showToast } from '../../stores/toast'
import { confirmDialog } from '../../stores/confirm'

const check = ref<UpdateCheckResult | null>(null)

async function runCheck() {
  try {
    await withBusy('update-check', async () => {
      check.value = await fetchSetting.update_check()
      if (check.value.error) {
        showToast(check.value.error, 'error')
        return
      }
      showToast(
        check.value.update_available
          ? `Есть обновление${check.value.release ? `: ${check.value.release}` : ''}`
          : 'Обновлений нет',
        check.value.update_available ? 'info' : 'success',
      )
      await fetchAndApplyState()
    })
  } catch (error) {
    showToast((error as Error).message, 'error')
  }
}

async function apply() {
  const ok = await confirmDialog({
    title: 'Применить обновление ZatorGO?',
    message: [
      'Будут скачаны и применены файлы релиза (ядро + Web UI + ресурсы).',
      'Это аналог пункта 5 меню Z2R. Может занять несколько минут.',
    ],
  })
  if (!ok) return
  try {
    await withBusy('update-apply', async () => {
      const res = await api<{ ok?: boolean; up_to_date?: boolean; installed?: number; release?: string }>(
        '/cgi-bin/settings.cgi',
        formBody({ setting: 'update_apply' }),
      )
      if (res.up_to_date) {
        showToast('Уже актуальная версия.')
      } else {
        showToast(`Обновление применено${res.release ? `: ${res.release}` : ''} (файлов: ${res.installed ?? 0}).`)
      }
      await fetchAndApplyState()
      await runCheck()
    })
  } catch (error) {
    showToast((error as Error).message, 'error')
  }
}
</script>

<template>
  <section class="panel">
    <div class="panel-header">
      <h2>Обновления ZatorGO</h2>
    </div>
    <p class="panel-desc">
      Проверка и установка релизов из вашего репозитория (пункт 5 CLI). Ядро zapret2
      обновляется отдельно при <code>zator install -zapret=true</code>.
    </p>
    <p v-if="check" class="panel-desc">
      Статус:
      <strong>{{ check.update_available ? 'есть обновление' : 'актуально' }}</strong>
      <template v-if="check.release"> · релиз {{ check.release }}</template>
      <template v-if="check.checked_at"> · проверено {{ check.checked_at }}</template>
    </p>
    <div class="actions">
      <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'update-check' }"
        @click="runCheck">Проверить</button>
      <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'update-apply' }"
        @click="apply">Установить обновление</button>
    </div>
  </section>
</template>
