import { ref } from 'vue'
import { api, formBody } from '../api/client'
import { fetchSetting, fetchState } from '../api/endpoints'
import type { UpdateCheckResult, VersionInfo } from '../api/types'
import { confirmDialog } from './confirm'
import { fetchAndApplyState } from './state'
import { scope, versionInfo, versionInfoCheckedAt, versionInfoFresh } from './status'
import { showToast } from './toast'
import { withBusy } from './busy'

export type UpdateCheckState = 'never' | 'checking' | 'success' | 'error'
export type UpdateMode = 'app' | 'config' | 'full'

const CHECK_INTERVAL = 6 * 60 * 60 * 1000

export const updateCheckState = ref<UpdateCheckState>('never')
export const updateCheckResult = ref<UpdateCheckResult | null>(null)
export const updateCheckError = ref('')
export const updateLastAttemptAt = ref<number | null>(null)
export const updateLastSuccessAt = ref<number | null>(null)
export const updateApplying = ref(false)
export const updateMode = ref<UpdateMode>('app')

let inFlight: Promise<void> | null = null
let timer: number | undefined
let dueAt = 0
let schedulerStarted = false
let overdue = false

export function hydrateUpdateCheck(version: VersionInfo | null) {
  if (!version || updateCheckState.value !== 'never') return
  if (version.latest_release || version.latest_zator_date || version.latest_webui_date) {
    updateCheckResult.value = {
      update_available: version.update_available,
      release: version.latest_release,
      latest_zator_date: version.latest_zator_date,
      latest_webui_date: version.latest_webui_date,
    }
  }
}

function clearTimer() {
  if (timer !== undefined) window.clearTimeout(timer)
  timer = undefined
}

function scheduleNext() {
  clearTimer()
  if (!schedulerStarted) return
  dueAt = Date.now() + CHECK_INTERVAL
  timer = window.setTimeout(() => {
    timer = undefined
    if (document.hidden) {
      overdue = true
      return
    }
    void refreshUpdateCheck()
  }, CHECK_INTERVAL)
}

function visibilityChanged() {
  if (!document.hidden && (overdue || Date.now() >= dueAt)) {
    overdue = false
    clearTimer()
    void refreshUpdateCheck()
  }
}

export function startUpdateScheduler() {
  stopUpdateScheduler()
  schedulerStarted = true
  overdue = false
  dueAt = Date.now() + CHECK_INTERVAL
  document.addEventListener('visibilitychange', visibilityChanged)
  timer = window.setTimeout(() => {
    timer = undefined
    if (document.hidden) overdue = true
    else void refreshUpdateCheck()
  }, CHECK_INTERVAL)
  // A full HTML load starts a fresh check after bootstrap; SPA navigation does not.
  void refreshUpdateCheck()
}

export function stopUpdateScheduler() {
  schedulerStarted = false
  clearTimer()
  document.removeEventListener('visibilitychange', visibilityChanged)
}

export function refreshUpdateCheck(): Promise<void> {
  if (inFlight) return inFlight
  updateCheckState.value = 'checking'
  updateCheckError.value = ''
  updateLastAttemptAt.value = Date.now()
  inFlight = (async () => {
    try {
      const result = await fetchSetting.update_check()
      if (result.error) throw new Error(result.error)
      updateCheckResult.value = result
      updateCheckState.value = 'success'
      updateLastSuccessAt.value = result.checked_at ? Date.parse(result.checked_at) || Date.now() : Date.now()
      if (versionInfo.value) {
        versionInfo.value = {
          ...versionInfo.value,
          update_available: result.update_available,
          latest_release: result.release || versionInfo.value.latest_release,
          latest_zator_date: result.latest_zator_date || versionInfo.value.latest_zator_date,
          latest_webui_date: result.latest_webui_date || versionInfo.value.latest_webui_date,
        }
      }
    } catch (error) {
      updateCheckState.value = 'error'
      updateCheckError.value = (error as Error).message || 'Не удалось проверить обновления'
    } finally {
      // /api/state recalculates local config mtime state; apply only its version
      // so a background check cannot replace drafts in SettingsView.
      versionInfoFresh.value = false
      try {
        const { version } = await fetchState(scope.value)
        versionInfo.value = version
        versionInfoCheckedAt.value = Date.now()
        versionInfoFresh.value = true
      } catch (error) {
        if ((error as Error & { status?: number }).status === 401) stopUpdateScheduler()
      }
    }
  })().finally(() => {
    inFlight = null
    scheduleNext()
  })
  return inFlight
}

interface UpdatePreflight {
  ram_available: number
  tmp_available: number
  tmp_required: number
  tmp_base_required: number
  flash_available: number
  flash_required: number
  zapret_flash_available: number
  zapret_flash_required: number
  can_rollback: boolean
  can_proceed_without_backup: boolean
  can_update: boolean
}

export async function applyUpdate(mode: UpdateMode = updateMode.value) {
  if (updateApplying.value) return
  updateApplying.value = true
  try {
  const pre = await api<{ preflight?: UpdatePreflight }>('/api/settings', formBody({ setting: 'update_preflight', mode }))
  if (!pre.preflight?.can_update) {
    showToast('Недостаточно свободной flash памяти для обновления', 'error')
    return
  }
  if (!pre.preflight.can_proceed_without_backup) {
    showToast(`Для самого обновления в /tmp нужно около ${pre.preflight.tmp_base_required} байт`, 'error')
    return
  }
  const noBackup = !pre.preflight.can_rollback
  const ok = await confirmDialog({
    title: mode === 'app' ? 'Обновить Zator?' : mode === 'config' ? 'Обновить конфигурационные ресурсы?' : 'Обновить всё?',
    message: noBackup
      ? [`RAM: ${pre.preflight.ram_available}; /tmp: ${pre.preflight.tmp_available}; flash Zator: ${pre.preflight.flash_available}; flash zapret2: ${pre.preflight.zapret_flash_available} байт. Для полного отката нужно около ${pre.preflight.tmp_required} байт в /tmp. Продолжить без резервной копии на свой риск?`]
      : [`Будут скачаны файлы релиза. RAM: ${pre.preflight.ram_available}; /tmp: ${pre.preflight.tmp_available}; flash Zator: ${pre.preflight.flash_available}; flash zapret2: ${pre.preflight.zapret_flash_available} байт.`],
  })
  if (!ok) return

    await withBusy('upd-apply', async () => {
      showToast('Скачиваем и устанавливаем обновление…', 'info')
      const res = await api<{
        needs_confirmation?: boolean
        warning?: string
        up_to_date?: boolean
        installed?: number
        release?: string
        restart_required?: boolean
      }>('/api/settings', {
        ...formBody({ setting: 'update_apply', mode, continue_without_backup: noBackup ? '1' : '0' }),
        timeoutMs: 200_000,
      })
      if (res.needs_confirmation) {
        showToast(res.warning || 'Свободного места в /tmp стало недостаточно; обновление не начато', 'warning')
      } else if (res.up_to_date) {
        showToast('Уже актуально')
        await fetchAndApplyState()
        hydrateUpdateCheck(versionInfo.value)
      } else if (res.restart_required) {
        showToast(`Обновлено (файлов: ${res.installed ?? 0}). Перезапуск WebUI…`)
        // The server has already committed the new release metadata. Refresh
        // the check now so a delayed WebUI restart cannot leave the old
        // release badge active and invite the user to apply the same update again.
        await refreshUpdateCheck()
      } else {
        showToast(`Обновлено (файлов: ${res.installed ?? 0})`)
        await fetchAndApplyState()
        hydrateUpdateCheck(versionInfo.value)
      }
    })
  } catch (error) {
    // Network loss does not imply that update_apply succeeded.
    showToast((error as Error).message || 'Результат обновления не подтверждён; обновите состояние', 'error')
  } finally {
    updateApplying.value = false
  }
}
