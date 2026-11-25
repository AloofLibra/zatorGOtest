import { ref } from 'vue'
import { fetchZapretReleases, switchZapretVersion } from '../api/endpoints'
import type { ZapretReleasesPayload } from '../api/types'
import { withBusy } from './busy'
import { confirmDialog } from './confirm'
import { fetchAndApplyState } from './state'
import { versionInfo } from './status'
import { announceRestart, restartSuffix, showToast } from './toast'

export type ZapretReleasesState = 'idle' | 'loading' | 'loaded' | 'error'
export type ZapretSource = 'fork' | 'official'

export const zapretReleases = ref<ZapretReleasesPayload | null>(null)
export const zapretReleasesState = ref<ZapretReleasesState>('idle')
export const zapretReleasesError = ref('')

let inFlight: Promise<void> | null = null

export function zapretBranchLabel(src: ZapretSource | '' | undefined): string {
  if (src === 'fork') return 'reasm-fix (MarkinAlexander)'
  if (src === 'official') return 'Обычная (bol-van)'
  return 'неизвестна'
}

export function zapretBranchShort(src: ZapretSource | '' | undefined): string {
  if (src === 'fork') return 'reasm-fix'
  if (src === 'official') return 'обычная'
  return ''
}

function splitVersion(raw: string): { nums: number[]; prefix: string; suffixNum: number } {
  const clean = String(raw || '').trim().replace(/^v/i, '')
  const dash = clean.indexOf('-')
  const base = dash >= 0 ? clean.slice(0, dash) : clean
  const suffix = dash >= 0 ? clean.slice(dash + 1) : ''
  const nums = base.split('.').map((p) => {
    const n = Number.parseInt(p, 10)
    return Number.isFinite(n) ? n : 0
  })
  if (!suffix) return { nums, prefix: '', suffixNum: 0 }
  const m = /^(.*?)(\d+)$/.exec(suffix)
  if (m) {
    return { nums, prefix: m[1], suffixNum: Number.parseInt(m[2], 10) }
  }
  return { nums, prefix: suffix, suffixNum: 1 }
}

export function compareZapretVersion(candidate: string, current: string): number {
  const a = splitVersion(candidate)
  const b = splitVersion(current)
  const len = Math.max(a.nums.length, b.nums.length)
  for (let i = 0; i < len; i++) {
    const av = a.nums[i] ?? 0
    const bv = b.nums[i] ?? 0
    if (av !== bv) return av > bv ? 1 : -1
  }
  if (a.prefix === b.prefix) {
    if (a.suffixNum !== b.suffixNum) return a.suffixNum > b.suffixNum ? 1 : -1
    return 0
  }
  return a.prefix.localeCompare(b.prefix)
}

export function actionLabelForRelease(
  relSource: ZapretSource,
  relVersion: string,
  curSource: ZapretSource | '' | undefined,
  curVersion: string | undefined,
): string {
  if (curSource && relSource !== curSource) return 'Переключить'
  if (!curVersion || curVersion === 'self-built') return 'Установить'
  if (relVersion === curVersion) return 'Переустановить'
  const cmp = compareZapretVersion(relVersion, curVersion)
  if (cmp > 0) return 'Обновить'
  if (cmp < 0) return 'Откатить'
  return 'Переустановить'
}

export function loadZapretReleases(refresh = false): Promise<void> {
  if (inFlight && !refresh) return inFlight
  const p = (async () => {
    zapretReleasesState.value = 'loading'
    zapretReleasesError.value = ''
    try {
      const payload = await fetchZapretReleases(refresh)
      zapretReleases.value = payload
      zapretReleasesState.value = 'loaded'
    } catch (e) {
      zapretReleasesError.value = (e as Error).message || 'Не удалось загрузить список релизов'
      zapretReleasesState.value = 'error'
    }
  })()
  inFlight = p.finally(() => {
    if (inFlight === p) inFlight = null
  })
  return inFlight
}

export async function installZapretRelease(source: ZapretSource, version: string): Promise<void> {
  const curVersion = zapretReleases.value?.installed.version || versionInfo.value?.zapret2_version || ''
  const curSource = (zapretReleases.value?.installed.source || versionInfo.value?.zapret2_source || '') as ZapretSource | ''
  const action = actionLabelForRelease(source, version, curSource, curVersion)
  const sameBuild = curSource === source && curVersion === version

  const ok = await confirmDialog({
    title: `${action} zapret2?`,
    message: [
      `Текущая: ${curVersion ? `v${curVersion}` : 'не определена'}${curSource ? ` (${zapretBranchShort(curSource)})` : ''}`,
      `Целевая: v${version} · ${zapretBranchLabel(source)}`,
      'Бинарники и встроенные Lua-скрипты zapret2 будут заменены, служба zapret2 перезапустится.',
    ],
    confirmText: action,
  })
  if (!ok) return

  try {
    await withBusy('zapret-version', async () => {
      announceRestart()
      const res = await switchZapretVersion(source, version, sameBuild)
      await fetchAndApplyState()
      if (zapretReleases.value) {
        zapretReleases.value = {
          ...zapretReleases.value,
          installed: { version: res.version, source: res.source, arch: res.arch },
        }
      }
      showToast(`zapret2 v${res.version} (${zapretBranchShort(res.source)}) установлен.${restartSuffix(res)}`)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}
