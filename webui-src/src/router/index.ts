import { createRouter, createWebHashHistory } from 'vue-router'
import OverviewView from '../views/OverviewView.vue'
import StrategiesView from '../views/StrategiesView.vue'
import DomainsView from '../views/DomainsView.vue'
import SettingsView from '../views/SettingsView.vue'
import { busyActive } from '../stores/busy'
import { statusLoaded } from '../stores/status'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'status', component: OverviewView },
    { path: '/strategies', name: 'strategies', component: StrategiesView },
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
  return !busyActive.value && statusLoaded.value
})
