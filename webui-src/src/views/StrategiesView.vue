<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { applySetting, clearLock, setLock, profileCheck } from '../api/endpoints'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { fetchAndApplyState } from '../stores/state'
import { locks, refreshAll, scope, scopes, statusLoaded, strategyChecks } from '../stores/status'
import { refreshTlsBlobSettings, tlsBlobSettings } from '../stores/settings'
import { announceRestart, restartSuffix, showToast } from '../stores/toast'
import { api, formBody } from '../api/client'

const router = useRouter()
const draft = ref<Record<string, string>>({})
const fakeMode = ref<Record<string, string>>({})
const tlsProfile = ref<Record<string, string>>({})
const cloneSize = ref<Record<string, string>>({})

watch(locks, (list) => {
  for (const p of list || []) {
    const id = String(p.profile)
    if (draft.value[id] === undefined) {
      draft.value[id] = p.current_lock && p.current_lock !== 'auto' ? String(p.current_lock) : ''
    }
  }
}, { immediate: true, deep: true })

watch(tlsBlobSettings, (t) => {
  if (!t) return
  for (const p of locks.value || []) {
    const id = String(p.profile)
    fakeMode.value[id] = t.profile_modes?.[id] || 'classic'
    tlsProfile.value[id] = t.profile_blobs?.[id] ?? ''
    cloneSize.value[id] = t.profile_sizes?.[id] || ''
  }
}, { immediate: true, deep: true })

const scopeOptions = computed(() => scopes.value.scopes || ['default'])

function changeScope(v: string) {
  scope.value = v || 'default'
  router.replace({ query: { scope: scope.value } })
  refreshAll().catch((e) => showToast((e as Error).message, 'error'))
}

async function refresh() {
  try {
    await withBusy('refresh-locks', fetchAndApplyState)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function save(profile: string) {
  const raw = draft.value[profile]?.trim()
  if (!raw) {
    showToast('Укажите номер стратегии', 'error')
    return
  }
  const n = Number(raw)
  if (!Number.isInteger(n) || n < 0) {
    showToast('Некорректный номер стратегии', 'error')
    return
  }
  try {
    await withBusy(`lock-${profile}`, async () => {
      await setLock(profile, n, scope.value)
      await refreshAll()
    })
    showToast(`Профиль ${profile}: стратегия ${n}`)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function reset(profile: string) {
  try {
    await withBusy(`clear-${profile}`, async () => {
      await clearLock(profile, scope.value)
      draft.value[profile] = ''
      await refreshAll()
    })
    showToast(`Профиль ${profile}: авто`)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function check(profile: string) {
  try {
    await withBusy(`check-${profile}`, async () => {
      strategyChecks[profile] = await profileCheck(profile, scope.value)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function supersweep(action: 'start' | 'cancel') {
  try {
    await withBusy(`ss-${action}`, async () => {
      await api('/cgi-bin/settings.cgi', formBody({ setting: 'supersweep', value: action }))
      showToast(action === 'start' ? 'Суперсвип запущен' : 'Суперсвип отменён')
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveFake(profile: string) {
  try {
    await withBusy(`fake-${profile}`, async () => {
      announceRestart()
      const res = await applySetting.fake_mode(profile, fakeMode.value[profile] || 'classic')
      showToast(`Режим фейков профиля ${profile}.${restartSuffix(res)}`)
      await refreshTlsBlobSettings()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveTlsProfile(profile: string) {
  try {
    await withBusy(`tlsprof-${profile}`, async () => {
      announceRestart()
      const res = await applySetting.tls_blob_profile(profile, tlsProfile.value[profile] ?? '')
      showToast(`TLS профиля ${profile}.${restartSuffix(res)}`)
      await refreshTlsBlobSettings()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveCloneSize(profile: string) {
  try {
    await withBusy(`clone-${profile}`, async () => {
      announceRestart()
      const res = await applySetting.clone_size(profile, cloneSize.value[profile] || '')
      showToast(`Лимит клона профиля ${profile}.${restartSuffix(res)}`)
      await refreshTlsBlobSettings()
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
        <h1>Стратегии</h1>
        <p>Зафиксируйте рабочую стратегию для каждого профиля или оставьте «Авто». Scope — если включены client marks.</p>
      </div>
      <div class="actions">
        <label class="field" style="min-width:160px">
          <span>Scope</span>
          <select :value="scope" :disabled="busyActive" @change="changeScope(($event.target as HTMLSelectElement).value)">
            <option v-for="s in scopeOptions" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
        <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'refresh-locks' }" @click="refresh">Обновить</button>
        <button type="button" class="ghost" :disabled="busyActive" @click="supersweep('start')">Суперсвип</button>
        <button type="button" class="ghost danger" :disabled="busyActive" @click="supersweep('cancel')">Стоп свип</button>
      </div>
    </div>

    <p v-if="scopes.warning" class="panel-d" style="color:var(--warn)">{{ scopes.warning }}</p>
    <div v-if="!statusLoaded" class="loading">Загрузка…</div>
    <div v-else class="strategy-grid">
      <article v-for="p in locks" :key="p.profile" class="strategy">
        <div>
          <h3>{{ p.profile }}. {{ p.label }}</h3>
          <p v-if="p.description" class="meta">{{ p.description }}</p>
          <div class="meta">
            lock: {{ p.current_lock || 'auto' }}
            · max: {{ p.max_strategy ?? '—' }}
            <template v-if="p.is_fallback"> · fallback {{ p.fallback_enabled ? 'вкл' : 'выкл' }}</template>
            <template v-if="p.is_udp_games"> · games {{ p.udp_games_enabled ? 'вкл' : 'выкл' }}</template>
            <template v-if="p.is_dns_desync"> · dns {{ p.dns_desync_enabled ? 'вкл' : 'выкл' }}</template>
          </div>
        </div>
        <div class="row">
          <input v-model="draft[String(p.profile)]" type="number" min="0" :max="p.max_strategy || 999"
            :disabled="busyActive" placeholder="номер" style="max-width:7rem">
          <button type="button" class="primary" :disabled="busyActive"
            :class="{ 'is-busy': busyButton === `lock-${p.profile}` }"
            @click="save(String(p.profile))">Зафиксировать</button>
          <button type="button" class="ghost" :disabled="busyActive"
            :class="{ 'is-busy': busyButton === `clear-${p.profile}` }"
            @click="reset(String(p.profile))">Авто</button>
          <button type="button" class="ghost" :disabled="busyActive"
            :class="{ 'is-busy': busyButton === `check-${p.profile}` }"
            @click="check(String(p.profile))">Проверка</button>
        </div>
        <div class="grid-2" style="margin-top:0.75rem">
          <label class="field">
            <span>Режим фейков</span>
            <select v-model="fakeMode[String(p.profile)]" :disabled="busyActive">
              <option value="classic">classic</option>
              <option value="clone">clone</option>
            </select>
          </label>
          <div class="actions" style="align-items:end">
            <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === `fake-${p.profile}` }"
              @click="saveFake(String(p.profile))">Сохранить режим</button>
          </div>
          <label class="field">
            <span>TLS blob профиля</span>
            <select v-model="tlsProfile[String(p.profile)]" :disabled="busyActive">
              <option value="">как глобальный</option>
              <option value="fake_default_tls">fake_default_tls</option>
              <option v-for="b in (tlsBlobSettings?.available_blobs || [])" :key="b" :value="b">{{ b }}</option>
            </select>
          </label>
          <div class="actions" style="align-items:end">
            <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === `tlsprof-${p.profile}` }"
              @click="saveTlsProfile(String(p.profile))">Сохранить TLS</button>
          </div>
          <label v-if="fakeMode[String(p.profile)] === 'clone'" class="field">
            <span>Лимит клона (байт, пусто = без лимита)</span>
            <input v-model="cloneSize[String(p.profile)]" :disabled="busyActive" placeholder="1200">
          </label>
          <div v-if="fakeMode[String(p.profile)] === 'clone'" class="actions" style="align-items:end">
            <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === `clone-${p.profile}` }"
              @click="saveCloneSize(String(p.profile))">Сохранить лимит</button>
          </div>
        </div>
        <div v-if="strategyChecks[String(p.profile)]" class="meta">
          {{ strategyChecks[String(p.profile)].message || (strategyChecks[String(p.profile)].results || []).map(r => r.verdict || r.text).join(' · ') }}
        </div>
      </article>
    </div>
  </section>
</template>
