import { mount } from 'svelte'
import './app.css'
import { applyTheme } from './lib/theme'
import App from './App.svelte'

applyTheme()

// No zooming of the interface: Ctrl/⌘ + wheel or +/-/0 in desktop
// webviews, pinch gestures where the viewport meta is ignored (Safari).
window.addEventListener('wheel', (e) => { if (e.ctrlKey || e.metaKey) e.preventDefault() }, { passive: false })
window.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && ['+', '=', '-', '_', '0'].includes(e.key)) e.preventDefault()
})
for (const ev of ['gesturestart', 'gesturechange', 'gestureend']) document.addEventListener(ev, (e) => e.preventDefault())
mount(App, { target: document.getElementById('app')! })
