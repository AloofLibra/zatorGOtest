<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, formBody } from '../../api/client'
import type { ScopesPayload } from '../../api/types'
import { busyActive, busyButton, withBusy } from '../../stores/busy'
import { fetchAndApplyState } from '../../stores/state'
import { scopes } from '../../stores/status'
import { showToast } from '../../stores/toast'
import { confirmDialog } from '../../stores/confirm'

const enabled = ref(false)
const mask = ref('0xff00')
const shift = ref('8')
const max = ref('255')

watch(scopes, (data: ScopesPayload | null) => {
  if (!data) return
  enabled.value = !!data.enabled
}, { immediate: true, deep: true })

const chip = computed(() => (enabled.value ? 'включен' : 'выключен'))

async function save(nextEnabled: boolean) {
  if (nextEnabled) {
    const ok = await confirmDialog({
      title: 'Включить Client scopes?',
      message: [
        'Разным устройствам можно будет назначать разные стратегии по IP (mark).',
        'Нужна поддержка connmark/firewall на роутере. zapret2 перезапустится.',
      ],
    })
    if (!ok) return
  }
  try {
    await withBusy('client-scope-save', async () => {
      await api('/cgi-bin/settings.cgi', formBody({
        setting: 'client_scope',
        client_scope_enable: nextEnabled ? '1' : '0',
        client_scope_mark_mask: mask.value.trim(),
        client_scope_mark_shift: shift.value.trim(),
        client_scope_mark_max: max.value.trim(),
      }))
      enabled.value = nextEnabled
      showToast(nextEnabled ? 'Client scopes включены.' : 'Client scopes выключены.')
      await fetchAndApplyState()
    })
  } catch (error) {
    showToast((error as Error).message, 'error')
  }
}
</script>

<template>
  <section class="panel">
    <div class="panel-header">
      <h2>Client scopes <span class="beta">Beta</span></h2>
      <span class="chip" :class="{ 'is-on': enabled }">{{ chip }}</span>
    </div>
    <p class="panel-desc">
      Разные стратегии для разных устройств по IP (mark:N). Соответствует пункту 23 меню Z2R.
      Настройка стратегий по scope — во вкладке «Стратегии».
    </p>
    <form class="stack-form" @submit.prevent="save(enabled)">
      <label>
        <span>Маска mark</span>
        <input v-model="mask" type="text" :disabled="busyActive" placeholder="0xff00" autocomplete="off">
      </label>
      <label>
        <span>Сдвиг</span>
        <input v-model="shift" type="number" min="0" max="31" :disabled="busyActive">
      </label>
      <label>
        <span>Макс. scope</span>
        <input v-model="max" type="number" min="1" max="255" :disabled="busyActive">
      </label>
      <div class="actions">
        <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'client-scope-save' }"
          @click="save(!enabled)">
          {{ enabled ? 'Выключить' : 'Включить' }}
        </button>
        <button type="submit" class="ghost" :disabled="busyActive || !enabled">Сохранить параметры</button>
      </div>
    </form>
    <p v-if="scopes?.warning" class="panel-desc warn">{{ scopes.warning }}</p>
    <p v-if="scopes?.scopes?.length" class="panel-desc">
      Активные scope: {{ scopes.scopes.join(', ') }}
    </p>
  </section>
</template>
