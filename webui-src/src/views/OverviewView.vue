<script setup lang="ts">
import { computed } from 'vue'
import { serviceAction, runCheck } from '../api/endpoints'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { fetchAndApplyState } from '../stores/state'
import { refreshAll, scopes, status, statusCheck, statusCheckRan, statusLoaded, versionInfo } from '../stores/status'
import { fallbackSettings } from '../stores/settings'
import { showToast } from '../stores/toast'

type Metric = {
  k: string
  v: string
  s?: string
  c?: string
  to?: string
}

const running = computed(() => Boolean(status.value?.zapret2_running))

const groups = computed(() => {
  const d = status.value
  if (!d) return [] as { title: string; items: Metric[] }[]
  const ver = versionInfo.value
  const scope = d.client_scope || scopes.value || {}
  const scopeOn = scope.mode === 'mark' || scopes.value?.enabled
  const fb = fallbackSettings.value?.state ?? '—'
  const dns = (d.profiles || []).find((p) => p.is_dns_desync)
  return [
    {
      title: 'Система',
      items: [
        { k: 'ZatorGO', v: ver?.zator_version || '—', s: ver?.update_available ? 'есть обновление' : ver?.webui_date, c: ver?.update_available ? 'info' : '', to: '/settings/updates' },
        { k: 'zapret2', v: d.zapret2_running ? 'Запущен' : 'Остановлен', s: ver?.zapret2_version, c: d.zapret2_running ? 'ok' : 'bad' },
        { k: 'Провайдер', v: String(d.provider || '—').split(' - ')[0], s: String(d.provider || '').includes(' - ') ? String(d.provider).split(' - ').slice(1).join(' - ') : '', to: '/settings/provider' },
      ],
    },
    {
      title: 'Обход DPI',
      items: [
        { k: 'Стратегии', v: d.strategy_locks_status || '—', to: '/strategies' },
        { k: 'Вручную', v: 'тест', s: 'свои desync / blob', to: '/manual' },
        { k: 'Авторотация', v: d.auto_mode || '—', c: d.auto_mode === 'включен' ? 'ok' : '', to: '/settings/modes' },
        { k: 'Fallback', v: fb, c: fb === 'включен' ? 'ok' : '', to: '/settings/modes' },
        { k: 'Hostlist', v: d.hostlist_mode || '—', to: '/settings/modes' },
        { k: 'TLS blob', v: d.tls_blob_mode || '—', to: '/settings/tls' },
        { k: 'WireGuard', v: d.wireguard || '—', to: '/settings/wg' },
      ],
    },
    {
      title: 'Сеть',
      items: [
        { k: 'Firewall', v: d.fwtype || '—', to: '/settings/firewall' },
        { k: 'Offload', v: d.flowoffload || '—', to: '/settings/firewall' },
        { k: 'Scopes', v: scopeOn ? 'mark' : 'выкл', s: scopeOn ? `mask ${scope.mask ?? scopes.value?.mask ?? '—'}` : undefined, to: '/settings/scopes' },
        { k: 'DNS антиспуф', v: dns ? (dns.dns_desync_enabled ? 'вкл' : 'выкл') : '—', to: '/settings/modes' },
      ],
    },
  ]
})

async function refresh() {
  try {
    await withBusy('refresh', fetchAndApplyState)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function svc(action: 'start' | 'stop' | 'restart') {
  try {
    await withBusy(action, async () => {
      await serviceAction(action)
      await refreshAll()
    })
    showToast(action === 'stop' ? 'zapret2 остановлен' : action === 'start' ? 'zapret2 запущен' : 'zapret2 перезапущен')
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function check() {
  try {
    await withBusy('check', async () => {
      statusCheck.value = await runCheck()
      statusCheckRan.value = true
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}
</script>

<template>
  <section>
    <div class="page-head">
      <div>
        <h1>Обзор</h1>
        <p>Сводка по системе, обходу и сети. Карточки ведут в нужный раздел настроек.</p>
      </div>
      <div class="actions">
        <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'refresh' }" @click="refresh">Обновить</button>
        <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'check' }" @click="check">Проверить доступ</button>
        <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === (running ? 'stop' : 'start') }" @click="svc(running ? 'stop' : 'start')">
          {{ running ? 'Стоп zapret2' : 'Старт zapret2' }}
        </button>
        <button type="button" class="ghost" :disabled="busyActive || !running" :class="{ 'is-busy': busyButton === 'restart' }" @click="svc('restart')">Рестарт</button>
      </div>
    </div>

    <div v-if="!statusLoaded" class="loading">Загрузка…</div>
    <template v-else>
      <template v-for="g in groups" :key="g.title">
        <div class="section-label">{{ g.title }}</div>
        <div class="metrics">
          <component
            :is="c.to ? 'router-link' : 'article'"
            v-for="c in g.items"
            :key="c.k"
            class="metric"
            v-bind="c.to ? { to: c.to } : {}"
          >
            <span class="k">{{ c.k }}</span>
            <span class="v" :class="c.c">{{ c.v }}</span>
            <span v-if="c.s" class="s">{{ c.s }}</span>
          </component>
        </div>
      </template>

      <div v-if="statusCheckRan && statusCheck" class="panel" style="margin-top:1.2rem">
        <div class="panel-h"><h2>Проверка доступности</h2></div>
        <p class="panel-d">TLS-пробы к типовым целям. «ok» — хотя бы одна версия TLS ответила успешно.</p>
        <div class="domain-list">
          <div v-for="(r, i) in (statusCheck.results || [])" :key="i" class="domain-item">
            <div>
              <strong>{{ r.label || r.target }}</strong>
              <div class="s" style="color:var(--muted);font-size:0.82rem">{{ r.verdict || r.text }}</div>
            </div>
            <span class="chip" :class="{ on: r.tls13 || r.tls12 }">{{ r.tls13 || r.tls12 ? 'ok' : 'fail' }}</span>
          </div>
        </div>
      </div>
    </template>
  </section>
</template>
