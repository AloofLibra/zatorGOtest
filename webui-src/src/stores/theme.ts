import { ref, watch } from 'vue'

export type ThemeMode = 'auto' | 'light' | 'dark'

function loadTheme(): ThemeMode {
  try {
    const saved = localStorage.getItem('zatorgo-theme') || localStorage.getItem('z2r-theme')
    if (saved === 'light' || saved === 'dark' || saved === 'auto') return saved
  } catch {
    // ignore
  }
  const current = document.documentElement.dataset.theme
  if (current === 'light' || current === 'dark' || current === 'auto') return current
  return 'light'
}

export const theme = ref<ThemeMode>(loadTheme())

watch(theme, (value) => {
  document.documentElement.dataset.theme = value
  try {
    localStorage.setItem('zatorgo-theme', value)
    localStorage.setItem('z2r-theme', value) // совместимость со старыми закладками
  } catch {
    // без хранилища тема действует до перезагрузки страницы
  }
}, { immediate: true })
