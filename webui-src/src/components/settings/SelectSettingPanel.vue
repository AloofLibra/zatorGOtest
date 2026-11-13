<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, formBody } from '../../api/client'
import { busyActive, busyButton, withBusy } from '../../stores/busy'
import { fetchAndApplyState } from '../../stores/state'
import { refreshAll, status } from '../../stores/status'
import { showToast } from '../../stores/toast'

const props = defineProps<{
  setting: 'fwtype' | 'flowoffload'
  title: string
  descriptions: string[]
  options: { value: string; label: string }[]
}>()

const current = computed(() => {
  const s = status.value
  if (!s) return props.options[0]?.value || ''
  return (props.setting === 'fwtype' ? s.fwtype : s.flowoffload) || props.options[0]?.value || ''
})

const selected = ref(current.value)
watch(current, (v) => { selected.value = v }, { immediate: true })

const dirty = computed(() => selected.value !== current.value)
const busyKey = computed(() => `select-${props.setting}`)

async function save() {
  try {
    await withBusy(busyKey.value, async () => {
      await api('/cgi-bin/settings.cgi', formBody({ setting: props.setting, value: selected.value }))
      showToast(`${props.title}: сохранено (${selected.value}).`)
      await fetchAndApplyState()
      await refreshAll()
    })
  } catch (error) {
    showToast((error as Error).message, 'error')
  }
}
</script>

<template>
  <section class="panel">
    <div class="panel-header">
      <h2>{{ title }}</h2>
      <span class="chip">{{ current || '—' }}</span>
    </div>
    <p v-for="(d, i) in descriptions" :key="i" class="panel-desc">{{ d }}</p>
    <form class="inline-form" @submit.prevent="save">
      <label>
        <span>Значение</span>
        <select v-model="selected" :disabled="busyActive">
          <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
        </select>
      </label>
      <button type="submit" :disabled="busyActive || !dirty"
        :class="{ 'is-busy': busyButton === busyKey }">Сохранить</button>
    </form>
  </section>
</template>
