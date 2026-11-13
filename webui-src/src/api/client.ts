import { forceLogin } from '../stores/auth'

export type ApiOptions = RequestInit & {
  /** Таймаут запроса; по умолчанию 120 с. 0 = без таймаута. */
  timeoutMs?: number
}

const DEFAULT_TIMEOUT_MS = 120_000

function isLoginPath(path: string): boolean {
  return path === '/api/login' || path.startsWith('/api/login?')
    || path === '/cgi-bin/login.cgi' || path.startsWith('/cgi-bin/login.cgi?')
}

/** Soft-error: 200 + error рядом с другими полями (update_check) — не throw. */
function isBareErrorPayload(data: Record<string, unknown>): boolean {
  if (!data || typeof data !== 'object') return false
  if (typeof data.error !== 'string' || !data.error) return false
  if ('ok' in data) return false
  return Object.keys(data).every((k) => k === 'error')
}

export async function api<T = Record<string, unknown>>(path: string, options: ApiOptions = {}): Promise<T> {
  const { timeoutMs = DEFAULT_TIMEOUT_MS, signal: userSignal, ...rest } = options
  const ctrl = new AbortController()
  const onUserAbort = () => ctrl.abort()
  userSignal?.addEventListener('abort', onUserAbort)
  let timer: ReturnType<typeof setTimeout> | undefined
  if (timeoutMs > 0) {
    timer = setTimeout(() => ctrl.abort(), timeoutMs)
  }
  try {
    const response = await fetch(path, { ...rest, signal: ctrl.signal })
    let data: Record<string, unknown> = {}
    try {
      data = await response.json() as Record<string, unknown>
    } catch {
      data = {}
    }
    if (!response.ok) {
      if (response.status === 401) {
        if (!isLoginPath(path)) {
          forceLogin()
        }
        const err = new Error(String(data.error || 'Требуется авторизация')) as Error & { status?: number }
        err.status = 401
        throw err
      }
      throw new Error(String(data.error || `HTTP ${response.status}`))
    }
    // Часть CGI отдаёт 200 с ok:false / error — не считаем это успехом.
    if (data && typeof data === 'object' && 'ok' in data && data.ok === false) {
      throw new Error(String(data.error || 'Операция не выполнена'))
    }
    if (isBareErrorPayload(data)) {
      throw new Error(String(data.error))
    }
    return data as T
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') {
      throw new Error(userSignal?.aborted ? 'Запрос отменён' : 'Превышено время ожидания ответа')
    }
    throw e
  } finally {
    if (timer) clearTimeout(timer)
    userSignal?.removeEventListener('abort', onUserAbort)
  }
}

export function formBody(params: Record<string, string | number | boolean | undefined | null>) {
  const normalized: Record<string, string> = {}
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue
    if (typeof value === 'number' && !Number.isFinite(value)) {
      throw new Error(`Некорректное значение параметра ${key}`)
    }
    normalized[key] = String(value)
  }
  return {
    method: 'POST' as const,
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams(normalized),
  }
}

/** Скачать бинарный ответ API (бэкапы) с cookie; при 401 → login. */
export async function downloadBlob(path: string, filename: string): Promise<void> {
  const response = await fetch(path)
  if (response.status === 401) {
    if (!isLoginPath(path)) forceLogin()
    throw new Error('Требуется авторизация')
  }
  if (!response.ok) {
    let msg = `HTTP ${response.status}`
    try {
      const data = await response.json() as { error?: string }
      if (data.error) msg = data.error
    } catch { /* binary or empty */ }
    throw new Error(msg)
  }
  const blob = await response.blob()
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.rel = 'noopener'
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}
