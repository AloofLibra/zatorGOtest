<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { busyActive } from './stores/busy'
import { fetchAndApplyState } from './stores/state'
import { statusLoaded } from './stores/status'
import { showToast } from './stores/toast'
import { theme, type ThemeMode } from './stores/theme'
import ToastHost from './components/ToastHost.vue'
import ConfirmDialog from './components/ConfirmDialog.vue'

const route = useRoute()
const tabs = [
  { to: '/', name: 'status', label: 'Обзор', hint: 'Состояние и быстрые действия' },
  { to: '/strategies', name: 'strategies', label: 'Стратегии', hint: 'Локи профилей и supersweep' },
  { to: '/domains', name: 'domains', label: 'Домены', hint: 'Списки hostlist / custom' },
  { to: '/settings', name: 'settings', label: 'Настройки', hint: 'Режимы, сеть, бэкапы' },
]
const themes: { id: ThemeMode; label: string }[] = [
  { id: 'light', label: 'Светлая' },
  { id: 'dark', label: 'Тёмная' },
  { id: 'auto', label: 'Авто' },
]
const active = computed(() => String(route.name || 'status'))

function setTheme(mode: ThemeMode) {
  theme.value = mode
}

onMounted(() => {
  fetchAndApplyState()
    .catch((e) => showToast((e as Error).message, 'error'))
    .finally(() => { statusLoaded.value = true })
})
</script>

<template>
  <div class="shell">
    <aside class="rail">
      <div class="brand">
        <div class="brand-mark">Zator<span>GO</span></div>
        <div class="brand-sub">Панель управления zapret2</div>
      </div>
      <nav class="nav" aria-label="Разделы">
        <router-link
          v-for="t in tabs"
          :key="t.name"
          :to="t.to"
          :class="{ 'is-active': active === t.name, 'is-locked': busyActive && !statusLoaded }"
        >
          <strong>{{ t.label }}</strong>
          <small>{{ t.hint }}</small>
        </router-link>
      </nav>
      <div class="rail-foot">
        <div class="theme-field">
          <span>Тема оформления</span>
          <div class="theme-seg" role="group" aria-label="Тема">
            <button
              v-for="t in themes"
              :key="t.id"
              type="button"
              :class="{ 'is-active': theme === t.id }"
              :aria-pressed="theme === t.id"
              @click="setTheme(t.id)"
            >{{ t.label }}</button>
          </div>
        </div>
      </div>
    </aside>
    <main class="main">
      <router-view />
    </main>
    <ToastHost />
    <ConfirmDialog />
  </div>
</template>
