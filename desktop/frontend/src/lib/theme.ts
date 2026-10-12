// Light / dark / system theme (per device, like Telegram). The page keeps
// the dark palette in :root; data-theme="light" switches the variables.
export type ThemePref = 'dark' | 'light' | 'system'

const KEY = 'kantra.theme'
const media = window.matchMedia('(prefers-color-scheme: light)')

export function themePref(): ThemePref {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch { /* storage unavailable */ }
  return 'dark'
}

export function applyTheme(pref = themePref()) {
  const light = pref === 'light' || (pref === 'system' && media.matches)
  document.documentElement.dataset.theme = light ? 'light' : 'dark'
  document.querySelector('meta[name="color-scheme"]')?.setAttribute('content', light ? 'light' : 'dark')
}

export function setThemePref(pref: ThemePref) {
  try { localStorage.setItem(KEY, pref) } catch { /* not persisted */ }
  applyTheme(pref)
}

media.addEventListener('change', () => applyTheme())
