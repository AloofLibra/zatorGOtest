import { createRouter, createWebHashHistory } from 'vue-router'
import OverviewView from '../views/OverviewView.vue'
import StrategiesView from '../views/StrategiesView.vue'
import CustomStratsView from '../views/CustomStratsView.vue'
import BlobsSniView from '../views/BlobsSniView.vue'
import SupersweepView from '../views/SupersweepView.vue'
import DomainsView from '../views/DomainsView.vue'
import SettingsView from '../views/SettingsView.vue'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'status', component: OverviewView },
    { path: '/strategies', name: 'strategies', component: StrategiesView },
    { path: '/sweep', name: 'sweep', component: SupersweepView },
    { path: '/custom-strats', name: 'custom-strats', component: CustomStratsView },
    { path: '/blobs-sni', name: 'blobs-sni', alias: '/custom-blobs', component: BlobsSniView },
    { path: '/tools', name: 'legacy-tools', redirect: '/custom-strats' },
    { path: '/manual', name: 'legacy-manual', redirect: (to) => ({
      path: to.hash.includes('supersweep-panel') ? '/sweep' : '/custom-strats', query: to.query,
    }) },
    { path: '/domains/:list?', name: 'domains', component: DomainsView },
    { path: '/settings/:panel?', name: 'settings', component: SettingsView },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior(to) {
    // /settings/:panel скроллит SettingsView сама — иначе top:0 перебивает якорь.
    if (to.name === 'settings' && to.params.panel) return false
    if (to.hash) return { el: to.hash, behavior: 'smooth' }
    return { top: 0 }
  },
})
