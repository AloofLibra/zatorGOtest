<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api, formBody } from './api/client'
import { logout as apiLogout } from './api/endpoints'
import { clearLoginGate, forceLogin, needLogin } from './stores/auth'
import { fetchAndApplyState } from './stores/state'
import { authRequired, status, statusLoaded, versionInfo } from './stores/status'
import { applyUpdate, hydrateUpdateCheck, startUpdateScheduler, stopUpdateScheduler, updateApplying, updateCheckResult, updateCheckState } from './stores/updates'
import { showToast } from './stores/toast'
import { theme, type ThemeMode } from './stores/theme'
import ToastHost from './components/ui/ToastHost.vue'
import ConfirmDialog from './components/ui/ConfirmDialog.vue'

const route = useRoute()
const primaryTabs = [
  { to: '/', name: 'status', label: 'Обзор', hint: 'Состояние системы', icon: 'M3 10.5 12 3l9 7.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 19.5z M9 21v-7h6v7' },
  { to: '/strategies', name: 'strategies', label: 'Стратегии', hint: 'Профили и фиксации', icon: 'M4 6h16M4 12h16M4 18h16 M8 3 4 6l4 3 M16 9l4 3-4 3 M8 15l-4 3 4 3' },
  { to: '/sweep', name: 'sweep', label: 'Подбор стратегий', hint: 'Supersweep', icon: 'M12 3a9 9 0 1 0 9 9 M12 7v5l3 2 M17 3h4v4' },
  { to: '/domains', name: 'domains', label: 'Домены и списки', hint: 'Hostlist и Googlevideo', icon: 'M3 12a9 9 0 1 0 18 0 9 9 0 1 0-18 0 M3 12h18 M12 3a15 15 0 0 1 0 18 M12 3a15 15 0 0 0 0 18' },
]
const extraTabs = [
  { to: '/tools', name: 'tools', label: 'Инструменты', hint: 'Ручные стратегии и blob', icon: 'M14.7 6.3a4 4 0 0 0-5 5L3 18l3 3 6.7-6.7a4 4 0 0 0 5-5l-2.3 2.3-3-3z' },
  { to: '/settings', name: 'settings', label: 'Настройки', hint: 'Система, сеть, диагностика', icon: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M19.4 15a7.9 7.9 0 0 0 0-6l2-1.5-2-3.4L17 5a8 8 0 0 0-5-2l-.5-2h-4L7 3a8 8 0 0 0-4 2L1 4l-2 3.5L1 9a8 8 0 0 0 0 6l-2 1.5L1 20l2-1a8 8 0 0 0 4 2l.5 2h4l.5-2a8 8 0 0 0 4-2l2 1 2-3.5z' },
]
const themes: { id: ThemeMode; label: string }[] = [
  { id: 'auto', label: 'Системная' },
  { id: 'light', label: 'Светлая' },
  { id: 'dark', label: 'Тёмная' },
]
const active = computed(() => String(route.name || 'status'))
const title = computed(() => [...primaryTabs, ...extraTabs].find((tab) => tab.name === active.value)?.label || 'Обзор')
const running = computed(() => Boolean(status.value?.zapret2_running))
const releaseVersion = computed(() => updateCheckState.value === 'success' && updateCheckResult.value?.update_available
  ? (updateCheckResult.value.release || versionInfo.value?.latest_release || '') : '')
const healthOverall = computed(() => status.value?.health?.overall || 'unknown')
const warnCount = computed(() => status.value?.service_warnings?.length || 0)
const healthLabel = computed(() => ({
  ok: 'Норма', degraded: 'Требует внимания', down: 'Остановлена', unknown: 'Состояние неизвестно',
} as Record<string, string>)[healthOverall.value] || healthOverall.value)
const mobileMoreOpen = ref(false)

const loginPass = ref('')
const loginError = ref('')
const loginBusy = ref(false)
const logoutBusy = ref(false)

function setTheme(mode: ThemeMode) { theme.value = mode }

async function bootstrap() {
  try {
    await fetchAndApplyState()
    hydrateUpdateCheck(versionInfo.value)
    clearLoginGate()
    startUpdateScheduler()
  } catch (e) {
    const err = e as Error & { status?: number }
    if (err.status === 401) {
      forceLogin()
      return
    }
    statusLoaded.value = false
    showToast(err.message, 'error')
  }
}

async function submitLogin() {
  loginError.value = ''
  loginBusy.value = true
  try {
    await api('/api/login', formBody({ password: loginPass.value }))
    loginPass.value = ''
    clearLoginGate()
    statusLoaded.value = false
    await bootstrap()
  } catch (e) {
    loginError.value = (e as Error).message
  } finally {
    loginBusy.value = false
  }
}

async function logout() {
  if (!authRequired.value) return
  logoutBusy.value = true
  stopUpdateScheduler()
  try {
    try { await apiLogout() } catch { /* session may already have expired */ }
    forceLogin()
    loginPass.value = ''
    loginError.value = ''
  } finally {
    logoutBusy.value = false
  }
}

watch(needLogin, (required) => { if (required) stopUpdateScheduler() })
watch(() => route.name, () => { mobileMoreOpen.value = false })
onMounted(() => { void bootstrap() })
onUnmounted(stopUpdateScheduler)
</script>

<template>
  <div v-if="needLogin" class="login-screen">
    <form class="login-card" @submit.prevent="submitLogin">
      <img class="login-logo" :src="'./icon.png'" width="72" height="72" alt="ZatorGO">
      <div class="login-brand">Zator<span>GO</span></div>
      <p>Панель управления zapret2</p>
      <p v-if="loginError" class="login-err" role="alert">{{ loginError }}</p>
      <label><span>Пароль</span><input v-model="loginPass" type="password" autocomplete="current-password" required autofocus :disabled="loginBusy"></label>
      <button type="submit" class="primary" :disabled="loginBusy || !loginPass">{{ loginBusy ? 'Входим…' : 'Войти' }}</button>
    </form>
  </div>
  <div v-else class="shell">
    <aside class="rail" aria-label="Основная навигация">
      <router-link class="brand" to="/" aria-label="ZatorGO — обзор">
        <img class="brand-logo" :src="'./icon.png'" width="40" height="40" alt="">
        <span><strong>Zator<span>GO</span></strong><small>CONTROL CENTER</small></span>
      </router-link>
      <div class="nav-caption">Управление</div>
      <nav class="nav" aria-label="Управление">
        <router-link v-for="tab in primaryTabs" :key="tab.name" :to="tab.to" :class="{ 'is-active': active === tab.name }">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="tab.icon" /></svg>
          <span><strong>{{ tab.label }}</strong><small>{{ tab.hint }}</small></span>
        </router-link>
      </nav>
      <div class="nav-caption nav-caption-extra">Дополнительно</div>
      <nav class="nav" aria-label="Дополнительно">
        <router-link v-for="tab in extraTabs" :key="tab.name" :to="tab.to" :class="{ 'is-active': active === tab.name }">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="tab.icon" /></svg>
          <span><strong>{{ tab.label }}</strong><small>{{ tab.hint }}</small></span>
        </router-link>
      </nav>
      <div class="rail-status" :class="healthOverall">
        <span class="status-dot" />
        <div><strong>{{ running ? 'Служба запущена' : statusLoaded ? 'Служба остановлена' : 'Состояние неизвестно' }}</strong>
          <small>{{ warnCount ? `${warnCount} предупреждений` : healthLabel }}</small></div>
      </div>
      <div class="rail-foot">ZatorGO Control Center</div>
    </aside>

    <div class="workspace">
      <header class="topbar">
        <div class="breadcrumbs"><span>Панель управления</span><b>/</b><strong>{{ title }}</strong></div>
        <div class="top-controls">
          <div class="version-control" aria-label="Версия ZatorGO">
            <router-link to="/settings/updates" class="version-current" title="Текущая версия ZatorGO">{{ versionInfo?.zator_version || 'ZatorGO' }}</router-link>
            <span v-if="releaseVersion" class="version-arrow" aria-hidden="true">→</span>
            <button v-if="releaseVersion" type="button" class="version-new" :disabled="updateApplying" :class="{ 'is-busy': updateApplying }" :aria-label="`Обновить ZatorGO до ${releaseVersion}`" @click="applyUpdate('app')">{{ releaseVersion }}</button>
            <span v-else-if="updateCheckState === 'checking'" class="version-checking" aria-label="Проверяем обновления">···</span>
          </div>
          <div class="top-health" :class="healthOverall"><span class="status-dot" /><span>{{ running ? 'zapret2 работает' : statusLoaded ? 'zapret2 остановлен' : healthLabel }}</span></div>
          <label class="theme-control"><span class="sr-only">Цветовая тема</span><select :value="theme" @change="setTheme(($event.target as HTMLSelectElement).value as ThemeMode)"><option v-for="option in themes" :key="option.id" :value="option.id">{{ option.label }}</option></select></label>
          <button v-if="authRequired" type="button" class="icon-button logout-btn" aria-label="Выйти" title="Выйти" :disabled="logoutBusy" @click="logout">↗</button>
        </div>
      </header>
      <main class="main"><router-view /></main>
    </div>

    <nav class="tab-bar" aria-label="Мобильная навигация">
      <router-link v-for="tab in primaryTabs" :key="tab.name" :to="tab.to" :class="{ 'is-active': active === tab.name }">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="tab.icon" /></svg><span>{{ tab.name === 'strategies' ? 'Профили' : tab.name === 'sweep' ? 'Подбор' : tab.name === 'domains' ? 'Домены' : 'Обзор' }}</span>
      </router-link>
      <div class="mobile-more-wrap">
        <button type="button" :class="{ 'is-active': ['tools', 'settings'].includes(active) }" aria-haspopup="menu" :aria-expanded="mobileMoreOpen" @click="mobileMoreOpen = !mobileMoreOpen">
          <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="5" cy="12" r="1.4"/><circle cx="12" cy="12" r="1.4"/><circle cx="19" cy="12" r="1.4"/></svg><span>Ещё</span>
        </button>
        <div v-if="mobileMoreOpen" class="mobile-more-menu" role="menu">
          <router-link v-for="tab in extraTabs" :key="tab.name" :to="tab.to" role="menuitem">{{ tab.label }}</router-link>
          <button v-if="authRequired" type="button" role="menuitem" @click="logout">Выйти</button>
        </div>
      </div>
    </nav>
    <ToastHost />
    <ConfirmDialog />
  </div>
</template>
