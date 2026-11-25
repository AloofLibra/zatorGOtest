<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { clearLock, fetchH3Curl, h3CurlAction, profileCheck, setLock } from '../api/endpoints'
import type { H3CurlStatus, ProfileInfo } from '../api/types'
import { gatedPanel, gatedReason, isAutoModeGated, isProfileGated } from '../gating'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { confirmDialog } from '../stores/confirm'
import { tlsBlobSettings } from '../stores/settings'
import { fetchAndApplyState } from '../stores/state'
import { locks, refreshAll, scope, scopes, status, statusLoaded, strategyChecks } from '../stores/status'
import { showToast } from '../stores/toast'
import CheckResults from '../components/ui/CheckResults.vue'

type FilterMode = 'all' | 'active' | 'locked'
type DraftMap = Record<string, string | number>

const TLS_MODE_PROFILES = new Set(['1', '2', '3', '4', '8'])

const router = useRouter()
const route = useRoute()
const filterMode = ref<FilterMode>('all')
const draft = ref<DraftMap>({})
const h3Curl = ref<H3CurlStatus | null>(null)
const focusedId = ref('')
let focusTimer: number | undefined

const autoModeEnabled = computed(() => status.value?.auto_mode === 'включен')
const scopeOptions = computed(() => scopes.value.scopes || ['default'])

function gated(p: ProfileInfo) {
  return isProfileGated(p)
}

function isLocked(p: ProfileInfo) {
  return !!p.current_lock && p.current_lock !== 'auto'
}

const activeCount = computed(() => locks.value.filter((p) => !gated(p)).length)
const lockedCount = computed(() => locks.value.filter((p) => isLocked(p) && p.current_lock !== '0').length)

const visibleLocks = computed(() => {
  if (filterMode.value === 'active') return locks.value.filter((p) => !gated(p))
  if (filterMode.value === 'locked') return locks.value.filter((p) => isLocked(p) && p.current_lock !== '0')
  return locks.value
})

function draftText(profile: string): string {
  const v = draft.value[profile]
  return v === undefined || v === null ? '' : String(v).trim()
}

function lockText(p: ProfileInfo): string {
  const lock = String(p.current_lock || 'auto')
  return lock === 'auto' ? '' : lock
}

function draftDirty(p: ProfileInfo): boolean {
  const d = draftText(String(p.profile))
  return !!d && d !== lockText(p)
}

function modeBadge(p: ProfileInfo): { label: string; isClone: boolean } | null {
  const id = String(p.profile)
  if (id === '5') return { label: 'quic', isClone: false }
  if (!TLS_MODE_PROFILES.has(id)) return null
  const isClone = (tlsBlobSettings.value?.profile_modes?.[id] || 'classic') === 'clone'
  return { label: isClone ? 'clone' : 'blob', isClone }
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

watch(locks, (list) => syncDraftFromLocks(list, false), { immediate: true, deep: true })

function focusProfilesFromQuery() {
  const raw = String(route.query.focus || '').trim()
  if (!raw) return
  const first = raw.split(/[,\s]+/).map((x) => x.trim()).find(Boolean)
  if (!first) return
  filterMode.value = 'all'
  nextTick(() => {
    const el = document.getElementById(`strategy-${first}`)
    if (!el) return
    el.scrollIntoView({ block: 'center', behavior: 'smooth' })
    focusedId.value = first
    window.clearTimeout(focusTimer)
    focusTimer = window.setTimeout(() => { focusedId.value = '' }, 4000)
  })
}

async function loadH3Curl() {
  try {
    h3Curl.value = await fetchH3Curl()
  } catch {
    // необязательно для отображения остальных профилей
  }
}

onMounted(async () => {
  const q = String(route.query.scope || '')
  if (q && q !== scope.value) {
    scope.value = q
    try {
      await refreshAll()
    } catch (e) {
      showToast((e as Error).message, 'error')
    }
  }
  if (statusLoaded.value) focusProfilesFromQuery()
  void loadH3Curl()
})

watch(statusLoaded, (ok) => { if (ok) focusProfilesFromQuery() })
watch(() => route.query.focus, () => { if (statusLoaded.value) focusProfilesFromQuery() })

async function changeScope(v: string) {
  scope.value = v || 'default'
  void router.replace({ query: { ...route.query, scope: scope.value } })
  for (const key of Object.keys(strategyChecks)) delete strategyChecks[key]
  draft.value = {}
  try {
    await withBusy('scope-change', async () => {
      await refreshAll()
      syncDraftFromLocks(locks.value, true)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function refresh() {
  try {
    await withBusy('refresh-locks', async () => {
      await fetchAndApplyState()
      await loadH3Curl()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function save(profile: string, p: ProfileInfo) {
  if (gated(p)) {
    showToast(gatedReason(p), 'error')
    return
  }
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
  if (n === 0) {
    const ok = await confirmDialog({
      title: `Выключить профиль ${profile}?`,
      message: `${p.label}: обработка профиля будет отключена для scope «${scope.value}».`,
      danger: true,
      confirmText: 'Выключить',
    })
    if (!ok) return
  }
  try {
    const { lockRes, checkPayload } = await withBusy(`lock-${profile}`, async () => {
      const lockRes = await setLock(profile, n, scope.value)
      draft.value[profile] = String(n)
      delete strategyChecks[profile]
      await refreshAll()
      syncDraftFromLocks(locks.value, true)
      let checkPayload: Awaited<ReturnType<typeof profileCheck>> | null = null
      if (n !== 0) {
        try {
          checkPayload = await profileCheck(profile, scope.value, { waitApply: true, expectLock: String(n) })
          strategyChecks[profile] = checkPayload
        } catch (e) {
          showToast(`Лок записан, проверка не выполнена: ${(e as Error).message}`, 'warning')
        }
      }
      return { lockRes, checkPayload }
    })
    const changed = lockRes.changed ? `${lockRes.previous || '?'}→${lockRes.lock}` : `лок ${n}`
    if (n === 0) {
      showToast(`Профиль ${profile} выключен (${changed})`)
      return
    }
    if (checkPayload?.applied === false) {
      showToast(`Профиль ${profile}: store не подтвердил лок ${n} (сейчас ${checkPayload.lock})`, 'error')
      return
    }
    const v = checkPayload?.results?.[0]?.verdict
    if (v === 'ok') showToast(`Профиль ${profile}: ${changed} — цель доступна`)
    else if (v === 'warn') showToast(`Профиль ${profile}: ${changed} — с предупреждением`, 'warning')
    else if (v === 'fail') showToast(`Профиль ${profile}: ${changed} записан, цель недоступна`, 'warning')
    else if (checkPayload?.message) showToast(`Профиль ${profile}: ${changed}. ${checkPayload.message}`, 'info')
    else showToast(`Профиль ${profile}: ${changed}`)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function clear(profile: string) {
  try {
    await withBusy(`clear-${profile}`, async () => {
      await clearLock(profile, scope.value)
      delete strategyChecks[profile]
      await refreshAll()
      syncDraftFromLocks(locks.value, true)
    })
    showToast(`Профиль ${profile}: фиксация снята (auto)`)
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
      const expect = String(p.current_lock || 'auto')
      const payload = await profileCheck(profile, scope.value, {
        waitApply: true,
        expectLock: expect === 'auto' ? undefined : expect,
      })
      strategyChecks[profile] = payload
      if (payload.applied === false) {
        showToast(`Лок в store ${payload.lock} ≠ UI ${expect}`, 'error')
        return
      }
      const v = payload.results?.[0]?.verdict
      if (v === 'ok') showToast(`Профиль ${profile}: цель доступна`)
      else if (v === 'warn') showToast(`Профиль ${profile}: предупреждение при проверке`, 'warning')
      else if (v === 'fail') showToast(`Профиль ${profile}: цель недоступна`, 'warning')
      else if (v === 'unknown') showToast(payload.message || `Профиль ${profile}: поток не подтверждён`, 'info')
      else if (payload.message) showToast(payload.message, 'info')
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function runH3Curl(action: 'install' | 'update' | 'remove') {
  if (action === 'remove') {
    const ok = await confirmDialog({
      title: 'Удалить управляемый h3-curl?',
      message: 'HTTP/3-проверки и автоподбор QUIC профиля 5 перестанут работать до повторной установки.',
      danger: true,
      confirmText: 'Удалить',
    })
    if (!ok) return
  }
  try {
    await withBusy(`h3-${action}`, async () => {
      const res = await h3CurlAction(action)
      h3Curl.value = { found: res.found, path: res.path, arch: res.arch, managed: res.managed }
      showToast(action === 'remove' ? 'h3-curl удалён' : action === 'update' ? 'h3-curl обновлён' : 'h3-curl установлен')
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}
</script>

<template>
  <section class="page-stack">
    <div class="page-head">
      <div>
        <span class="eyebrow">Профили трафика</span>
        <h1>Стратегии</h1>
        <p>Фиксация стратегий и проверка доступности по профилям; пусто означает <code>auto</code>.</p>
      </div>
      <div class="actions">
        <label v-if="scopes.enabled" class="field scope-picker">
          <span>Scope</span>
          <select :value="scope" :disabled="busyActive" @change="changeScope(($event.target as HTMLSelectElement).value)">
            <option v-for="s in scopeOptions" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
        <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'refresh-locks' }" @click="refresh">Обновить</button>
        <router-link class="btn-link" to="/sweep">Подбор →</router-link>
      </div>
    </div>

    <div v-if="scopes.warning" class="notice-banner is-warn" role="status">
      <span>{{ scopes.warning }}</span>
    </div>

    <div v-if="autoModeEnabled" class="notice-banner" role="status">
      <span>Авторотация включена — профили 1–4 подбираются автоматически.</span>
      <router-link class="btn-link compact-link" to="/settings/modes">Режимы →</router-link>
    </div>

    <div class="filter-bar">
      <div class="tabs-bar" role="tablist" aria-label="Фильтр профилей">
        <button type="button" role="tab" :aria-selected="filterMode === 'all'"
          class="tab-pill" :class="{ active: filterMode === 'all' }" @click="filterMode = 'all'">
          Все <span class="tab-count">{{ locks.length }}</span>
        </button>
        <button type="button" role="tab" :aria-selected="filterMode === 'active'"
          class="tab-pill" :class="{ active: filterMode === 'active' }" @click="filterMode = 'active'">
          Активные <span class="tab-count">{{ activeCount }}</span>
        </button>
        <button type="button" role="tab" :aria-selected="filterMode === 'locked'"
          class="tab-pill" :class="{ active: filterMode === 'locked' }" @click="filterMode = 'locked'">
          Зафиксированные <span class="tab-count">{{ lockedCount }}</span>
        </button>
      </div>
      <router-link class="bar-link" to="/blobs-sni">Блобы и SNI →</router-link>
    </div>

    <div v-if="!statusLoaded" class="loading">Загрузка…</div>
    <section v-else class="panel matrix-panel" aria-label="Список профилей">
      <p v-if="!visibleLocks.length" class="panel-d" style="margin:0">
        Нет профилей, соответствующих выбранному фильтру.
      </p>
      <div v-else class="strat-list">
        <article v-for="p in visibleLocks" :id="`strategy-${p.profile}`" :key="p.profile"
          class="strat-item" :class="{ 'is-gated': gated(p), 'is-target': focusedId === String(p.profile) }">
          <div class="strat-main">
            <span class="prof-id">{{ p.profile }}</span>
            <div class="strat-info">
              <div class="strat-title-line">
                <strong>{{ p.label }}</strong>
                <span class="chip" :class="p.current_lock === '0' ? 'is-bad' : isLocked(p) ? 'on' : ''">
                  {{ p.current_lock === '0' ? 'выключен' : isLocked(p) ? `лок #${p.current_lock}` : 'auto' }}
                </span>
                <span v-if="modeBadge(p)" class="chip mono" :class="{ on: modeBadge(p)!.isClone }">
                  [{{ modeBadge(p)!.label }}]
                </span>
                <span v-if="p.is_fallback" class="chip">fb {{ p.fallback_enabled ? 'on' : 'off' }}</span>
                <span v-if="p.is_udp_games" class="chip">games {{ p.udp_games_enabled ? 'on' : 'off' }}</span>
                <span v-if="p.is_dns_desync" class="chip">dns {{ p.dns_desync_enabled ? 'on' : 'off' }}</span>
              </div>
              <div class="strat-sub">
                <span>макс. № {{ p.max_strategy ?? '—' }}</span>
                <span v-if="p.description"> · {{ p.description }}</span>
                <span v-if="draftDirty(p)" class="draft-warn"> · черновик {{ draftText(String(p.profile)) }}</span>
              </div>
            </div>

            <div class="strat-controls">
              <template v-if="gated(p)">
                <span class="gate-hint">{{ isAutoModeGated(p) ? 'авторотация' : 'отключён' }}</span>
                <router-link v-if="gatedPanel(p)" class="btn-link compact-link" :to="`/settings/${gatedPanel(p)}`">
                  Настроить →
                </router-link>
              </template>
              <template v-else>
                <input
                  class="strat-num-input mono"
                  type="number" min="0" :max="p.max_strategy || 999"
                  :value="draft[String(p.profile)] ?? ''"
                  :disabled="busyActive || autoModeEnabled"
                  placeholder="auto"
                  aria-label="Номер стратегии"
                  @input="draft[String(p.profile)] = ($event.target as HTMLInputElement).value"
                  @keydown.enter.prevent="save(String(p.profile), p)"
                >
                <button v-if="autoModeEnabled && isAutoModeGated(p)" type="button" class="ghost" disabled>Авто</button>
                <button v-else type="button"
                  :class="[draftDirty(p) ? 'primary' : '', { 'is-busy': busyButton === `lock-${p.profile}` }]"
                  :disabled="busyActive"
                  @click="save(String(p.profile), p)">Лок</button>
                <button v-if="isLocked(p) && !(autoModeEnabled && isAutoModeGated(p))" type="button" class="ghost"
                  :class="{ 'is-busy': busyButton === `clear-${p.profile}` }"
                  :disabled="busyActive"
                  @click="clear(String(p.profile))">Сброс</button>
                <button type="button" class="ghost"
                  :class="{ 'is-busy': busyButton === `check-${p.profile}` }"
                  :disabled="busyActive"
                  @click="check(String(p.profile), p)">Check</button>
              </template>
            </div>
          </div>

          <div v-if="String(p.profile) === '5'" class="h3-strip">
            <div class="h3-info">
              <span>HTTP/3 (<code>h3-curl</code>):</span>
              <span class="chip" :class="{ on: !!h3Curl?.found }" :title="h3Curl?.path || ''">
                {{ h3Curl ? (h3Curl.found ? `установлен${h3Curl.arch ? ` · ${h3Curl.arch}` : ''}` : 'не установлен') : 'проверка…' }}
              </span>
            </div>
            <div class="actions">
              <button v-if="h3Curl && !h3Curl.found" type="button" class="primary compact-btn"
                :disabled="busyActive" :class="{ 'is-busy': busyButton === 'h3-install' }"
                @click="runH3Curl('install')">Установить h3-curl</button>
              <template v-else-if="h3Curl?.found">
                <button type="button" class="ghost compact-btn" :disabled="busyActive"
                  :class="{ 'is-busy': busyButton === 'h3-update' }"
                  @click="runH3Curl('update')">Обновить</button>
                <button v-if="h3Curl.managed" type="button" class="ghost danger compact-btn" :disabled="busyActive"
                  :class="{ 'is-busy': busyButton === 'h3-remove' }"
                  @click="runH3Curl('remove')">Удалить</button>
              </template>
            </div>
          </div>

          <p v-if="!gated(p) && (p.is_fallback || p.is_udp_games)" class="row-note">
            {{ p.is_fallback ? 'Проверка TLS не подтверждает fallback для всех доменов.' : 'Проверка TLS не подтверждает игровой UDP-трафик.' }}
          </p>

          <div v-if="!gated(p) && strategyChecks[String(p.profile)]" class="check-tray">
            <CheckResults
              :payload="strategyChecks[String(p.profile)]"
              empty-message="Нет результатов проверки."
              :empty-hidden="false"
            />
            <button type="button" class="ghost compact-btn" style="justify-self:end"
              @click="delete strategyChecks[String(p.profile)]">Скрыть</button>
          </div>
        </article>
      </div>
    </section>
  </section>
</template>

<style scoped>
.scope-picker { margin: 0; min-width: 7.5rem; }
.notice-banner {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.65rem;
  padding: 0.65rem 0.9rem;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  font-size: 0.85rem;
}
.notice-banner.is-warn {
  border-color: color-mix(in srgb, var(--warn) 55%, var(--line));
  color: var(--warn);
}
.filter-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.6rem;
}
.tabs-bar { display: flex; flex-wrap: wrap; gap: 0.4rem; }
.tab-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0.45rem 0.85rem;
  border-radius: 999px;
  border: 1px solid var(--line);
  background: var(--surface-2);
  color: var(--muted);
  font-size: 0.84rem;
  font-weight: 500;
}
.tab-pill:hover { color: var(--ink); }
.tab-pill.active {
  border-color: transparent;
  background: var(--accent);
  color: var(--accent-ink);
  font-weight: 600;
}
.tab-count { font-family: var(--font-mono); font-size: 0.74rem; opacity: 0.85; }
.bar-link { color: var(--accent); font-size: 0.84rem; font-weight: 600; }
.bar-link:hover { text-decoration: underline; }

.strat-list { display: grid; gap: 0.5rem; }
.strat-item {
  display: grid;
  gap: 0.5rem;
  padding: 0.7rem 0.85rem;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--bg1) 45%, transparent);
  transition: border-color 150ms var(--ease);
}
.strat-item.is-target { border-color: var(--accent); }
.strat-item.is-gated { opacity: 0.72; }
.strat-main {
  display: grid;
  grid-template-columns: 2rem minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.65rem;
}
.prof-id {
  display: inline-grid;
  place-items: center;
  width: 1.8rem;
  height: 1.8rem;
  border-radius: 6px;
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  color: var(--accent);
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.82rem;
}
.strat-info { min-width: 0; display: grid; gap: 0.22rem; }
.strat-title-line { display: flex; flex-wrap: wrap; align-items: center; gap: 0.35rem 0.45rem; }
.strat-title-line strong { font-size: 0.92rem; }
.strat-sub { color: var(--muted); font-size: 0.78rem; }
.draft-warn { color: var(--warn); font-weight: 600; }
.chip.is-bad { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 40%, var(--line)); }

.strat-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem;
  justify-content: flex-end;
}
.strat-num-input { width: 4.8rem; padding: 0.42rem 0.5rem; text-align: center; }
.gate-hint { color: var(--muted); font-size: 0.8rem; }
.compact-link, .compact-btn { padding: 0.35rem 0.65rem; font-size: 0.8rem; }

.h3-strip {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  padding: 0.45rem 0.6rem;
  border-top: 1px dashed var(--line);
  font-size: 0.8rem;
}
.h3-info { display: flex; flex-wrap: wrap; align-items: center; gap: 0.4rem; color: var(--muted); }
.row-note { margin: 0; color: var(--muted); font-size: 0.78rem; }
.check-tray {
  display: grid;
  gap: 0.4rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--line);
}

@media (max-width: 820px) {
  .strat-main { grid-template-columns: 2rem minmax(0, 1fr); }
  .strat-controls { grid-column: 1 / -1; justify-content: flex-start; }
}
</style>
