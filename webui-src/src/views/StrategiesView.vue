<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { applySetting, clearLock, setLock, profileCheck } from '../api/endpoints'
import type { ProfileInfo } from '../api/types'
import { gatedReason, isProfileGated } from '../gating'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { fetchAndApplyState } from '../stores/state'
import { locks, refreshAll, scope, scopes, statusLoaded, strategyChecks } from '../stores/status'
import { refreshTlsBlobSettings, tlsBlobSettings } from '../stores/settings'
import { restartSuffix, showToast } from '../stores/toast'
import CheckResults from '../components/ui/CheckResults.vue'
import SupersweepPanel from '../components/strategies/SupersweepPanel.vue'

const OVERRIDE_PROFILES = new Set(['1', '2', '3', '4', '8'])

const router = useRouter()
const route = useRoute()
/** Vue 3.4+ для input[type=number] кладёт в v-model number — всегда нормализуем в строку. */
type DraftMap = Record<string, string | number>
const draft = ref<DraftMap>({})
const fakeMode = ref<Record<string, string>>({})
const tlsProfile = ref<Record<string, string>>({})
const cloneSize = ref<Record<string, string>>({})
const overrideDirty = ref<Record<string, boolean>>({})

function draftText(profile: string): string {
  const v = draft.value[profile]
  return v === undefined || v === null ? '' : String(v).trim()
}

function syncDraftFromLocks(list: ProfileInfo[] | undefined | null, force = false) {
  const next: DraftMap = force ? {} : { ...draft.value }
  for (const p of list || []) {
    const id = String(p.profile)
    if (force || next[id] === undefined) {
      next[id] = p.current_lock && p.current_lock !== 'auto' ? String(p.current_lock) : ''
    }
  }
  draft.value = next
}

function syncOverridesFromSettings(force = false) {
  const t = tlsBlobSettings.value
  if (!t) return
  for (const p of locks.value || []) {
    const id = String(p.profile)
    if (!OVERRIDE_PROFILES.has(id)) continue
    if (!force && overrideDirty.value[id]) continue
    fakeMode.value[id] = t.profile_modes?.[id] || 'classic'
    tlsProfile.value[id] = t.profile_blobs?.[id] ?? ''
    cloneSize.value[id] = t.profile_sizes?.[id] || ''
  }
}

watch(locks, (list) => syncDraftFromLocks(list, false), { immediate: true, deep: true })
watch(tlsBlobSettings, () => syncOverridesFromSettings(false), { immediate: true, deep: true })

const scopeOptions = computed(() => scopes.value.scopes || ['default'])

onMounted(() => {
  const q = String(route.query.scope || '')
  if (q && q !== scope.value) {
    scope.value = q
    refreshAll().catch((e) => showToast((e as Error).message, 'error'))
  }
})

async function changeScope(v: string) {
  scope.value = v || 'default'
  router.replace({ query: { scope: scope.value } })
  for (const key of Object.keys(strategyChecks)) delete strategyChecks[key]
  draft.value = {}
  overrideDirty.value = {}
  try {
    await withBusy('scope-change', async () => {
      await refreshAll()
      syncDraftFromLocks(locks.value, true)
      syncOverridesFromSettings(true)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

function markOverrideDirty(profile: string) {
  overrideDirty.value[profile] = true
}

function canOverride(p: ProfileInfo) {
  return OVERRIDE_PROFILES.has(String(p.profile))
}

function gated(p: ProfileInfo) {
  return isProfileGated(p)
}

async function refresh() {
  try {
    await withBusy('refresh-locks', fetchAndApplyState)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function save(profile: string, p: ProfileInfo) {
  if (gated(p)) {
    showToast(gatedReason(p), 'error')
    return
  }
  try {
    const raw = draftText(profile)
    if (!raw) {
      showToast('Укажите номер стратегии', 'error')
      return
    }
    const n = Number(raw)
    if (!Number.isInteger(n) || n < 0) {
      showToast('Некорректный номер стратегии', 'error')
      return
    }
    const max = Number(p.max_strategy || 0)
    if (n !== 0 && max > 0 && n > max) {
      showToast(`Стратегия вне диапазона (1–${max}, или 0)`, 'error')
      return
    }
    await withBusy(`lock-${profile}`, async () => {
      await setLock(profile, n, scope.value)
      draft.value[profile] = String(n)
      delete strategyChecks[profile]
      await refreshAll()
      syncDraftFromLocks(locks.value, true)
      if (n !== 0) {
        try {
          strategyChecks[profile] = await profileCheck(profile, scope.value)
        } catch {
          // стратегия уже зафиксирована — проверка необязательна
        }
      }
    })
    showToast(n === 0
      ? `Профиль ${profile} выключен`
      : `Профиль ${profile}: стратегия ${n}`)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function reset(profile: string, p: ProfileInfo) {
  if (gated(p)) {
    showToast(gatedReason(p), 'error')
    return
  }
  try {
    await withBusy(`clear-${profile}`, async () => {
      await clearLock(profile, scope.value)
      draft.value[profile] = ''
      delete strategyChecks[profile]
      await refreshAll()
    })
    showToast(`Профиль ${profile}: авто`)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function check(profile: string, p: ProfileInfo) {
  if (gated(p)) {
    showToast(gatedReason(p), 'error')
    return
  }
  try {
    await withBusy(`check-${profile}`, async () => {
      strategyChecks[profile] = await profileCheck(profile, scope.value)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

function focusSupersweep() {
  document.getElementById('supersweep-panel')?.scrollIntoView({ block: 'start', behavior: 'smooth' })
}

async function saveFake(profile: string) {
  try {
    await withBusy(`fake-${profile}`, async () => {
      const res = await applySetting.fake_mode(profile, fakeMode.value[profile] || 'classic')
      showToast(`Режим фейков профиля ${profile}.${restartSuffix(res)}`)
      overrideDirty.value[profile] = false
      await refreshTlsBlobSettings()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveTlsProfile(profile: string) {
  try {
    await withBusy(`tlsprof-${profile}`, async () => {
      const res = await applySetting.tls_blob_profile(profile, tlsProfile.value[profile] ?? '')
      showToast(`TLS профиля ${profile}.${restartSuffix(res)}`)
      overrideDirty.value[profile] = false
      await refreshTlsBlobSettings()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveCloneSize(profile: string) {
  try {
    await withBusy(`clone-${profile}`, async () => {
      const res = await applySetting.clone_size(profile, cloneSize.value[profile] || '')
      showToast(`Лимит клона профиля ${profile}.${restartSuffix(res)}`)
      overrideDirty.value[profile] = false
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
        <p>Локи рабочих стратегий по профилям. Новые тестовые desync и blob — во вкладке «Вручную».</p>
      </div>
      <div class="actions">
        <label class="field" style="min-width:160px">
          <span>Scope</span>
          <select :value="scope" :disabled="busyActive" @change="changeScope(($event.target as HTMLSelectElement).value)">
            <option v-for="s in scopeOptions" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
        <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'refresh-locks' }" @click="refresh">Обновить</button>
        <router-link class="ghost btn-link" to="/manual">Вручную</router-link>
        <button type="button" class="ghost" @click="focusSupersweep">К суперсвипу</button>
      </div>
    </div>

    <p v-if="scopes.warning" class="panel-d" style="color:var(--warn)">{{ scopes.warning }}</p>

    <h2 class="section-title">Локи профилей</h2>
    <div v-if="!statusLoaded" class="loading">Загрузка…</div>
    <div v-else class="strategy-grid">
      <article v-for="p in locks" :key="p.profile" class="strategy" :class="{ 'is-gated': gated(p) }">
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
          <p v-if="gated(p)" class="panel-d" style="color:var(--warn);margin-top:0.45rem">{{ gatedReason(p) }}</p>
        </div>
        <div class="row">
          <input
            type="number" min="0" :max="p.max_strategy || 999"
            :value="draft[String(p.profile)] ?? ''"
            :disabled="busyActive || gated(p)" placeholder="номер" style="max-width:7rem"
            @input="draft[String(p.profile)] = ($event.target as HTMLInputElement).value">
          <button type="button" class="primary" :disabled="busyActive || gated(p)"
            :class="{ 'is-busy': busyButton === `lock-${p.profile}` }"
            @click="save(String(p.profile), p)">Зафиксировать</button>
          <button type="button" class="ghost" :disabled="busyActive || gated(p)"
            :class="{ 'is-busy': busyButton === `clear-${p.profile}` }"
            @click="reset(String(p.profile), p)">Авто</button>
          <button type="button" class="ghost" :disabled="busyActive || gated(p)"
            :class="{ 'is-busy': busyButton === `check-${p.profile}` }"
            @click="check(String(p.profile), p)">Проверка</button>
        </div>
        <CheckResults
          v-if="!gated(p) && strategyChecks[String(p.profile)]"
          class="inline-check"
          :payload="strategyChecks[String(p.profile)]"
          empty-message="Нет результатов проверки."
          :empty-hidden="false"
        />
        <div v-if="canOverride(p)" class="grid-2" style="margin-top:0.75rem">
          <label class="field">
            <span>Режим фейков</span>
            <select v-model="fakeMode[String(p.profile)]" :disabled="busyActive"
              @change="markOverrideDirty(String(p.profile))">
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
            <select v-model="tlsProfile[String(p.profile)]" :disabled="busyActive"
              @change="markOverrideDirty(String(p.profile))">
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
            <input v-model="cloneSize[String(p.profile)]" :disabled="busyActive" placeholder="1200"
              @input="markOverrideDirty(String(p.profile))">
          </label>
          <div v-if="fakeMode[String(p.profile)] === 'clone'" class="actions" style="align-items:end">
            <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === `clone-${p.profile}` }"
              @click="saveCloneSize(String(p.profile))">Сохранить лимит</button>
          </div>
        </div>
      </article>
    </div>

    <h2 class="section-title" style="margin-top:1.5rem">Суперсвип</h2>
    <SupersweepPanel
      :scope="scope"
      :scopes="scopeOptions"
      @started="refresh"
      @cancelled="refresh"
    />
  </section>
</template>
