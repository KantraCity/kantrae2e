// Thin wrapper over the generated Wails bindings. The same calls work in the
// desktop window and in the browser (server mode): the Wails runtime picks
// the transport.
import { Events } from '@wailsio/runtime'
import * as Messenger from '../../bindings/github.com/kantracity/kantrae2e/desktop/messenger'
import type { UIEvent } from '../../bindings/github.com/kantracity/kantrae2e/desktop/models'

export * from '../../bindings/github.com/kantracity/kantrae2e/desktop/models'
export { Messenger }

export function onEvent(fn: (e: UIEvent) => void): () => void {
  return Events.On('kantra', (ev) => fn(ev.data as UIEvent))
}

export function errorText(e: unknown): string {
  const raw = e instanceof Error ? e.message : typeof e === 'object' && e && 'message' in e ? String((e as any).message) : String(e)
  // Wails wraps Go errors; show the useful part.
  try {
    const parsed = JSON.parse(raw)
    if (parsed?.message) return parsed.message
  } catch { /* not JSON */ }
  return raw
    .replace(/^(unauthenticated|permission_denied|not_found|invalid_argument|already_exists|unavailable|aborted|internal): /, '')
}
