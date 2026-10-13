import { mount } from 'svelte'
import './app.css'
import { applyTheme } from './lib/theme'
import { watchErrors } from './lib/diag'
import App from './App.svelte'
import { app } from './lib/app.svelte'

applyTheme()
watchErrors()

// No zooming of the interface: Ctrl/⌘ + wheel or +/-/0 in desktop
// webviews, pinch gestures where the viewport meta is ignored (Safari).
window.addEventListener('wheel', (e) => { if (e.ctrlKey || e.metaKey) e.preventDefault() }, { passive: false })
window.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && ['+', '=', '-', '_', '0'].includes(e.key)) e.preventDefault()
})
for (const ev of ['gesturestart', 'gesturechange', 'gestureend']) document.addEventListener(ev, (e) => e.preventDefault())
// Links never navigate the app's own page (a phone app has no "back" out
// of a web page): web links open in the system browser, in-app downloads
// go through Messenger.SaveMedia / SaveBytes.
document.addEventListener('click', (e) => {
  const a = (e.target as Element | null)?.closest?.('a[href]') as HTMLAnchorElement | null
  if (!a || e.defaultPrevented) return
  const href = a.getAttribute('href') ?? ''
  if (href.startsWith('#') || a.hasAttribute('download')) return
  e.preventDefault()
  if (/^(https?:|mailto:|tel:)/i.test(a.href)) app.openUrl(a.href)
})
mount(App, { target: document.getElementById('app')! })
