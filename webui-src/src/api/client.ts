export type ApiOptions = RequestInit & {
  /** Таймаут запроса; по умолчанию 120 с. 0 = без таймаута. */
  timeoutMs?: number
}

const DEFAULT_TIMEOUT_MS = 120_000

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
      throw new Error(String(data.error || `HTTP ${response.status}`))
    }
    // Часть CGI отдаёт 200 с ok:false / error — не считаем это успехом.
    if (data && typeof data === 'object' && 'ok' in data && data.ok === false) {
      throw new Error(String(data.error || 'Операция не выполнена'))
    }
    if (data && typeof data === 'object' && typeof data.error === 'string' && data.error && !('ok' in data)) {
      throw new Error(data.error)
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
