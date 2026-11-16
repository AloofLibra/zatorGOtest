<script setup lang="ts">
import { computed, ref } from 'vue'
import { runCheck, serviceAction } from '../api/endpoints'
import CheckResults from '../components/ui/CheckResults.vue'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { confirmDialog } from '../stores/confirm'
import { refreshAll, scope, status, statusCheck, statusCheckRan, statusCheckAt, statusLoaded, versionInfo, versionInfoFresh } from '../stores/status'
import { showToast } from '../stores/toast'

const running = computed(() => Boolean(status.value?.zapret2_running))
const health = computed(() => status.value?.health)
const overall = computed(() => health.value?.overall || 'unknown')
const overallLabel = computed(() => {
  if (running.value && overall.value === 'ok') return 'Служба запущена'
  if (running.value && overall.value !== 'ok') return 'Служба требует внимания'
  if (!running.value && overall.value === 'down') return 'Служба остановлена'
  return 'Состояние неизвестно'
})
const warnings = computed(() => status.value?.service_warnings || [])
const profiles = computed(() => status.value?.profiles || [])
const pinnedCount = computed(() => profiles.value.filter((p) => {
  const lock = String(p.current_lock || 'auto')
  return lock !== 'auto' && lock !== '0'
}).length)
const disabledProfiles = computed(() => profiles.value.filter((p) => String(p.current_lock) === '0').length)
const autoMode = computed(() => status.value?.auto_mode || '—')
const watchdog = computed(() => health.value?.watchdog ?? status.value?.nfqws_watchdog)
const configPending = computed(() => !!versionInfo.value?.config_update_pending)
const checkTime = ref<number | null>(statusCheckAt.value)

function yn(value: boolean | undefined) {
  return value === true ? 'Включён' : value === false ? 'Выключен' : 'Неизвестно'
}
function dateTime(value: number | null) {
  return value ? new Date(value).toLocaleString('ru-RU') : '—'
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
      checkTime.value = Date.now()
      statusCheckAt.value = checkTime.value
    })
    showToast('Проверка доступности завершена')
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function svc(action: 'start' | 'stop' | 'restart') {
  if (action === 'restart') {
    const ok = await confirmDialog({
      title: 'Перезапустить zapret2?',
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
    const labels = { start: 'запущена', stop: 'остановлена', restart: 'перезапущена' } as const
    showToast(`zapret2 ${labels[action]}`)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

</script>

<template>
  <section class="page-stack">
    <header class="page-heading overview-heading">
      <div><span class="eyebrow">Главная панель</span><h1>Обзор системы</h1><p>Состояние службы, результаты проверки и важные действия.</p></div>
      <button type="button" class="secondary-button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'refresh-status' }" @click="refresh">Обновить состояние</button>
    </header>

    <div v-if="!statusLoaded" class="loading">Загружаем состояние устройства…</div>
    <template v-else>
      <section class="overview-top">
        <article class="service-card" :class="overall">
          <div class="service-card-label"><span class="status-dot" />Состояние zapret2</div>
          <h2>{{ overallLabel }}</h2>
          <p>{{ running ? 'Процесс службы запущен.' : 'Процесс службы не запущен.' }} Доступность сайтов определяется отдельной проверкой.</p>
          <div class="service-actions">
            <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'status-check' }" @click="check">Проверить доступность</button>
            <button type="button" class="secondary-button" :disabled="busyActive" :class="{ 'is-busy': busyButton === (running ? 'svc-stop' : 'svc-start') }" @click="svc(running ? 'stop' : 'start')">{{ running ? 'Остановить службу' : 'Запустить службу' }}</button>
            <button type="button" class="icon-action" aria-label="Перезапустить zapret2" title="Перезапустить zapret2" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'svc-restart' }" @click="svc('restart')">↻</button>
          </div>
        </article>
        <aside class="quick-card">
          <h2>Быстрые переходы</h2>
          <router-link to="/sweep"><span><strong>Подобрать стратегии</strong><small>Быстрый тест и полный прогон</small></span><b>→</b></router-link>
          <router-link to="/strategies"><span><strong>Настроить профили</strong><small>Фиксации и проверки</small></span><b>→</b></router-link>
          <router-link to="/domains"><span><strong>Управлять списками</strong><small>Домены, исключения, Googlevideo</small></span><b>→</b></router-link>
        </aside>
      </section>

      <section class="update-card" aria-labelledby="updates-title">
        <div class="section-heading"><div><h2 id="updates-title">Конфигурация</h2><p>Обновление шаблонов и стратегических ресурсов</p></div>
          <router-link class="text-button" to="/settings/updates">Настроить</router-link>
        </div>
        <div class="update-row" :class="versionInfoFresh ? configPending ? 'is-info' : 'is-neutral' : 'is-muted'">
          <span class="update-mark">⌘</span>
          <div class="update-copy">
            <strong v-if="configPending">Обнаружен более новый шаблон конфигурации</strong>
            <strong v-else-if="versionInfoFresh">Нового шаблона конфигурации не обнаружено</strong>
            <strong v-else>Состояние шаблона не обновлено</strong>
            <small v-if="versionInfoFresh && versionInfo">Активный: {{ versionInfo.config_date || '—' }} · Шаблон: {{ versionInfo.config_default_date || '—' }}</small>
            <small v-else>Показанные сведения могут быть устаревшими.</small>
          </div>
          <router-link class="text-button" to="/settings/config">Посмотреть</router-link>
        </div>
      </section>

      <section class="overview-metrics" aria-label="Краткие показатели">
        <article><span>Сторожевой процесс</span><strong :class="watchdog === true ? 'state-ok' : watchdog === false ? 'state-warn' : ''">{{ yn(watchdog) }}</strong><small>Watchdog nfqws2</small></article>
        <article><span>Авторотация</span><strong>{{ autoMode }}</strong><small>TCP / HTTP</small></article>
        <article><span>Стратегии</span><strong>{{ pinnedCount }} из {{ profiles.length }}</strong><small>{{ disabledProfiles }} выключено</small></article>
        <article><span>Предупреждения</span><strong :class="warnings.length ? 'state-warn' : ''">{{ warnings.length }}</strong><small>Состояние службы</small></article>
      </section>

      <section class="results-section">
        <div class="section-heading"><div><h2>Проверка доступности</h2><p>Результаты появляются только после запуска проверки через текущую конфигурацию.</p></div>
          <span v-if="checkTime" class="scope-label">Последняя проверка: {{ dateTime(checkTime) }}</span>
        </div>
        <article v-if="!statusCheckRan" class="empty-state"><strong>Проверка ещё не запускалась</strong><p>Запустите проверку, чтобы получить актуальные результаты для TLS и медиапотоков.</p><button type="button" class="primary" :disabled="busyActive" @click="check">Проверить доступность</button></article>
        <CheckResults v-else :payload="statusCheck" :empty-hidden="false" empty-message="Проверка завершилась без подробных результатов." />
      </section>

      <section class="overview-bottom">
        <article class="panel">
          <div class="section-heading"><div><h2>Основные профили</h2><p>Текущие фиксации из состояния устройства</p></div><router-link class="text-button" to="/strategies">Все профили →</router-link></div>
          <div class="profile-summary-list">
            <div v-for="profile in profiles.slice(0, 4)" :key="profile.profile">
              <span><strong>{{ profile.label }}</strong><small>Профиль {{ profile.profile }} · макс. №{{ profile.max_strategy ?? '—' }}</small></span>
              <b :class="profile.current_lock === '0' ? 'is-disabled' : profile.current_lock && profile.current_lock !== 'auto' ? 'is-locked' : ''">{{ profile.current_lock === '0' ? 'Выключен' : profile.current_lock && profile.current_lock !== 'auto' ? `Стратегия #${profile.current_lock}` : 'Без фиксации' }}</b>
            </div>
            <p v-if="!profiles.length" class="panel-d">Профили пока не загружены.</p>
          </div>
        </article>
        <details class="diagnostics-card">
          <summary><span><strong>Диагностика службы</strong><small>Технические детали доступны по запросу</small></span><b>⌄</b></summary>
          <dl>
            <div><dt>Scope</dt><dd>{{ scope }}</dd></div>
            <div><dt>Firewall ensure</dt><dd>{{ health?.fw_ensure || '—' }}</dd></div>
            <div><dt>Аргументы nfqws2</dt><dd>{{ health?.args_present ?? status?.args_present ?? '—' }}</dd></div>
            <div><dt>Firewall hook</dt><dd>{{ health?.fw_hook_present ?? status?.fw_hook_present ?? '—' }}</dd></div>
            <div><dt>Проверка после обновления</dt><dd>{{ health?.post_update_verify || status?.post_update_verify || '—' }}</dd></div>
            <div><dt>Версия ZatorGO</dt><dd>{{ versionInfo?.zator_version || '—' }}</dd></div>
          </dl>
          <div v-if="warnings.length" class="warning-list"><strong>Предупреждения</strong><p v-for="(warning, index) in warnings" :key="index">{{ warning }}</p></div>
          <router-link class="text-button" to="/settings/errors">Все сообщения службы →</router-link>
        </details>
      </section>
    </template>
  </section>
</template>
