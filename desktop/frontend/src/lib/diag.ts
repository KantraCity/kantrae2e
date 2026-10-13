import { Messenger } from './api'

// UI errors go to the device's diagnostics log (desktop/diaglog.go): only the
// error text and where it happened, never message content. At most 20 a
// minute, so a loop of errors cannot flood the log.
let budget = 20
setInterval(() => (budget = 20), 60_000)

export function logClient(text: string) {
  if (budget <= 0) return
  budget--
  Messenger.LogClient(text).catch(() => {})
}

export function watchErrors() {
  window.addEventListener('error', (e) => {
    const where = e.filename ? ` @ ${e.filename.replace(location.origin, '')}:${e.lineno}:${e.colno}` : ''
    logClient(`script: ${e.message}${where}${e.error?.stack ? ' ' + String(e.error.stack).slice(0, 800) : ''}`)
  })
  window.addEventListener('unhandledrejection', (e) => {
    const r = e.reason
    logClient(`promise: ${r instanceof Error ? r.message + ' ' + (r.stack ?? '').slice(0, 800) : String(r)}`)
  })
}
