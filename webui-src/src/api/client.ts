export async function api<T = Record<string, unknown>>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, options)
  let data: Record<string, unknown> = {}
  try {
    data = await response.json() as Record<string, unknown>
  } catch {
    data = {}
  }
  if (!response.ok) {
    throw new Error(String(data.error || `HTTP ${response.status}`))
  }
  return data as T
}

export function formBody(params: Record<string, string | number | boolean | undefined | null>) {
  const normalized: Record<string, string> = {}
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue
    normalized[key] = String(value)
  }
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams(normalized),
  }
}
