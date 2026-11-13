import { ref } from 'vue'
import { status } from './status'

export type ToastType = 'success' | 'error' | 'info' | 'warning'

export interface ToastState {
  id: number
  message: string
  type: ToastType
}

export const toast = ref<ToastState | null>(null)

export function showToast(message: string, type: ToastType = 'success') {
  toast.value = { id: Date.now() + Math.random(), message, type }
}

export function announceRestart() {
  if (status.value?.zapret2_running) showToast('Применяем настройку — перезапускаем zapret2…', 'info')
}

export function restartSuffix(payload: { restarted?: boolean; restart_required?: boolean } | null) {
  if (payload?.restarted) return ' zapret2 перезапущен.'
  if (payload?.restart_required === false) return ''
  return ' Применится при следующем запуске zapret2.'
}
