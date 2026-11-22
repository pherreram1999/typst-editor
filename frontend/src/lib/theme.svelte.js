// Tema de la interfaz: claro (por defecto), oscuro o solarizado. Se guarda en
// localStorage y se aplica como <html data-theme="…"> (los colores están en
// app.css). index.html lo aplica antes de pintar para evitar un destello.
const KEY = 'typsteditor.theme'

export const THEMES = [
  { key: 'light', label: 'Claro', meta: '#1e40af' },
  { key: 'dark', label: 'Oscuro', meta: '#0f1a2e' },
  { key: 'solarized', label: 'Solarizado', meta: '#073642' },
]

function read() {
  try {
    const t = localStorage.getItem(KEY)
    return THEMES.some((x) => x.key === t) ? t : 'light'
  } catch {
    return 'light'
  }
}

export const theme = $state({ current: read() })

function apply(key) {
  const el = document.documentElement
  if (key === 'light') delete el.dataset.theme
  else el.dataset.theme = key
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', THEMES.find((x) => x.key === key).meta)
}

export function setTheme(key) {
  theme.current = key
  apply(key)
  try {
    localStorage.setItem(KEY, key)
  } catch {}
}

apply(theme.current)
