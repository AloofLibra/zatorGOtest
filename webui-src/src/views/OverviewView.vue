<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { runCheck, serviceAction } from '../api/endpoints'
import CheckResults from '../components/ui/CheckResults.vue'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { confirmDialog } from '../stores/confirm'
import {
  refreshAll,
  scope,
  status,
  statusCheck,
  statusCheckRan,
  statusLoaded,
  versionInfo,
} from '../stores/status'
import { showToast } from '../stores/toast'

const running = computed(() => Boolean(status.value?.zapret2_running))
const health = computed(() => status.value?.health)
const overall = computed(() => health.value?.overall || (running.value ? 'ok' : 'down'))
const overallLabel = computed(() => {
  switch (overall.value) {
    case 'ok': return 'Норма'
    case 'degraded': return 'Деградация'
    case 'down': return 'Остановлен'
    default: return String(overall.value)
  }
})
const warnings = computed(() => status.value?.service_warnings || [])
const want = computed(() => {
  if (typeof health.value?.want_running === 'boolean') return health.value.want_running
  return Boolean(status.value?.nfqws_want_running)
})
const watchdog = computed(() => {
  if (typeof health.value?.watchdog === 'boolean') return health.value.watchdog
  return Boolean(status.value?.nfqws_watchdog)
})
const fwEnsure = computed(() => health.value?.fw_ensure || '—')
const ver = computed(() => versionInfo.value?.zator_version || '—')

function yn(v: boolean | undefined) {
  if (v === true) return 'да'
  if (v === false) return 'нет'
  return '—'
}

async function refresh() {
  try {
    await withBusy('refresh-status', async () => { await refreshAll() })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function check() {
  try {
    await withBusy('status-check', async () => {
      statusCheck.value = await runCheck()
      statusCheckRan.value = true
    })
    showToast('Проверка TLS завершена')
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function svc(action: 'start' | 'stop' | 'restart') {
  if (action === 'restart') {
    const ok = await confirmDialog({
      title: 'Перезапуск zapret2',
      message: 'Служба будет остановлена и запущена заново.',
      confirmText: 'Перезапустить',
    })
    if (!ok) return
  }
  try {
    await withBusy(`svc-${action}`, async () => {
      await serviceAction(action)
      await refreshAll()
    })
    const labels = { start: 'запущен', stop: 'остановлен', restart: 'перезапущен' } as const
    showToast(`zapret2 ${labels[action]}`)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

onMounted(() => {
  if (!statusLoaded.value) void refresh()
})
</script>

<template>
  <section class="console">
    <div v-if="!statusLoaded" class="loading">Загрузка пульта…</div>
    <template v-else>
      <div class="console-hero">
        <div class="console-hero-top">
          <div class="console-state">
            <div class="eyebrow">Состояние системы</div>
            <h1 :class="overall">{{ overallLabel }}</h1>
            <div class="sub mono">
              {{ running ? 'zapret2 работает' : 'zapret2 не запущен' }}
              · want {{ want ? 'on' : 'off' }}
              · v{{ ver }}
            </div>
          </div>
          <span class="chip" :class="{ on: running }">{{ running ? 'running' : 'stopped' }}</span>
        </div>

        <div class="health-grid">
          <div class="health-cell">
            <span class="k">Health</span>
            <span class="v" :class="overall === 'ok' ? 'ok' : overall === 'down' ? 'bad' : 'warn'">{{ overall }}</span>
          </div>
          <div class="health-cell">
            <span class="k">Want</span>
            <span class="v">{{ yn(want) }}</span>
          </div>
          <div class="health-cell">
            <span class="k">Watchdog</span>
            <span class="v" :class="watchdog ? 'ok' : 'warn'">{{ yn(watchdog) }}</span>
          </div>
          <div class="health-cell">
            <span class="k">FW ensure</span>
            <span class="v mono">{{ fwEnsure }}</span>
          </div>
          <div class="health-cell">
            <span class="k">WebUI WD</span>
            <span class="v">{{ yn(health?.webui_watchdog) }}</span>
          </div>
          <div class="health-cell">
            <span class="k">Args / hook</span>
            <span class="v">{{ yn(health?.args_present ?? status?.args_present) }} / {{ yn(health?.fw_hook_present ?? status?.fw_hook_present) }}</span>
          </div>
          <div class="health-cell">
            <span class="k">Warnings</span>
            <span class="v" :class="warnings.length ? 'warn' : 'ok'">{{ warnings.length }}</span>
          </div>
          <div class="health-cell">
            <span class="k">Post-update</span>
            <span class="v mono">{{ health?.post_update_verify || status?.post_update_verify || '—' }}</span>
          </div>
        </div>

        <div v-if="warnings.length" class="warn-list">
          <strong>Предупреждения службы</strong>
          <div v-for="(w, i) in warnings" :key="i">{{ w }}</div>
        </div>

        <div class="console-cta">
          <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'status-check' }" @click="check">Проверить</button>
          <button
            type="button"
            :disabled="busyActive"
            :class="{ 'is-busy': busyButton === (running ? 'svc-stop' : 'svc-start') }"
            @click="svc(running ? 'stop' : 'start')"
          >{{ running ? 'Стоп' : 'Старт' }}</button>
          <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'svc-restart' }" @click="svc('restart')">Рестарт</button>
          <button type="button" class="ghost" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'refresh-status' }" @click="refresh">Обновить</button>
        </div>
      </div>

      <div class="panel">
        <div class="panel-h">
          <h2>Результаты TLS</h2>
          <span class="chip">scope {{ scope }}</span>
        </div>
        <p class="panel-d">Проверка доступности эталонных целей через текущую конфигурацию.</p>
        <CheckResults
          :payload="statusCheck"
          :empty-hidden="false"
          empty-message="Нажмите «Проверить», чтобы получить TLS-результаты."
        />
      </div>

      <div class="section-label">Контекст</div>
      <div class="console-links">
        <router-link class="console-link" to="/settings#sec-provider">
          <strong>Провайдер</strong>
          <span>{{ status?.provider || '—' }}</span>
        </router-link>
        <router-link class="console-link" to="/settings#sec-modes">
          <strong>Hostlist</strong>
          <span>{{ status?.hostlist_mode || '—' }}</span>
        </router-link>
        <router-link class="console-link" to="/manual">
          <strong>TLS blob</strong>
          <span>{{ status?.tls_blob_mode || '—' }}</span>
        </router-link>
        <router-link class="console-link" to="/strategies">
          <strong>Локи стратегий</strong>
          <span>{{ status?.strategy_locks_status || '—' }}</span>
        </router-link>
        <router-link class="console-link" to="/settings#sec-firewall">
          <strong>Firewall</strong>
          <span>{{ status?.fwtype || '—' }} · FO {{ status?.flowoffload || '—' }}</span>
        </router-link>
        <router-link class="console-link" to="/settings#sec-modes">
          <strong>Авторежим</strong>
          <span>{{ status?.auto_mode || '—' }}</span>
        </router-link>
      </div>
    </template>
  </section>
</template>
