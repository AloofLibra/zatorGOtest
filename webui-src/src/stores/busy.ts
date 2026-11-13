import { ref } from 'vue'

export const busyActive = ref(false)
// ключ кнопки, запустившей операцию — для спиннера button.is-busy
export const busyButton = ref<string | null>(null)

let busyDepth = 0
const busyStack: (string | null)[] = []

/** Сериализует UI-busy со счётчиком: параллельные withBusy не сбрасывают чужой finally. */
export async function withBusy<T>(button: string | null, task: () => Promise<T>): Promise<T> {
  busyDepth++
  busyStack.push(button)
  busyActive.value = true
  busyButton.value = button
  try {
    return await task()
  } finally {
    busyStack.pop()
    busyDepth = Math.max(0, busyDepth - 1)
    busyButton.value = busyStack.length ? busyStack[busyStack.length - 1] : null
    busyActive.value = busyDepth > 0
  }
}
