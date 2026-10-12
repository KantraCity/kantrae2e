import { Messenger, onEvent, errorText, type AccountInfo, type Chat, type LinkRequest, type LinkView, type Message, type State, type UIEvent, type UserHit, type UserProfile } from './api'
import { plural } from './util'
import { MAX_FILE, prepareFile, prepareSticker, type Prepared } from './media'
import { restoreOpenChat, saveOpenChat } from './persist'

export type Toast = { id: number; text: string; kind: 'info' | 'error' | 'warning' | 'security' }

export type Modal =
  | { kind: 'newGroup' }
  | { kind: 'newDirect' }
  | { kind: 'approveLink'; deviceId: string }
  | { kind: 'recovery'; phrase: string }
  | { kind: 'drawer' }
  | { kind: 'forward'; chatId: string; ids: string[] }
  | { kind: 'sendFiles'; files: File[] }
  | { kind: 'viewer'; m: Message }
  | { kind: 'deleteMsg'; chatId: string; ids: string[]; canAll: boolean }
  | { kind: 'editProfile' }
  | { kind: 'logout' }
  | { kind: 'chatInfo'; chatId: string }
  | { kind: 'verify'; username: string }
  | { kind: 'settings' }
  | { kind: 'relogin' }
  | { kind: 'deleteChat'; chatId: string; action: 'delete' | 'clear' | 'leave' }
  | { kind: 'schedule'; chatId: string; text: string; silent: boolean; link: LinkView | null }
  | { kind: 'scheduled'; chatId: string }
  | { kind: 'poll'; chatId: string }
  | { kind: 'pinMsg'; m: Message }
  | { kind: 'passcode'; mode: 'set' | 'change' | 'off' }

// Unread counters per account (localStorage, this device only).
const unreadKey = (userId?: string) => 'kantra.unread' + (userId ? '.' + userId : '')

function loadUnread(userId?: string): Record<string, number> {
  try {
    return JSON.parse(localStorage.getItem(unreadKey(userId)) || '{}')
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
  unread = $state<Record<string, number>>({})
  /** Composer state: replying to / editing a message of the open chat. */
  reply = $state<Message | null>(null)
  editing = $state<Message | null>(null)
  /** "typing…" per chat: who and until when. */
  typing = $state<Record<string, { name: string; until: number }>>({})
  /** Local blob URLs of media sent from this device (no re-download). */
  localUrls = $state<Record<string, string>>({})
  /** Messages being uploaded (temporary ids). */
  sending = $state<Record<string, boolean>>({})
  /** Message context menu. */
  menu = $state<{ m: Message; x: number; y: number } | null>(null)
  /** Context menu of a chat in the list. */
  chatMenu = $state<{ chat: Chat; x: number; y: number } | null>(null)
  /** This account's profile and all accounts on this device. */
  me = $state<UserProfile | null>(null)
  accounts = $state<AccountInfo[]>([])
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
    await this.loadPasscode()
    // A fresh page (browser reload, Android activity recreated) is a new
    // start: ask for the passcode like on launch.
    if (this.passcode.enabled && !this.locked) {
      this.locked = true
      Messenger.Lock().catch(() => {})
    }
    window.addEventListener('popstate', () => {
      if (this.modal && this.modal.kind !== 'approveLink') this.modal = null
      if (!history.state?.chat) this.setCurrent(null)
    })
    // Android back button: close the dialog, then the chat; false = leave the app.
    ;(window as any).kantraBack = () => {
      if (this.modal && this.modal.kind !== 'approveLink' && this.modal.kind !== 'relogin') {
        this.modal = null
        return true
      }
      if (this.currentId) {
        this.openChat(null)
        return true
      }
      return false
    }
    // Web mode: the tab's visibility is "foreground" for notifications.
    document.addEventListener('visibilitychange', () => {
      if (this.state?.webMode) Messenger.SetForeground(!document.hidden).catch(() => {})
      if (!document.hidden && this.currentId) this.markRead()
      if (document.hidden) saveOpenChat(this.state?.account?.userId, this.currentId)
    })
    await this.loadAccount()
    this.loaded = true
    // Web mode: an open, visible tab is "online" (the desktop window and the
    // Android activity report focus themselves).
    if (this.state?.webMode) Messenger.SetForeground(!document.hidden).catch(() => {})
  }

  /** (Re)loads everything for the active account (start, account switch). */
  async loadAccount() {
    this.chats = []
    this.messages = {}
    this.currentId = null
    this.links = []
    this.saved = { gif: [], sticker: [] }
    this.savedLoaded = { gif: false, sticker: false }
    this.me = null
    this.search = ''
    this.userHits = []
    if (this.modal?.kind !== 'relogin') this.modal = null
    if (history.state?.chat) history.replaceState(null, '')
    try {
      this.state = await Messenger.GetState()
      this.online = this.state.online
      this.unread = loadUnread(this.state.account?.userId)
      await this.refreshAccounts()
      if (this.state.account && !this.state.pending) {
        await this.refreshChats()
        await this.refreshLinks()
        if (!(await this.openTappedChat())) {
          // The page was reloaded (e.g. Android recreated it): reopen the chat.
          const id = restoreOpenChat(this.state.account.userId)
          if (id && this.chats.some((c) => c.id === id)) await this.openChat(id)
        }
        this.loadMe()
        this.loadBlocked()
      }
    } catch (e) {
      this.toast(errorText(e), 'error')
    }
  }

  async refreshAccounts() {
    this.accounts = (await Messenger.Accounts().catch(() => null)) ?? []
  }

  async loadMe() {
    const p = await Messenger.MyProfile().catch(() => null)
    if (p) this.me = p
  }

  async switchAccount(id: string) {
    await this.try(() => Messenger.SwitchAccount(id)) // "account_switched" reloads
  }

  async addAccount() {
    await this.try(() => Messenger.AddAccount())
  }

  private setCurrent(id: string | null) {
    this.currentId = id
    saveOpenChat(this.state?.account?.userId, id)
    Messenger.SetActiveChat(id ?? '').catch(() => {})
  }

  /** A notification was tapped/clicked: show its chat. */
  async openTappedChat(): Promise<boolean> {
    const id = await Messenger.TakeOpenChat().catch(() => '')
    if (!id || this.state?.pending) return false
    if (!this.chats.some((c) => c.id === id)) await this.refreshChats()
    this.modal = null
    if (this.currentId !== id) {
      if (this.currentId) this.setCurrent(null)
      await this.openChat(id)
    }
    return true
  }

  /** Web mode: a browser notification (the Go side decided it is due). */
  private browserNotify(e: UIEvent) {
    if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
    // image: the photo/GIF/video preview (Chrome, Edge, Android browsers).
    const opts = { body: e.body, tag: e.chatId, icon: '/icon.svg', image: e.image || undefined } as NotificationOptions
    const n = new Notification(e.title || 'Kantra', opts)
    n.onclick = () => {
      window.focus()
      if (e.chatId) Messenger.OpenNotification(e.chatId) // key "<account>/<chat>"
      n.close()
    }
  }

  async afterAuth() {
    this.state = await Messenger.GetState()
    this.unread = loadUnread(this.state.account?.userId)
    await this.refreshAccounts()
    if (this.state.account && !this.state.pending) {
      await this.refreshChats()
      this.loadMe()
    }
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
  /** A direct chat being opened (created) for this username. */
  opening = $state('')

  async openDirect(username: string) {
    // Already in the list: open it at once.
    const known = this.chats.find((c) => c.kind === 'direct' && c.peer === username)
    if (known) {
      this.search = ''
      this.userHits = []
      await this.openChat(known.id)
      return
    }
    // Creating it takes a moment: the chat that was open must not keep its
    // composer (a message typed now would go to the wrong chat).
    this.opening = username
    if (this.currentId) {
      this.reply = null
      this.editing = null
      this.currentId = null
    }
    const chat = await this.try(() => Messenger.OpenDirect(username)).finally(() => (this.opening = ''))
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
    if (id && !this.currentId && !history.state?.chat) history.pushState({ chat: true }, '')
    if (id !== this.currentId) {
      this.reply = null
      this.editing = null
      this.selected = []
    }
    this.setCurrent(id)
    if (!id) return
    this.unread[id] = 0
    this.saveUnread()
    await this.loadMessages(id)
    this.markRead()
  }

  async loadMessages(id: string) {
    const ms = await this.try(() => Messenger.Messages(id, 500))
    if (!ms) return
    // Keep messages that arrived via events while the request was running.
    // (Only those: an older cached message missing from the answer was
    // deleted meanwhile and must go.)
    const seen = new Set(ms.map((m) => m.id))
    const newest = ms.length ? ms[ms.length - 1].sentAt : 0
    const extra = (this.messages[id] ?? []).filter((m) => !seen.has(m.id) && (m.id.startsWith('local-') || m.sentAt > newest))
    this.messages[id] = extra.length ? [...ms, ...extra].sort((a, b) => a.sentAt - b.sentAt || a.seq - b.seq) : ms
  }

  // Sends are queued so that quickly typed messages keep their order.
  private sendQueue: Promise<unknown> = Promise.resolve()

  /** Messages that failed to send (temporary id -> retry). */
  failed = $state<Record<string, () => void>>({})

  /**
   * Sends (or saves an edit) optimistically: the message is on screen at
   * once with a clock, the network round trip happens in the background.
   */
  send(text: string, opts: { silent?: boolean; link?: LinkView | null } = {}): Promise<boolean> {
    const chatId = this.currentId
    if (!chatId || !text.trim()) return Promise.resolve(false)
    const editing = this.editing
    const reply = this.reply
    this.reply = null
    this.editing = null
    if (editing) {
      const list = this.messages[chatId]
      const i = list?.findIndex((x) => x.id === editing.id) ?? -1
      const before = i >= 0 ? { ...list![i] } : null
      if (i >= 0) list![i] = { ...list![i], text, edited: true }
      this.sending[editing.id] = true
      const p = this.sendQueue.then(async () => {
        const ok = await this.try(() => Messenger.EditMessage(chatId, editing.id, text))
        delete this.sending[editing.id]
        if (ok === undefined && before && i >= 0) list![i] = before
        return ok !== undefined
      })
      this.sendQueue = p
      return Promise.resolve(true)
    }
    const tmp = 'local-' + Math.random().toString(36).slice(2)
    const temp = {
      seq: 0, id: tmp, chatId, senderId: this.state?.account?.userId ?? '', sender: this.me?.name ?? '', kind: 'text', text,
      sentAt: Date.now(), outgoing: true, origin: '', silent: opts.silent, link: opts.link ?? undefined,
      reply: reply ? { id: reply.id, sender: reply.sender, text: reply.text || reply.file?.caption || reply.file?.name || '' } : undefined,
    } as Message
    const attempt = () => {
      delete this.failed[tmp]
      this.sending[tmp] = true
      const p = this.sendQueue.then(async () => {
        let msg: Message | null = null
        try {
          msg = await Messenger.SendWith(chatId, text, { replyTo: reply?.id ?? '', silent: !!opts.silent, link: opts.link ?? null })
        } catch (e) {
          this.toast(errorText(e), 'error', 5000)
        }
        delete this.sending[tmp]
        const list = this.messages[chatId]
        const i = list?.findIndex((x) => x.id === tmp) ?? -1
        if (!msg) {
          this.failed[tmp] = attempt // tap the red mark to retry
          return
        }
        // Replace in place: no jump, the clock turns into a check mark.
        if (list && i >= 0) list[i] = msg
        else this.addMessage(msg)
        this.touchChat(msg)
      })
      this.sendQueue = p
    }
    this.addMessage(temp)
    attempt()
    return Promise.resolve(true)
  }

  /** Moves the chat to the top of the list with its new last message. */
  private touchChat(m: Message) {
    const chat = this.chats.find((c) => c.id === m.chatId)
    if (!chat) return
    chat.last = m
    chat.updatedAt = m.sentAt
    this.chats.sort(chatOrder)
  }


  /** Re-reads one message after an edit/deletion/reaction (null: deleted). */
  async refreshMessage(chatId: string, id: string) {
    const list = this.messages[chatId]
    const m = await Messenger.GetMessage(chatId, id).catch(() => undefined)
    if (m === undefined) return
    if (list) {
      const i = list.findIndex((x) => x.id === id)
      if (m === null) {
        if (i >= 0) list.splice(i, 1)
      } else if (i >= 0) {
        list[i] = m
      }
      // Replies quoting it show the new text.
      for (let j = 0; j < list.length; j++) {
        if (list[j].reply?.id === id) {
          const r = await Messenger.GetMessage(chatId, list[j].id).catch(() => null)
          if (r) list[j] = r
        }
      }
    }
    const chat = this.chats.find((c) => c.id === chatId)
    if (chat?.last?.id === id) this.refreshChats()
  }

  /** Reaction, applied on screen first. */
  async react(m: Message, emoji: string) {
    const mine = m.reactions?.find((r) => r.mine)
    const next = mine?.emoji === emoji ? '' : emoji // tapping your reaction again removes it
    const list = this.messages[m.chatId]
    const i = list?.findIndex((x) => x.id === m.id) ?? -1
    if (list && i >= 0) {
      const rs = (list[i].reactions ?? []).map((r) => ({ ...r }))
      if (mine) {
        const r = rs.find((x) => x.emoji === mine.emoji)!
        r.count--
        r.mine = false
      }
      if (next) {
        const r = rs.find((x) => x.emoji === next)
        if (r) { r.count++; r.mine = true } else rs.push({ emoji: next, count: 1, mine: true })
      }
      list[i] = { ...list[i], reactions: rs.filter((r) => r.count > 0) }
    }
    if ((await this.try(() => Messenger.React(m.chatId, m.id, next))) === undefined) await this.refreshMessage(m.chatId, m.id)
  }

  /** Deletes / clears / leaves a chat; the list updates first (undone on failure). */
  async chatAction(chatId: string, action: 'delete' | 'clear' | 'leave', forAll: boolean) {
    const chats = this.chats
    const msgs = this.messages[chatId]
    if (action !== 'clear') {
      if (this.currentId === chatId) this.openChat(null)
      this.chats = chats.filter((c) => c.id !== chatId)
      delete this.unread[chatId]
      this.saveUnread()
    }
    this.messages[chatId] = []
    try {
      if (action === 'delete') await Messenger.DeleteChat(chatId, forAll)
      else if (action === 'clear') await Messenger.ClearHistory(chatId, forAll)
      else await Messenger.LeaveGroup(chatId)
      if (action === 'clear') await this.refreshChats()
    } catch (e) {
      this.chats = chats
      if (msgs) this.messages[chatId] = msgs
      this.toast(errorText(e), 'error')
    }
  }

  /** Deletion, removed from the screen first (restored if it fails). */
  async deleteMessages(chatId: string, ids: string[], forAll: boolean) {
    const list = this.messages[chatId] ?? []
    const removed = list.filter((x) => ids.includes(x.id))
    this.messages[chatId] = list.filter((x) => !ids.includes(x.id))
    const ok = await this.try(() => Messenger.DeleteMessages(chatId, ids, forAll))
    if (ok === undefined) {
      this.messages[chatId] = [...(this.messages[chatId] ?? []), ...removed].sort((a, b) => a.sentAt - b.sentAt || a.seq - b.seq)
    }
    const chat = this.chats.find((c) => c.id === chatId)
    if (chat?.last && ids.includes(chat.last.id)) this.refreshChats()
  }


  startReply(m: Message) {
    this.editing = null
    this.reply = m
  }

  startEdit(m: Message) {
    this.reply = null
    this.editing = m
  }

  private readTimer: ReturnType<typeof setTimeout> | undefined

  /** Read receipt for the open chat (debounced; only when really visible). */
  markRead() {
    clearTimeout(this.readTimer)
    this.readTimer = setTimeout(() => {
      const id = this.currentId
      if (id && !document.hidden) Messenger.MarkRead(id).catch(() => {})
    }, 400)
  }

  private lastTyping = 0

  /** Called on input: at most one "typing" signal per 4 s. */
  typingTick() {
    const id = this.currentId
    if (!id || Date.now() - this.lastTyping < 4000) return
    this.lastTyping = Date.now()
    Messenger.Typing(id).catch(() => {})
  }

  typingIn(chatId: string): string | null {
    const t = this.typing[chatId]
    return t && t.until > Date.now() ? t.name : null
  }


  /** When the page itself last received a file drop (see "files_dropped"). */
  lastDomDrop = 0

  /** Files dropped onto the desktop window (macOS/Linux deliver them natively). */
  private async filesDropped(list: { url: string; name: string; mime: string }[]) {
    if (Date.now() - this.lastDomDrop < 3000) return // the page already got them
    if (!this.current?.active) {
      this.toast('Откройте чат, чтобы отправить файлы', 'info')
      return
    }
    try {
      const files = await Promise.all(list.map(async (f) => {
        const blob = await (await fetch(f.url)).blob()
        return new File([blob], f.name, { type: f.mime || blob.type })
      }))
      this.pickFiles(files)
    } catch {
      this.toast('Не удалось прочитать файл', 'error')
    }
  }

  /** Opens the "send files" dialog (caption, as photo / as file). */
  pickFiles(files: File[]) {
    const ok = files.filter((f) => f.size <= MAX_FILE)
    if (ok.length < files.length) this.toast('Файлы больше 64 МБ не отправляются', 'error')
    if (ok.length) this.modal = { kind: 'sendFiles', files: ok }
  }

  async sendPrepared(files: File[], compress: boolean, caption: string, asSticker = false) {
    // Several photos/videos sent "as media" go out as one album (one grid).
    const visual = (f: File) => f.type.startsWith('image/') || f.type.startsWith('video/')
    const album = compress && !asSticker && files.length > 1 && files.every(visual) ? 'al-' + Math.random().toString(36).slice(2, 12) : ''
    for (let i = 0; i < files.length; i++) {
      try {
        let p: Prepared
        if (asSticker) {
          const upload = await prepareSticker(files[i])
          const blob = await (await fetch('data:' + upload.mime + ';base64,' + upload.base64)).blob()
          p = { upload, blob, localUrl: URL.createObjectURL(blob) }
        } else {
          p = await prepareFile(files[i], compress)
        }
        // Like Telegram: the caption goes with the first item.
        if (i === 0) p.upload.caption = caption
        if (album && (p.upload.kind === 'photo' || p.upload.kind === 'video')) p.upload.album = album
        await this.sendMedia(p)
      } catch {
        this.toast(`Не удалось подготовить ${files[i].name}`, 'error')
      }
    }
  }

  /** Sends prepared media; it shows up at once with a progress spinner. */
  sendMedia(p: Prepared): Promise<void> {
    const chatId = this.currentId
    if (!chatId) return Promise.resolve()
    const replyTo = this.reply?.id ?? ''
    this.reply = null
    const tmp = 'local-' + Math.random().toString(36).slice(2)
    const u = p.upload
    const temp = {
      seq: 0, id: tmp, chatId, senderId: this.state?.account?.userId ?? '', sender: '', kind: 'file', text: '',
      sentAt: Date.now(), outgoing: true, origin: '',
      file: { name: u.name, size: p.blob.size, kind: u.kind, mime: u.mime, w: u.w, h: u.h, dur: u.dur,
        thumb: u.thumb ? 'data:image/jpeg;base64,' + u.thumb : '', wave: u.wave, caption: u.caption, url: p.localUrl, album: u.album },
    } as Message
    this.localUrls[tmp] = p.localUrl
    this.sending[tmp] = true
    this.addMessage(temp)
    const run = this.sendQueue.then(async () => {
      const msg = await this.try(() => Messenger.SendMedia(chatId, u, replyTo))
      const list = this.messages[chatId]
      const i = list?.findIndex((x) => x.id === tmp) ?? -1
      delete this.sending[tmp]
      if (!msg) {
        if (list && i >= 0) list.splice(i, 1)
        return
      }
      this.localUrls[msg.id] = p.localUrl
      if (list && i >= 0) list.splice(i, 1)
      this.addMessage(msg)
    })
    this.sendQueue = run
    return run
  }

  /** Joins a group by its invite link (search box, a link in a message). */
  async joinByLink(link: string) {
    const id = await this.try(() => Messenger.JoinByLink(link.trim()))
    if (id === undefined) return
    if (this.chats.some((c) => c.id === id)) this.openChat(id)
    else this.toast('Запрос отправлен — вас добавят, как только кто-то из участников будет в сети', 'info', 6000)
  }

  // ---- pinned messages of the open chat ----
  pinned = $state<Record<string, Message[]>>({})
  async loadPinned(chatId: string) {
    this.pinned[chatId] = (await Messenger.Pinned(chatId).catch(() => null)) ?? []
  }
  async pin(m: Message, pin: boolean, forAll: boolean) {
    const ok = await this.try(() => Messenger.Pin(m.chatId, m.id, pin, forAll))
    if (ok === undefined) return
    await this.loadPinned(m.chatId)
    await this.refreshMessage(m.chatId, m.id)
  }

  // ---- jumping to a message (reply quote, search, pinned bar) ----
  /** MessageList scrolls to it and flashes it. */
  jump = $state<{ chatId: string; id: string; n: number } | null>(null)
  private jumpSeq = 0
  async jumpTo(chatId: string, id: string) {
    // Older than what is loaded: load more history first.
    let list = this.messages[chatId] ?? []
    for (const limit of [2000, 10000, 50000]) {
      if (list.some((m) => m.id === id)) break
      const ms = await Messenger.Messages(chatId, limit).catch(() => null)
      if (!ms) break
      this.messages[chatId] = ms
      list = ms
      if (ms.length < limit) break
    }
    if (!list.some((m) => m.id === id)) {
      this.toast('Сообщение не найдено', 'info')
      return
    }
    this.jump = { chatId, id, n: ++this.jumpSeq }
  }

  // ---- local passcode (desktop/passcode.go) ----
  locked = $state(false)
  passcode = $state<{ enabled: boolean; autoLock: number }>({ enabled: false, autoLock: 0 })
  async loadPasscode() {
    const st = await Messenger.GetPasscode().catch(() => null)
    if (!st) return
    this.passcode = { enabled: st.enabled, autoLock: st.autoLock }
    this.locked = st.locked
  }
  lock() {
    Messenger.Lock().catch(() => {})
  }

  // ---- pinned / muted chats (synced across the account's devices) ----
  async pinChat(chat: Chat, pin: boolean) {
    if ((await this.try(() => Messenger.PinChat(chat.id, pin))) === undefined) return
    chat.pinned = pin ? Date.now() : 0
    this.chats.sort(chatOrder)
  }
  /** minutes: -1 forever, 0 unmute. */
  async muteChat(chat: Chat, minutes: number) {
    if ((await this.try(() => Messenger.MuteChat(chat.id, minutes))) === undefined) return
    chat.mutedUntil = minutes < 0 ? 253402300799000 : minutes > 0 ? Date.now() + minutes * 60000 : 0
  }

  // ---- scheduled messages (sent by this device at their time) ----
  /** Bumped when the list may have changed (scheduled event, own changes). */
  scheduledTick = $state(0)
  async schedule(chatId: string, text: string, at: number, silent: boolean, link: LinkView | null) {
    const ok = await this.try(() => Messenger.Schedule(chatId, text, at, { replyTo: '', silent, link }))
    if (ok === undefined) return false
    this.scheduledTick++
    this.toast('Сообщение будет отправлено ' + new Date(at).toLocaleString('ru', { day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' }))
    return true
  }

  // ---- selecting several messages ----
  selected = $state<string[]>([])
  toggleSelect(id: string) {
    this.selected = this.selected.includes(id) ? this.selected.filter((x) => x !== id) : [...this.selected, id]
  }

  // ---- block list (ids), for the "Разблокировать" bar of a direct chat ----
  blocked = $state<string[]>([])
  async loadBlocked() {
    this.blocked = ((await Messenger.Blocked().catch(() => null)) ?? []).map((u) => u.userId)
  }
  async setBlocked(userId: string, on: boolean) {
    const ok = await this.try(() => (on ? Messenger.Block(userId) : Messenger.Unblock(userId)))
    if (ok === undefined) return false
    this.blocked = on ? [...new Set([...this.blocked, userId])] : this.blocked.filter((x) => x !== userId)
    return true
  }

  // ---- presence ("в сети / был(а)") of people, refreshed while visible ----
  presence = $state<Record<string, { online: boolean; lastSeen: number; hidden: boolean }>>({})
  async refreshPresence(userIds: string[]) {
    const ids = userIds.filter(Boolean)
    if (!ids.length) return
    const list = await Messenger.Presence(ids).catch(() => null)
    for (const p of list ?? []) this.presence[p.userId] = { online: p.online, lastSeen: p.lastSeen, hidden: p.hidden }
  }

  // ---- saved GIFs and stickers (no packs: what you liked, you keep) ----
  saved = $state<{ gif: Message[]; sticker: Message[] }>({ gif: [], sticker: [] })
  savedLoaded = { gif: false, sticker: false }

  async loadSaved(kind: 'gif' | 'sticker') {
    const list = await Messenger.Saved(kind).catch(() => null)
    if (list) {
      this.saved[kind] = list
      this.savedLoaded[kind] = true
    }
  }

  /** Saves a message's GIF/sticker (a photo as a sticker, a short video as a GIF). */
  async saveAs(m: Message, kind: 'gif' | 'sticker') {
    try {
      await Messenger.SaveAs(m.chatId, m.id, kind)
      this.toast(kind === 'gif' ? 'GIF сохранён' : 'Стикер сохранён')
      await this.loadSaved(kind)
    } catch (e) {
      this.toast(errorText(e), 'error')
    }
  }

  /** Adds a file of one's own to the saved GIFs / stickers. */
  async addSaved(file: File, kind: 'gif' | 'sticker') {
    try {
      const up = kind === 'sticker' ? await prepareSticker(file) : (await prepareFile(file, true)).upload
      if (kind === 'gif' && up.kind !== 'gif' && up.kind !== 'video') throw new Error('Нужен файл GIF или короткое видео')
      up.kind = kind
      await Messenger.AddSaved(up)
      await this.loadSaved(kind)
    } catch (e) {
      this.toast(errorText(e), 'error')
    }
  }

  async removeSaved(g: Message) {
    const kind = (g.file?.kind === 'sticker' ? 'sticker' : 'gif') as 'gif' | 'sticker'
    const before = this.saved[kind]
    this.saved[kind] = before.filter((x) => x.id !== g.id)
    try {
      await Messenger.RemoveSavedGIF(g.id)
    } catch (e) {
      this.saved[kind] = before
      this.toast(errorText(e), 'error')
    }
  }

  /** Sends a saved GIF/sticker: shown at once, nothing is uploaded again. */
  async sendGif(g: Message) {
    const chatId = this.currentId
    if (!chatId) return
    const replyTo = this.reply?.id ?? ''
    this.reply = null
    const tmp = 'local-' + Math.random().toString(36).slice(2)
    const temp: Message = { ...g, id: tmp, chatId, outgoing: true, sentAt: Date.now(), seq: 0, read: false, reactions: [],
      forwarded: '', reply: undefined, origin: '' }
    this.sending[tmp] = true
    this.addMessage(temp)
    this.touchChat(temp)
    try {
      const msg = await Messenger.SendSavedGIF(chatId, g.id, replyTo)
      const list = this.messages[chatId]
      const i = list?.findIndex((x) => x.id === tmp) ?? -1
      if (list && i >= 0 && msg) list[i] = msg
      else if (msg) this.addMessage(msg)
    } catch (e) {
      const list = this.messages[chatId]
      const i = list?.findIndex((x) => x.id === tmp) ?? -1
      if (list && i >= 0) list.splice(i, 1)
      this.toast(errorText(e), 'error')
    } finally {
      delete this.sending[tmp]
    }
  }

  /** Where to load a media message from (local copy if we sent it). */
  mediaUrl(m: Message): string {
    return this.localUrls[m.id] ?? m.file?.url ?? ''
  }

  addMessage(m: Message) {
    const list = this.messages[m.chatId]
    if (list) {
      if (!list.some((x) => x.id === m.id)) {
        const last = list[list.length - 1]
        list.push(m)
        // Usually already in order: sort only when it is not.
        if (last && (m.sentAt < last.sentAt || (m.sentAt === last.sentAt && m.seq < last.seq))) {
          list.sort((a, b) => a.sentAt - b.sentAt || a.seq - b.seq)
        }
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
      this.chats.sort(chatOrder)
    } else {
      this.refreshChats()
    }
  }

  private saveUnread() {
    try {
      localStorage.setItem(unreadKey(this.state?.account?.userId), JSON.stringify(this.unread))
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
          if (e.chatId) delete this.typing[e.chatId] // the message arrived: no longer typing
          if (e.message.kind === 'system') {
            // group events: not unread
          } else if (!e.message.outgoing && (e.chatId !== this.currentId || document.hidden)) {
            this.unread[e.chatId!] = (this.unread[e.chatId!] ?? 0) + 1
            this.saveUnread()
          } else if (!e.message.outgoing && e.chatId === this.currentId) {
            this.markRead()
          }
        }
        break
      case 'read_self':
        // Read on another device of ours.
        if (e.chatId && this.unread[e.chatId]) {
          this.unread[e.chatId] = 0
          this.saveUnread()
        }
        break
      case 'message_updated':
        if (e.chatId && e.detail) await this.refreshMessage(e.chatId, e.detail)
        if (e.chatId && this.pinned[e.chatId]) await this.loadPinned(e.chatId)
        break
      case 'locked':
        this.locked = true
        this.menu = null
        this.chatMenu = null
        break
      case 'chatprefs':
        await this.refreshChats()
        break
      case 'scheduled':
        this.scheduledTick++
        break
      case 'read':
        if (e.chatId && this.messages[e.chatId]) {
          const upto = await Messenger.ReadUpTo(e.chatId).catch(() => 0)
          for (const m of this.messages[e.chatId]) if (m.outgoing && m.sentAt <= upto) m.read = true
        }
        break
      case 'typing':
        if (e.chatId) {
          this.typing[e.chatId] = { name: e.detail ?? '', until: Date.now() + 6000 }
          const chat = e.chatId
          setTimeout(() => { if ((this.typing[chat]?.until ?? 0) <= Date.now()) delete this.typing[chat] }, 6100)
        }
        break
      case 'group_joined':
      case 'group_updated':
        await this.refreshChats()
        // e.g. "X left the group" was stored locally
        if (e.chatId && e.chatId === this.currentId) await this.loadMessages(e.chatId)
        break
      case 'removed': {
        const known = this.chats.some((c) => c.id === e.chatId) // not if we left it ourselves
        await this.refreshChats()
        if (known) this.toast('Вас удалили из группы', 'warning')
        break
      }
      case 'chat_cleared':
        // Deleted/cleared on another device of ours (or by the other person).
        await this.refreshChats()
        if (e.chatId && this.messages[e.chatId]) await this.loadMessages(e.chatId)
        if (e.chatId && this.currentId === e.chatId && !this.chats.some((c) => c.id === e.chatId)) this.openChat(null)
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
      case 'account_switched':
        await this.loadAccount()
        break
      case 'accounts':
        await this.refreshAccounts()
        this.loadMe()
        break
      case 'open_chat':
        await this.openTappedChat()
        break
      case 'gifs':
        if (this.savedLoaded.gif) await this.loadSaved('gif')
        if (this.savedLoaded.sticker) await this.loadSaved('sticker')
        break
      case 'files_dropped':
        if (e.files?.length) this.filesDropped(e.files)
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

/** Chat list order: pinned chats first (last pinned on top), then newest. */
export function chatOrder(a: Chat, b: Chat): number {
  const pa = a.pinned ?? 0, pb = b.pinned ?? 0
  if (!!pa !== !!pb) return pa ? -1 : 1
  if (pa !== pb) return pb - pa
  return b.updatedAt - a.updatedAt
}
