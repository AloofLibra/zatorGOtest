<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, formBody } from './api/client'
import { logout as apiLogout } from './api/endpoints'
import { clearLoginGate, forceLogin, needLogin } from './stores/auth'
import { busyActive } from './stores/busy'
import { fetchAndApplyState } from './stores/state'
import { authRequired, statusLoaded } from './stores/status'
import { showToast } from './stores/toast'
import { theme, type ThemeMode } from './stores/theme'
import ToastHost from './components/ui/ToastHost.vue'
import ConfirmDialog from './components/ui/ConfirmDialog.vue'

const route = useRoute()
const tabs = [
  { to: '/', name: 'status', label: 'Обзор', hint: 'Состояние и быстрые действия' },
  { to: '/strategies', name: 'strategies', label: 'Стратегии', hint: 'Локи профилей и supersweep' },
  { to: '/manual', name: 'manual', label: 'Вручную', hint: 'Добавить стратегию / blob (тест)' },
  { to: '/domains', name: 'domains', label: 'Домены', hint: 'Списки hostlist / custom' },
  { to: '/settings', name: 'settings', label: 'Настройки', hint: 'Режимы, сеть, бэкапы' },
]
const themes: { id: ThemeMode; label: string }[] = [
  { id: 'light', label: 'Светлая' },
  { id: 'dark', label: 'Тёмная' },
  { id: 'auto', label: 'Авто' },
]
const active = computed(() => String(route.name || 'status'))

const loginPass = ref('')
const loginError = ref('')
const loginBusy = ref(false)
const logoutBusy = ref(false)

function setTheme(mode: ThemeMode) {
  theme.value = mode
}

async function bootstrap() {
  try {
    await fetchAndApplyState()
    clearLoginGate()
    // statusLoaded / settingsLoaded выставляет fetchAndApplyState
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
  try {
    try {
      await apiLogout()
    } catch {
      // даже при сетевой ошибке показываем логин; cookie сбросит public logout при повторе
    }
    forceLogin()
    loginPass.value = ''
    loginError.value = ''
  } finally {
    logoutBusy.value = false
  }
}

onMounted(() => { void bootstrap() })
</script>

<template>
  <div v-if="needLogin" class="login-screen">
    <form class="login-card" @submit.prevent="submitLogin">
      <h1>Zator<span>GO</span></h1>
      <p>Введите пароль панели управления</p>
      <p v-if="loginError" class="login-err">{{ loginError }}</p>
      <label>
        <span>Пароль</span>
        <input v-model="loginPass" type="password" autocomplete="current-password" required autofocus :disabled="loginBusy">
      </label>
      <button type="submit" class="primary" :disabled="loginBusy || !loginPass">Войти</button>
    </form>
  </div>
  <div v-else class="shell">
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
          :class="{ 'is-active': active === t.name, 'is-locked': busyActive || !statusLoaded }"
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
        <button
          v-if="authRequired"
          type="button"
          class="ghost logout-btn"
          :disabled="logoutBusy || busyActive"
          @click="logout"
        >Выйти</button>
      </div>
    </aside>
    <main class="main">
      <router-view />
    </main>
    <ToastHost />
    <ConfirmDialog />
  </div>
</template>
