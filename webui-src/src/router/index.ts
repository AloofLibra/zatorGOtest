import { createRouter, createWebHashHistory } from 'vue-router'
import OverviewView from '../views/OverviewView.vue'
import StrategiesView from '../views/StrategiesView.vue'
import ManualAddView from '../views/ManualAddView.vue'
import DomainsView from '../views/DomainsView.vue'
import SettingsView from '../views/SettingsView.vue'
import { busyActive } from '../stores/busy'
import { statusLoaded } from '../stores/status'
import { showToast } from '../stores/toast'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'status', component: OverviewView },
    { path: '/strategies', name: 'strategies', component: StrategiesView },
    { path: '/manual', name: 'manual', component: ManualAddView },
    { path: '/domains/:list?', name: 'domains', component: DomainsView },
    { path: '/settings/:panel?', name: 'settings', component: SettingsView },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior(to) {
    if (to.hash) return { el: to.hash, behavior: 'smooth' }
    return { top: 0 }
  },
})

let first = true
router.beforeEach(() => {
  if (first) {
    first = false
    return true
  }
  if (!statusLoaded.value) {
    showToast('Дождитесь загрузки состояния', 'error')
    return false
  }
  // Busy не блокирует навигацию: вкладки уже помечаются is-locked в App.vue.
  // Жёсткий return false оставлял UI «навсегда», если CGI завис без таймаута.
  if (busyActive.value) {
    showToast('Идёт операция — подождите завершения', 'info')
  }
  return true
})
