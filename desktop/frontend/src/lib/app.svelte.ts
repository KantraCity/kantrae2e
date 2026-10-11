import { Messenger, onEvent, errorText, type Chat, type LinkRequest, type Message, type State, type UIEvent, type UserHit } from './api'
import { readFileBase64, plural } from './util'

export type Toast = { id: number; text: string; kind: 'info' | 'error' | 'warning' | 'security' }

export type Modal =
  | { kind: 'newGroup' }
  | { kind: 'newDirect' }
  | { kind: 'approveLink'; deviceId: string }
  | { kind: 'recovery'; phrase: string }
  | { kind: 'chatInfo'; chatId: string }
  | { kind: 'verify'; username: string }
  | { kind: 'settings' }
  | { kind: 'relogin' }

const UNREAD_KEY = 'kantra.unread'

function loadUnread(): Record<string, number> {
  try {
    return JSON.parse(localStorage.getItem(UNREAD_KEY) || '{}')
  } catch {
    return {}
  }
}

class AppState {
  loaded = $state(false)
  state = $state<State | null>(null)
  chats = $state<Chat[]>([])
  currentId = $state<string | null>(null)
  messages = $state<Record<string, Message[]>>({})
  unread = $state<Record<string, number>>(loadUnread())
  toasts = $state<Toast[]>([])
  online = $state(false)
  modal = $state<Modal | null>(null)
  search = $state('')
  /** Global search results (people by username). */
  userHits = $state<UserHit[]>([])
  searching = $state(false)
  /** New devices of this account waiting for approval. */
  links = $state<LinkRequest[]>([])

  current = $derived(this.chats.find((c) => c.id === this.currentId) ?? null)
  loggedIn = $derived(!!this.state?.account)
  pending = $derived(!!this.state?.pending)

  private toastSeq = 0
  private unsubscribe: (() => void) | null = null

  async init() {
    this.unsubscribe?.()
    this.unsubscribe = onEvent((e) => this.handleEvent(e))
    window.addEventListener('popstate', () => {
      if (this.modal && this.modal.kind !== 'approveLink') this.modal = null
      if (!history.state?.chat) this.setCurrent(null)
    })
    // Web mode: the tab's visibility is "foreground" for notifications.
    document.addEventListener('visibilitychange', () => {
      if (this.state?.webMode) Messenger.SetForeground(!document.hidden).catch(() => {})
    })
    try {
      this.state = await Messenger.GetState()
      this.online = this.state.online
      if (this.state.account && !this.state.pending) {
        await this.refreshChats()
        await this.refreshLinks()
        await this.openTappedChat()
      }
    } catch (e) {
      this.toast(errorText(e), 'error')
    }
    this.loaded = true
  }

  private setCurrent(id: string | null) {
    this.currentId = id
    Messenger.SetActiveChat(id ?? '').catch(() => {})
  }

  /** A notification was tapped/clicked: show its chat. */
  async openTappedChat() {
    const id = await Messenger.TakeOpenChat().catch(() => '')
    if (!id || this.state?.pending) return
    if (!this.chats.some((c) => c.id === id)) await this.refreshChats()
    this.modal = null
    if (this.currentId !== id) {
      if (this.currentId) this.setCurrent(null)
      await this.openChat(id)
    }
  }

  /** Web mode: a browser notification (the Go side decided it is due). */
  private browserNotify(e: UIEvent) {
    if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
    const n = new Notification(e.title || 'Kantra', { body: e.body, tag: e.chatId, icon: '/icon.svg' })
    n.onclick = () => {
      window.focus()
      if (e.chatId) this.openChat(e.chatId)
      n.close()
    }
  }

  async afterAuth() {
    this.state = await Messenger.GetState()
    if (!this.state.pending) await this.refreshChats()
  }

  async refreshLinks() {
    const ls = await Messenger.PendingLinks().catch(() => null)
    if (!ls) return
    this.links = ls
    // Ask about the oldest request unless something else is open.
    if (ls.length && !this.modal) this.modal = { kind: 'approveLink', deviceId: ls[0].deviceId }
    if (this.modal?.kind === 'approveLink') {
      const id = this.modal.deviceId
      if (!ls.some((l) => l.deviceId === id)) this.modal = ls.length ? { kind: 'approveLink', deviceId: ls[0].deviceId } : null
    }
  }

  private searchSeq = 0

  /** Global search by username (debounced by the caller). */
  async searchUsers(q: string) {
    const seq = ++this.searchSeq
    q = q.trim().replace(/^@/, '')
    if (q.length < 2) {
      this.userHits = []
      this.searching = false
      return
    }
    this.searching = true
    const hits = await Messenger.SearchUsers(q).catch(() => [] as UserHit[])
    if (seq !== this.searchSeq) return
    this.userHits = hits ?? []
    this.searching = false
  }

  /** Opens (or starts) the private chat with a user, like tapping a person in Telegram. */
  async openDirect(username: string) {
    const chat = await this.try(() => Messenger.OpenDirect(username))
    if (!chat) return
    this.search = ''
    this.userHits = []
    if (!this.chats.some((c) => c.id === chat.id)) this.chats = [chat, ...this.chats]
    await this.openChat(chat.id)
    this.refreshChats()
  }

  toast(text: string, kind: Toast['kind'] = 'info', ms = 4000) {
    const id = ++this.toastSeq
    this.toasts.push({ id, text, kind })
    setTimeout(() => (this.toasts = this.toasts.filter((t) => t.id !== id)), ms)
  }

  /** Runs an API call and shows errors as toasts; returns undefined on error. */
  async try<T>(fn: () => Promise<T>): Promise<T | undefined> {
    try {
      return await fn()
    } catch (e) {
      const msg = errorText(e)
      if (/session expired|log in again/i.test(msg)) {
        this.modal = { kind: 'relogin' }
      } else {
        this.toast(msg, 'error', 6000)
      }
      return undefined
    }
  }

  async refreshChats() {
    const chats = await this.try(() => Messenger.Chats())
    if (chats) this.chats = chats
  }

  async openChat(id: string | null) {
    // A history entry per open chat: the Android back button (WebView
    // goBack) and the browser's back button return to the chat list.
    if (!id && history.state?.chat) {
      history.back() // popstate clears currentId
      return
    }
    if (id && !this.currentId) history.pushState({ chat: true }, '')
    this.setCurrent(id)
    if (!id) return
    this.unread[id] = 0
    this.saveUnread()
    await this.loadMessages(id)
  }

  async loadMessages(id: string) {
    const ms = await this.try(() => Messenger.Messages(id, 500))
    if (!ms) return
    // Keep messages that arrived via events while the request was running.
    const seen = new Set(ms.map((m) => m.id))
    const extra = (this.messages[id] ?? []).filter((m) => !seen.has(m.id))
    this.messages[id] = extra.length ? [...ms, ...extra].sort((a, b) => a.sentAt - b.sentAt || a.seq - b.seq) : ms
  }

  // Sends are queued so that quickly typed messages keep their order.
  private sendQueue: Promise<unknown> = Promise.resolve()

  send(text: string): Promise<boolean> {
    const id = this.currentId
    if (!id || !text.trim()) return Promise.resolve(false)
    const p = this.sendQueue.then(async () => {
      const msg = await this.try(() => Messenger.Send(id, text))
      if (!msg) return false
      this.addMessage(msg)
      return true
    })
    this.sendQueue = p
    return p
  }

  async sendFile(file: File) {
    const id = this.currentId
    if (!id) return
    if (file.size > 24 * 1024 * 1024) {
      this.toast('Файл больше 24 МБ', 'error')
      return
    }
    const b64 = await readFileBase64(file)
    const p = this.sendQueue.then(async () => {
      const msg = await this.try(() => Messenger.SendFile(id, file.name, b64))
      if (msg) this.addMessage(msg)
    })
    this.sendQueue = p
    await p
  }

  private addMessage(m: Message) {
    const list = this.messages[m.chatId]
    if (list) {
      if (!list.some((x) => x.id === m.id)) {
        list.push(m)
        list.sort((a, b) => a.sentAt - b.sentAt || a.seq - b.seq)
      }
    } else if (m.chatId === this.currentId) {
      // The chat is still loading: remember it, loadMessages merges it.
      this.messages[m.chatId] = [m]
    }
    const chat = this.chats.find((c) => c.id === m.chatId)
    if (chat) {
      if (!chat.last || m.sentAt >= chat.last.sentAt) {
        chat.last = m
        chat.updatedAt = m.sentAt
      }
      this.chats.sort((a, b) => b.updatedAt - a.updatedAt)
    } else {
      this.refreshChats()
    }
  }

  private saveUnread() {
    try {
      localStorage.setItem(UNREAD_KEY, JSON.stringify(this.unread))
    } catch { /* storage unavailable */ }
  }

  private async handleEvent(e: UIEvent) {
    switch (e.type) {
      case 'connected':
        this.online = true
        break
      case 'disconnected':
        this.online = false
        break
      case 'message':
        if (e.message) {
          this.addMessage(e.message)
          if (!e.message.outgoing && (e.chatId !== this.currentId || document.hidden)) {
            this.unread[e.chatId!] = (this.unread[e.chatId!] ?? 0) + 1
            this.saveUnread()
          }
        }
        break
      case 'group_joined':
      case 'group_updated':
        await this.refreshChats()
        break
      case 'removed':
        await this.refreshChats()
        this.toast('Вас удалили из группы', 'warning')
        break
      case 'history':
        if (e.count) {
          this.toast(`Получено ${e.count} ${plural(e.count, 'пропущенное сообщение', 'пропущенных сообщения', 'пропущенных сообщений')} от участников`, 'info')
        }
        await this.refreshChats()
        if (e.chatId && this.messages[e.chatId]) await this.loadMessages(e.chatId)
        break
      case 'security':
        this.toast(`Безопасность: ${e.detail ?? ''}`, 'security', 12000)
        await this.refreshChats()
        break
      case 'new_device':
        this.toast(`Новое устройство: ${e.detail ?? ''}. Сверьте отпечатки.`, 'warning', 9000)
        break
      case 'open_chat':
        await this.openTappedChat()
        break
      case 'notify':
        this.browserNotify(e)
        break
      case 'link_request':
      case 'link_resolved':
        await this.refreshLinks()
        break
      case 'approved':
        if (this.state?.pending) {
          await this.afterAuth()
          this.toast('Устройство подтверждено')
        }
        if (e.count) {
          this.toast(`Восстановлено ${e.count} ${plural(e.count, 'сообщение', 'сообщения', 'сообщений')} из резервной копии`)
        }
        await this.refreshChats()
        break
    }
  }
}

export const app = new AppState()
