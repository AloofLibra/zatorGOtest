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
  { to: '/', name: 'status', label: 'Обзор' },
  { to: '/strategies', name: 'strategies', label: 'Стратегии' },
  { to: '/domains', name: 'domains', label: 'Домены' },
  { to: '/settings', name: 'settings', label: 'Настройки' },
]
const active = computed(() => String(route.name || 'status'))
const themeSelect = computed({
  get: () => theme.value,
  set: (v: string) => { theme.value = v as ThemeMode },
})

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
        <div class="brand-sub">управление zapret2</div>
      </div>
      <nav class="nav" aria-label="Разделы">
        <router-link
          v-for="t in tabs"
          :key="t.name"
          :to="t.to"
          :class="{ 'is-active': active === t.name, 'is-locked': busyActive && !statusLoaded }"
        >{{ t.label }}</router-link>
      </nav>
      <div class="rail-foot">
        <label class="theme-field">
          <span>Тема</span>
          <select v-model="themeSelect" aria-label="Тема">
            <option value="auto">Авто</option>
            <option value="light">Светлая</option>
            <option value="dark">Тёмная</option>
          </select>
        </label>
      </div>
    </aside>
    <main class="main">
      <router-view />
    </main>
    <ToastHost />
    <ConfirmDialog />
  </div>
</template>
