// Small UI state that must survive a WebView reload (Android can recreate
// the page behind system dialogs): the open chat, drafts, the sign-up form.
// Browser storage may be unavailable; everything here is best effort.

function get<T>(key: string, fallback: T): T {
  try {
    const v = localStorage.getItem('kantra.' + key)
    return v == null ? fallback : (JSON.parse(v) as T)
  } catch {
    return fallback
  }
}

function set(key: string, value: unknown) {
  try {
    if (value == null || value === '') localStorage.removeItem('kantra.' + key)
    else localStorage.setItem('kantra.' + key, JSON.stringify(value))
  } catch {
    /* storage unavailable */
  }
}

// The chat that was open is restored only shortly after (a reload), not on
// a fresh start the next day.
const RESTORE_WINDOW = 2 * 60_000

export function saveOpenChat(userId: string | undefined, chatId: string | null) {
  if (userId) set('open.' + userId, chatId ? { chatId, at: Date.now() } : null)
}

export function restoreOpenChat(userId: string | undefined): string | null {
  if (!userId) return null
  const v = get<{ chatId: string; at: number } | null>('open.' + userId, null)
  return v && Date.now() - v.at < RESTORE_WINDOW ? v.chatId : null
}

export function loadDraft(chatId: string): string {
  return get<string>('draft.' + chatId, '')
}

export function saveDraft(chatId: string, text: string) {
  set('draft.' + chatId, text)
}

export type AuthDraft = { mode: 'login' | 'register'; firstName: string; lastName: string; username: string }
export function loadAuthDraft(): AuthDraft | null {
  return get<AuthDraft | null>('auth', null)
}
export function saveAuthDraft(d: AuthDraft | null) {
  set('auth', d)
}
