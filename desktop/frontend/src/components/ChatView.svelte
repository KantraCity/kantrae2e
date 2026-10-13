<script lang="ts">
  import { tick } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import { lastSeenText, plural } from '../lib/util'
  import { loadDraft, saveDraft } from '../lib/persist'
  import Avatar from './Avatar.svelte'
  import MessageList from './MessageList.svelte'
  import EmojiPanel from './EmojiPanel.svelte'
  import AttachSheet from './AttachSheet.svelte'
  import PinnedBar from './PinnedBar.svelte'
  import ChatSearch from './ChatSearch.svelte'
  import SelectBar from './SelectBar.svelte'
  import { firstLink, wrap } from '../lib/format'
  import type { LinkView, Member } from '../lib/api'
  import { calls } from '../lib/call.svelte'
  import Icon from './Icon.svelte'
  import { duration, hasRecordPermission, micError, rememberRecordPermission, requestRecordPermission, RoundRecorder, VoiceRecorder } from '../lib/media'
  import { fade, fly, scale } from 'svelte/transition'

  let text = $state('')
  let input: HTMLTextAreaElement | undefined = $state()
  let fileInput: HTMLInputElement | undefined = $state()
  let dragging = $state(false)
  // Emoji / stickers / GIF panel. On phones it takes the place of the
  // system keyboard (like Telegram): opening it hides the keyboard, tapping
  // the text field brings the keyboard back.
  let panelOpen = $state(false)
  let attachOpen = $state(false)
  const narrow = () => window.matchMedia('(max-width: 720px)').matches
  function togglePanel() {
    if (panelOpen) {
      panelOpen = false
      input?.focus()
      return
    }
    if (narrow()) input?.blur()
    panelOpen = true
  }
  function insertEmoji(e: string) {
    const el = input
    const start = el?.selectionStart ?? text.length, end = el?.selectionEnd ?? text.length
    text = text.slice(0, start) + e + text.slice(end)
    tick().then(() => {
      if (!el) return
      const pos = start + e.length
      if (!narrow()) el.focus()
      el.setSelectionRange(pos, pos)
    })
  }
  let memberCount = $state<number | null>(null)
  let searching = $state(false)

  const chat = $derived(app.current)
  const messages = $derived(chat ? (app.messages[chat.id] ?? []) : [])

  // Per-chat drafts, kept across chat switches and reloads.
  let draftFor = ''
  $effect(() => {
    const id = chat?.id ?? ''
    if (id === draftFor) return
    if (draftFor && !app.editing) saveDraft(draftFor, text)
    draftFor = id
    text = id ? loadDraft(id) : ''
    tick().then(resize)
  })
  $effect(() => {
    const t = text
    if (draftFor && !app.editing) saveDraft(draftFor, t)
  })

  $effect(() => {
    const id = chat?.id
    const active = chat?.active
    memberCount = null
    // Presence of the person, refreshed every 30 s while the chat is open.
    let presenceTimer: ReturnType<typeof setInterval> | undefined
    const peer = chat?.kind === 'direct' ? chat.peerId : ''
    if (peer) {
      app.refreshPresence([peer])
      presenceTimer = setInterval(() => app.refreshPresence([peer]), 30_000)
    }
    members = []
    mention = null
    searching = false
    preview = null
    previewOff = ''
    if (id && active && chat?.kind === 'group') Messenger.Members(id).then((ms) => { members = ms ?? []; memberCount = new Set(members.map((x) => x.userId)).size }).catch(() => {})
    input?.focus()
    return () => clearInterval(presenceTimer)
  })

  async function send(silent = false) {
    const t = text
    if (!t.trim()) return
    const link = !app.editing && preview && !previewOff && firstLink(t) === preview.url ? preview : null
    text = ''
    preview = null
    previewOff = ''
    resize()
    input?.focus()
    // On failure put the text back unless the user already typed something new.
    if (!(await app.send(t, { silent, link })) && !text) text = t
  }

  // ---- send menu (right click / long press on send): silent, scheduled ----
  let sendMenu = $state(false)
  let sendPress: ReturnType<typeof setTimeout> | undefined
  let sendLong = false
  function sendDown(e: PointerEvent) {
    if (app.editing || e.pointerType === 'mouse') return
    sendLong = false
    sendPress = setTimeout(() => { sendLong = true; navigator.vibrate?.(15); sendMenu = true }, 450)
  }
  function sendUp() { clearTimeout(sendPress) }
  function sendClick() {
    if (sendLong) { sendLong = false; return }
    send()
  }
  function scheduleIt() {
    sendMenu = false
    if (!chat || !text.trim()) return
    const link = preview && !previewOff && firstLink(text) === preview.url ? preview : null
    app.modal = { kind: 'schedule', chatId: chat.id, text, silent: false, link }
    text = ''
    preview = null
  }
  let scheduledCount = $state(0)
  $effect(() => {
    void app.scheduledTick
    const id = chat?.id
    if (!id) return
    Messenger.Scheduled(id).then((l) => { if (chat?.id === id) scheduledCount = l?.length ?? 0 }).catch(() => {})
  })

  // ---- link preview of the first link being typed ----
  let preview = $state<LinkView | null>(null)
  let previewOff = $state('') // the link whose preview was closed
  let previewTimer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    const url = app.editing ? '' : firstLink(text)
    clearTimeout(previewTimer)
    if (!url) { preview = null; return }
    if (preview?.url === url) return
    previewTimer = setTimeout(async () => {
      const lp = await Messenger.LinkPreview(url).catch(() => null)
      if (firstLink(text) === url) preview = lp && (lp.title || lp.desc || lp.image) ? lp : null
    }, 600)
  })

  // ---- formatting: shortcuts and a toolbar over a selection ----
  let hasSel = $state(false)
  function checkSel() {
    hasSel = !!input && input.selectionStart !== input.selectionEnd && document.activeElement === input
  }
  function fmt(mark: string, close = mark) {
    if (!input) return
    const r = wrap(input, text, mark, close)
    text = r.text
    tick().then(() => { input?.focus(); input?.setSelectionRange(r.start, r.end); checkSel() })
  }
  async function fmtLink() {
    if (!input) return
    // The dialog takes the focus: remember the selection first.
    const s0 = input.selectionStart ?? 0, e0 = input.selectionEnd ?? 0
    const url = await app.prompt('Ссылка (https://…)', 'https://')
    if (!url || !/^https?:\/\/\S+$/.test(url.trim()) || !input) return
    const label = text.slice(s0, e0) || url.trim()
    text = text.slice(0, s0) + `[${label}](${url.trim()})` + text.slice(e0)
    tick().then(() => input?.focus())
  }

  // ---- @mentions in groups ----
  let members = $state<Member[]>([])
  let mention = $state<{ q: string; at: number } | null>(null)
  let mentionIdx = $state(0)
  const mentionHits = $derived.by(() => {
    if (!mention) return []
    const q = mention.q.toLowerCase()
    const me = app.state?.account?.userId
    const seen = new Set<string>()
    return members.filter((x) => {
      if (x.userId === me || seen.has(x.userId) || !x.username) return false
      seen.add(x.userId)
      return !q || x.username.toLowerCase().startsWith(q) || x.name.toLowerCase().split(/\s+/).some((w) => w.startsWith(q))
    }).slice(0, 8)
  })
  function checkMention() {
    if (!input || chat?.kind !== 'group') { mention = null; return }
    const pos = input.selectionStart ?? 0
    const m = /(^|\s)@([a-zA-Z0-9_.-]{0,32})$/.exec(text.slice(0, pos))
    mention = m ? { q: m[2], at: pos - m[2].length - 1 } : null
    mentionIdx = 0
  }
  function insertMention(u: Member) {
    if (!mention || !input) return
    const pos = input.selectionStart ?? text.length
    const ins = '@' + u.username + ' '
    text = text.slice(0, mention.at) + ins + text.slice(pos)
    const caret = mention.at + ins.length
    mention = null
    tick().then(() => { input?.focus(); input?.setSelectionRange(caret, caret) })
  }

  function key(e: KeyboardEvent) {
    if (mention && mentionHits.length) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        mentionIdx = (mentionIdx + (e.key === 'ArrowDown' ? 1 : mentionHits.length - 1)) % mentionHits.length
        return
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault()
        insertMention(mentionHits[mentionIdx])
        return
      }
      if (e.key === 'Escape') { mention = null; return }
    }
    if ((e.ctrlKey || e.metaKey) && !e.altKey) {
      const k = e.code
      const f = !e.shiftKey && k === 'KeyB' ? ['**'] : !e.shiftKey && k === 'KeyI' ? ['__'] : e.shiftKey && k === 'KeyX' ? ['~~']
        : e.shiftKey && k === 'KeyM' ? ['`'] : e.shiftKey && k === 'KeyP' ? ['||'] : null
      if (f) { e.preventDefault(); fmt(f[0]); return }
      if (!e.shiftKey && k === 'KeyK') { e.preventDefault(); fmtLink(); return }
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      send()
    } else if (e.key === 'Escape' && (app.reply || app.editing)) {
      cancelCompose()
    } else if (e.key === 'ArrowUp' && !text) {
      // Like Telegram Desktop: ↑ in an empty field edits your last message.
      const last = [...messages].reverse().find((x) => x.outgoing && x.kind === 'text')
      if (last) {
        e.preventDefault()
        app.startEdit(last)
      }
    }
  }

  function cancelCompose() {
    if (app.editing) text = ''
    app.reply = null
    app.editing = null
    resize()
  }

  // Editing puts the message's text into the field.
  let editingId = ''
  $effect(() => {
    const ed = app.editing
    if (ed && ed.id !== editingId) {
      editingId = ed.id
      text = ed.text
      tick().then(() => { resize(); input?.focus() })
    } else if (!ed) {
      editingId = ''
    }
  })
  $effect(() => { if (app.reply) input?.focus() })

  // The field follows its text: grows while typing, shrinks back after a
  // send/cancel (resize after the DOM has the new value).
  $effect(() => {
    void text
    if (input) tick().then(resize)
  })

  function resize() {
    if (!input) return
    input.style.height = 'auto'
    input.style.height = Math.min(input.scrollHeight, 180) + 'px'
    // Scrolls only when it is taller than the box (no stray scroll bar).
    input.style.overflowY = input.scrollHeight > 180 ? 'auto' : 'hidden'
  }

  function pick(files: FileList | null | undefined) {
    if (files?.length) app.pickFiles(Array.from(files))
    if (fileInput) fileInput.value = ''
  }

  // Pasting an image (screenshot) opens the send dialog, like Telegram.
  function paste(e: ClipboardEvent) {
    const files = Array.from(e.clipboardData?.files ?? [])
    if (files.length) {
      e.preventDefault()
      app.pickFiles(files)
    }
  }

  // ---- voice / video messages -------------------------------------------
  // Like Telegram: tap the button to switch voice <-> video message; hold
  // to record and release to send; slide left to cancel, up to lock.
  let recMode = $state<'voice' | 'round'>('voice')
  let rec = $state<{ kind: 'voice' | 'round'; locked: boolean; startX: number; startY: number; t: number; level: number } | null>(null)
  let recorder: VoiceRecorder | RoundRecorder | null = null
  let roundPreview: HTMLVideoElement | undefined = $state()
  let holdTimer: ReturnType<typeof setTimeout> | undefined
  let tickTimer: ReturnType<typeof setInterval> | undefined
  let pressAt = 0
  let cancelHint = $state(false)

  let asking = false
  function micDown(e: PointerEvent) {
    if (rec || asking) return
    e.preventDefault()
    const kind = recMode
    if (!hasRecordPermission(kind)) {
      // First time: ask for access with this tap, record with the next hold.
      asking = true
      requestRecordPermission(kind)
        .then(() => app.toast(kind === 'voice' ? 'Микрофон доступен — удерживайте кнопку, чтобы записать' : 'Камера доступна — удерживайте кнопку, чтобы записать', 'info', 4000))
        .catch((err) => app.toast(micError(err), 'error', 6000))
        .finally(() => (asking = false))
      pressAt = 0
      return
    }
    pressAt = Date.now()
    const x = e.clientX, y = e.clientY
    ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
    holdTimer = setTimeout(() => startRec(x, y), 250)
  }

  let starting: Promise<void> | null = null
  async function startRec(x: number, y: number) {
    const kind = recMode
    const r = kind === 'voice' ? new VoiceRecorder() : new RoundRecorder()
    recorder = r
    rec = { kind, locked: false, startX: x, startY: y, t: 0, level: 0 }
    navigator.vibrate?.(20)
    starting = r.start()
    try {
      await starting
      rememberRecordPermission(kind)
      if (recorder !== r) { r.cancel(); return } // released before the mic was ready
      if (r instanceof VoiceRecorder) r.onLevel = (v) => rec && (rec.level = v)
      if (r instanceof RoundRecorder) {
        await tick()
        if (roundPreview) { roundPreview.srcObject = r.stream; roundPreview.play().catch(() => {}) }
      }
      tickTimer = setInterval(() => {
        if (!rec || !recorder) return
        rec.t = (Date.now() - recorder.started) / 1000
        if (rec.kind === 'round' && rec.t >= 60) finishRec(true) // Telegram's limit
      }, 200)
    } catch (err) {
      r.cancel()
      if (recorder === r) { recorder = null; rec = null }
      app.toast(micError(err), 'error', 6000)
    } finally {
      starting = null
    }
  }

  function micMove(e: PointerEvent) {
    if (!rec || rec.locked) return
    cancelHint = rec.startX - e.clientX > 60
    if (rec.startX - e.clientX > 120) finishRec(false)
    else if (rec.startY - e.clientY > 70) rec.locked = true
  }

  function micUp() {
    clearTimeout(holdTimer)
    if (!rec) {
      if (pressAt && Date.now() - pressAt < 250) recMode = recMode === 'voice' ? 'round' : 'voice'
      return
    }
    if (!rec.locked) finishRec(!cancelHint)
  }

  async function finishRec(send: boolean) {
    clearInterval(tickTimer)
    const r = recorder
    recorder = null
    cancelHint = false
    rec = null
    if (!r) return
    if (starting) {
      // Released while the mic/camera was still starting: nothing recorded.
      r.cancel()
      return
    }
    if (!send) { r.cancel(); return }
    const p = r instanceof RoundRecorder ? await r.stop(roundPreview) : await r.stop()
    if (p) await app.sendMedia(p)
    else app.toast('Слишком короткое сообщение — удерживайте кнопку', 'info')
  }

  function dragOver(e: DragEvent) {
    if (!e.dataTransfer?.types.includes('Files')) return
    e.preventDefault()
    if (chat?.active) {
      e.dataTransfer.dropEffect = 'copy'
      dragging = true
    }
  }

  function drop(e: DragEvent) {
    e.preventDefault()
    dragging = false
    const files = e.dataTransfer?.files
    if (chat?.active && files?.length) {
      app.lastDomDrop = Date.now()
      pick(files)
    }
  }
</script>

{#if chat}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <!-- data-file-drop-target: the desktop runtime accepts OS file drops only over it -->
  <section class="chat-view" class:dragging data-file-drop-target={chat.active ? '' : undefined}
    ondragover={dragOver} ondragleave={(e) => { if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) dragging = false }} ondrop={drop}>
    {#if app.selected.length}
      <SelectBar chatId={chat.id} />
    {:else if searching}
      <ChatSearch chatId={chat.id} onclose={() => (searching = false)} />
    {:else}
    <header>
      <button class="icon-btn back" aria-label="Назад" onclick={() => app.openChat(null)}><Icon name="back" /></button>
      <button class="title" onclick={() => (app.modal = { kind: 'chatInfo', chatId: chat.id })}>
        <Avatar name={chat.name} id={chat.peerId || chat.id} user={chat.peerId} photo={chat.peerAvatar} src={chat.photo} size={42} />
        <div class="title-text">
          <div class="name">{chat.name}</div>
          <div class="status muted">
            {#if !chat.active}{chat.kind === 'direct' ? 'чат недоступен' : 'вы не участник этой группы'}
            {:else if !app.online}соединение…
            {:else if app.typingIn(chat.id)}<span class="typing">{chat.kind === 'direct' ? 'печатает' : `${app.typingIn(chat.id)} печатает`}<span class="dots"></span></span>
            {:else if chat.kind === 'direct'}<span class:online={app.presence[chat.peerId ?? '']?.online}>{lastSeenText(app.presence[chat.peerId ?? '']) || '@' + chat.peer}</span>
            {:else if memberCount !== null}{memberCount} {plural(memberCount, 'участник', 'участника', 'участников')}
            {:else}&nbsp;{/if}
          </div>
        </div>
      </button>
      {#if chat.active}
        <button class="icon-btn" aria-label="Аудиозвонок" title="Позвонить" onclick={() => calls.start(chat.id, false)}><Icon name="phone" /></button>
        <button class="icon-btn" aria-label="Видеозвонок" title="Видеозвонок" onclick={() => calls.start(chat.id, true)}><Icon name="video" /></button>
      {/if}
      <button class="icon-btn" aria-label="Поиск" title="Поиск по чату" onclick={() => (searching = true)}><Icon name="search" /></button>
      <button class="icon-btn" aria-label="Информация" onclick={() => (app.modal = { kind: 'chatInfo', chatId: chat.id })}><Icon name="dots" /></button>
    </header>
    {/if}
    <PinnedBar chatId={chat.id} />
    {#if calls.active[chat.id] && calls.call?.chatId !== chat.id && chat.kind === 'group'}
      <button class="call-banner" onclick={() => calls.join(chat.id)}>
        <span class="cb-dot"></span> Идёт групповой {calls.active[chat.id].video ? 'видеозвонок' : 'звонок'}
        <span class="cb-join">Присоединиться</span>
      </button>
    {/if}

    {#key chat.id}
      <MessageList chatId={chat.id} group={chat.kind === 'group'} />
    {/key}

    {#if chat.active && preview && previewOff !== preview.url && !app.editing}
      <div class="compose-bar lp-bar">
        <span class="cb-icon"><Icon name="link" size={22} /></span>
        <div class="cb-text">
          <div class="cb-title">{preview.site || preview.title}</div>
          <div class="cb-body">{preview.title || preview.desc}</div>
        </div>
        {#if preview.image}<img class="lp-thumb" src={preview.image} alt="" />{/if}
        <button class="icon-btn" aria-label="Без превью" onclick={() => (previewOff = preview!.url)}><Icon name="close" size={20} /></button>
      </div>
    {/if}
    {#if chat.active && (app.reply || app.editing)}
      {@const ref = app.editing ?? app.reply}
      <div class="compose-bar">
        <span class="cb-icon"><Icon name={app.editing ? 'pencil' : 'reply'} size={22} /></span>
        <div class="cb-text">
          <div class="cb-title">{app.editing ? 'Редактирование' : `В ответ ${ref?.sender ?? ''}`}</div>
          <div class="cb-body">{ref?.text || (ref?.file ? (ref.file.caption || ref.file.name) : '')}</div>
        </div>
        <button class="icon-btn" aria-label="Отмена" onclick={cancelCompose}><Icon name="close" size={20} /></button>
      </div>
    {/if}
    {#if chat.kind === 'direct' && chat.peerId && app.blocked.includes(chat.peerId)}
      <footer class="readonly">
        <button class="unblock" onclick={() => app.setBlocked(chat.peerId!, false)}>Разблокировать</button>
      </footer>
    {:else if chat.active}
      <footer>
        {#if attachOpen}<AttachSheet onclose={() => (attachOpen = false)} />{/if}
        {#if panelOpen}
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <div class="panel-backdrop" onclick={() => (panelOpen = false)}></div>
          <div class="emoji-pop" transition:fly={{ y: 12, duration: 180 }}>
            <EmojiPanel onpick={insertEmoji} onclose={() => (panelOpen = false)} ongif={() => (panelOpen = false)} />
          </div>
        {/if}
        {#if mention && mentionHits.length}
          <div class="mentions" transition:fly={{ y: 8, duration: 120 }}>
            {#each mentionHits as u, i (u.userId)}
              <button class:cur={i === mentionIdx} onmousedown={(e) => e.preventDefault()} onclick={() => insertMention(u)}>
                <Avatar name={u.name} id={u.userId} user={u.userId} photo={u.avatarId} size={32} />
                <span class="mn">{u.name}</span><span class="mu muted">@{u.username}</span>
              </button>
            {/each}
          </div>
        {/if}
        {#if hasSel && !rec}
          <div class="fmt" transition:fly={{ y: 6, duration: 120 }}>
            <button title="Жирный (Ctrl+B)" onmousedown={(e) => e.preventDefault()} onclick={() => fmt('**')}><Icon name="bold" size={18} /></button>
            <button title="Курсив (Ctrl+I)" onmousedown={(e) => e.preventDefault()} onclick={() => fmt('__')}><Icon name="italic" size={18} /></button>
            <button title="Зачёркнутый (Ctrl+Shift+X)" onmousedown={(e) => e.preventDefault()} onclick={() => fmt('~~')}><Icon name="strike" size={18} /></button>
            <button title="Моноширинный (Ctrl+Shift+M)" onmousedown={(e) => e.preventDefault()} onclick={() => fmt('`')}><Icon name="code" size={18} /></button>
            <button title="Спойлер (Ctrl+Shift+P)" onmousedown={(e) => e.preventDefault()} onclick={() => fmt('||')}><Icon name="spoiler" size={18} /></button>
            <button title="Ссылка (Ctrl+K)" onmousedown={(e) => e.preventDefault()} onclick={fmtLink}><Icon name="link" size={18} /></button>
          </div>
        {/if}
        {#if sendMenu}
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <div class="panel-backdrop show" onclick={() => (sendMenu = false)}></div>
          <div class="send-menu" transition:fly={{ y: 8, duration: 140 }}>
            <button onclick={() => { sendMenu = false; send(true) }}><Icon name="bell-off" size={20} /> Отправить без звука</button>
            <button onclick={scheduleIt}><Icon name="clock" size={20} /> Отложить сообщение</button>
          </div>
        {/if}
        <div class="composer">
          {#if rec}
            <div class="recbar">
              <span class="recdot"></span>
              <span class="rectime">{duration(rec.t)}</span>
              {#if rec.kind === 'voice'}<span class="level" style="transform:scaleX({0.15 + rec.level})"></span>{/if}
              {#if rec.locked}
                <button class="link" onclick={() => finishRec(false)}>Отмена</button>
              {:else}
                <span class="hint" class:warn={cancelHint}>‹ Влево — отмена · вверх — закрепить</span>
              {/if}
            </div>
          {:else}
            <button class="icon-btn" aria-label="Прикрепить файл" onclick={() => (attachOpen = !attachOpen)}><Icon name="clip" /></button>
            <textarea bind:this={input} bind:value={text} rows="1" placeholder="Сообщение" onkeydown={key} onpaste={paste}
              onfocus={() => { if (narrow()) panelOpen = false }} onselect={checkSel} onkeyup={() => { checkSel(); checkMention() }}
              onmouseup={checkSel} onblur={() => setTimeout(() => { hasSel = false; mention = null }, 150)} onclick={checkMention}
              oninput={() => { resize(); app.typingTick(); checkMention() }}></textarea>
            {#if scheduledCount && !app.editing}
              <button class="icon-btn sched-btn" aria-label="Отложенные сообщения" title="Отложенные сообщения"
                onclick={() => (app.modal = { kind: 'scheduled', chatId: chat.id })}><Icon name="clock" /></button>
            {/if}
            {#if !app.editing}
              <button class="icon-btn emoji-btn" class:on={panelOpen} aria-label={panelOpen ? 'Клавиатура' : 'Эмодзи, стикеры и GIF'}
                title="Эмодзи, стикеры и GIF" onclick={togglePanel}><Icon name={panelOpen && narrow() ? 'keyboard' : 'smile'} /></button>
            {/if}
          {/if}
          <input type="file" multiple hidden accept="*/*" bind:this={fileInput} onchange={(e) => pick((e.currentTarget as HTMLInputElement).files)} />
        </div>
        {#if text.trim() || app.editing}
          <button class="send" aria-label="Отправить" onclick={sendClick} onpointerdown={sendDown} onpointerup={sendUp} onpointerleave={sendUp}
            oncontextmenu={(e) => { e.preventDefault(); if (!app.editing) sendMenu = true }}><Icon name={app.editing ? 'check' : 'send'} /></button>
        {:else if rec?.locked}
          <button class="send" aria-label="Отправить" onclick={() => finishRec(true)}><Icon name="send" /></button>
        {:else}
          <button class="send mic" class:recording={!!rec} aria-label={recMode === 'voice' ? 'Голосовое сообщение (удерживайте)' : 'Видеосообщение (удерживайте)'}
            title={recMode === 'voice' ? 'Удерживайте для записи голоса, нажмите — видео' : 'Удерживайте для записи видео, нажмите — голос'}
            onpointerdown={micDown} onpointermove={micMove} onpointerup={micUp} onpointercancel={micUp} oncontextmenu={(e) => e.preventDefault()}>
            <Icon name={recMode === 'voice' ? 'mic' : 'video'} />
          </button>
        {/if}
      </footer>
    {:else}
      <footer class="readonly muted">{chat.kind === 'direct' ? 'Чат пока недоступен на этом устройстве — доступна только история' : 'Вы не участник этой группы — доступна только история'}</footer>
    {/if}

    {#if rec?.kind === 'round'}
      <div class="round-backdrop" transition:fade={{ duration: 150 }}></div>
      <div class="round-rec" transition:scale={{ start: 0.6, duration: 200 }}>
        <!-- svelte-ignore a11y_media_has_caption -->
        <video bind:this={roundPreview} muted playsinline></video>
        <svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="48" pathLength="1" stroke-dasharray="1" stroke-dashoffset={1 - rec.t / 60} /></svg>
        <span class="round-time"><span class="recdot"></span>{duration(rec.t)}</span>
      </div>
    {/if}

    {#if chat.active}
      <div class="dropzone"><div><Icon name="file" size={40} /><br />Отпустите, чтобы отправить</div></div>
    {/if}
  </section>
{:else}
  <section class="chat-view placeholder">
    <span class="pill">{#if app.opening}<span class="spin-sm"></span> Открываем чат с @{app.opening}…{:else}Выберите чат, чтобы начать общение{/if}</span>
  </section>
{/if}

<style>
  .chat-view {
    position: relative; display: flex; flex-direction: column; height: 100%; min-width: 0; min-height: 0; overflow: hidden; overflow: clip;
    background-color: var(--bg-chat);
    background-image: var(--wallpaper);
  }
  header { display: flex; align-items: center; gap: 6px; height: 56px; padding: 0 8px 0 12px; background: var(--bg-header); border-bottom: 1px solid var(--border); }
  .back { display: none; }
  .title { flex: 1; display: flex; align-items: center; gap: 12px; text-align: left; min-width: 0; }
  .title-text { min-width: 0; }
  .name { font-weight: 600; font-size: 15px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .status { font-size: 13px; }
  footer { position: relative; display: flex; align-items: flex-end; gap: 8px; padding: 8px 16px 14px; max-width: 792px; width: 100%; margin: 0 auto; }
  .emoji-btn.on { color: var(--accent-light); }
  .sched-btn { color: var(--accent-light); }
  .lp-bar { margin-bottom: 0; border-radius: 12px 12px 0 0; }
  .lp-bar + .compose-bar { border-radius: 0; margin-top: 0; }
  .lp-thumb { width: 40px; height: 40px; border-radius: 6px; object-fit: cover; }
  .mentions { position: absolute; left: 16px; right: 84px; bottom: calc(100% + 4px); z-index: 27; max-height: 260px; overflow-y: auto;
    background: var(--bg-menu); border-radius: 12px; box-shadow: var(--shadow); padding: 4px 0; }
  .mentions button { display: flex; align-items: center; gap: 10px; width: 100%; padding: 6px 12px; text-align: left; }
  .mentions button:hover, .mentions button.cur { background: var(--bg-hover); }
  .mn { font-weight: 500; }
  .mu { font-size: 13px; }
  .fmt { position: absolute; left: 60px; bottom: calc(100% + 4px); z-index: 27; display: flex; gap: 2px; padding: 4px;
    background: var(--bg-menu); border-radius: 12px; box-shadow: var(--shadow); }
  .fmt button { width: 34px; height: 34px; border-radius: 8px; display: flex; align-items: center; justify-content: center; color: var(--text); }
  .fmt button:hover { background: var(--bg-hover); }
  .send-menu { position: absolute; right: 16px; bottom: calc(100% + 4px); z-index: 27; min-width: 240px; padding: 6px 0;
    background: var(--bg-menu); border-radius: 12px; box-shadow: var(--shadow); display: flex; flex-direction: column; }
  .send-menu button { display: flex; align-items: center; gap: 14px; padding: 10px 16px; font-size: 15px; text-align: left; }
  .send-menu button:hover { background: var(--bg-hover); }
  .unblock { color: #e17076; font-weight: 600; font-size: 15px; text-transform: uppercase; letter-spacing: .3px; padding: 6px 16px; border-radius: 10px; }
  .unblock:hover { background: var(--bg-hover); }
  .panel-backdrop { position: fixed; inset: 0; z-index: 25; }
  .emoji-pop { position: absolute; right: 16px; bottom: calc(100% + 6px); z-index: 26; width: 380px; height: 430px; max-height: 60vh;
    border-radius: 16px; overflow: hidden; box-shadow: var(--shadow); }
  @media (max-width: 720px) {
    footer { flex-wrap: wrap; }
    /* Phones: docked under the field, where the keyboard was. */
    .panel-backdrop:not(.show) { display: none; }
    .mentions { left: 8px; right: 8px; }
    .emoji-pop { position: static; order: 10; flex-basis: 100%; width: auto; height: 300px; max-height: 45vh; border-radius: 0;
      box-shadow: none; margin: 6px -8px -10px; }
  }
  .call-banner { display: flex; align-items: center; gap: 10px; padding: 8px 16px; background: var(--bg-header); border-bottom: 1px solid var(--border);
    font-size: 14px; color: var(--text); text-align: left; }
  .call-banner:hover { background: var(--bg-hover); }
  .cb-dot { width: 8px; height: 8px; border-radius: 50%; background: #4dcd5e; animation: blink 1.4s infinite; }
  .cb-join { margin-left: auto; color: #fff; background: #3fbf5f; border-radius: 14px; padding: 4px 12px; font-weight: 500; }
  .composer { flex: 1; display: flex; align-items: flex-end; background: var(--bg-sidebar); border-radius: 18px; padding: 4px 6px; box-shadow: 0 1px 3px rgba(0,0,0,.3); }
  textarea { flex: 1; resize: none; border: 0; outline: 0; background: transparent; padding: 10px 6px; font-size: 15px; line-height: 1.35; max-height: 180px;
    overflow-y: hidden; scrollbar-width: none; }
  textarea::-webkit-scrollbar { display: none; }
  textarea::placeholder { color: var(--text-muted); }
  .send { width: 52px; height: 52px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center;
    flex-shrink: 0; transition: filter .15s, transform .15s; box-shadow: 0 1px 3px rgba(0,0,0,.3); }
  .send:hover:not(:disabled) { filter: brightness(1.1); }
  .send:disabled { background: var(--bg-sidebar); color: var(--text-muted); cursor: default; }
  .compose-bar { display: flex; align-items: center; gap: 12px; max-width: 760px; width: calc(100% - 32px); margin: 0 auto -4px; padding: 8px 12px;
    background: var(--bg-sidebar); border-radius: 12px 12px 0 0; }
  .cb-icon { color: var(--accent-light); display: inline-flex; }
  .cb-text { flex: 1; min-width: 0; border-left: 2px solid var(--accent-light); padding-left: 10px; }
  .cb-title { color: var(--accent-light); font-weight: 500; font-size: 14px; }
  .cb-body { font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--text-muted); }
  .typing { color: var(--accent-light); }
  .status .online { color: var(--accent-light); }
  .dots::after { content: '…'; animation: dots 1.2s steps(3) infinite; display: inline-block; width: 1em; overflow: hidden; vertical-align: bottom; }
  @keyframes dots { from { width: 0; } to { width: 1em; } }
  .send.mic { background: var(--bg-sidebar); color: var(--text-muted); touch-action: none; user-select: none; -webkit-user-select: none; -webkit-touch-callout: none;
    position: relative; z-index: 32; }
  .send.mic:hover { color: var(--accent-light); }
  .send.mic.recording { background: var(--accent); color: #fff; transform: scale(1.35); }
  .recbar { flex: 1; display: flex; align-items: center; gap: 10px; padding: 10px 8px; min-height: 42px; }
  .recdot { width: 10px; height: 10px; border-radius: 50%; background: #e53935; animation: blink 1s infinite; }
  .rectime { font-variant-numeric: tabular-nums; }
  .level { width: 60px; height: 4px; border-radius: 2px; background: var(--accent-light); transform-origin: left; transition: transform .05s; }
  .hint { flex: 1; text-align: center; color: var(--text-muted); font-size: 14px; }
  .hint.warn { color: #e17076; }
  .link { margin-left: auto; color: var(--accent-light); }
  @keyframes blink { 50% { opacity: .2; } }
  .round-backdrop { position: fixed; inset: 0; background: rgba(0, 0, 0, .55); z-index: 30; pointer-events: none; }
  .round-rec { position: fixed; left: 0; right: 0; top: 0; bottom: 120px; margin: auto; width: min(300px, 72vw); height: min(300px, 72vw); z-index: 31; pointer-events: none; }
  .round-time { position: absolute; left: 50%; bottom: -36px; transform: translateX(-50%); display: flex; align-items: center; gap: 8px;
    background: rgba(0, 0, 0, .5); color: #fff; padding: 4px 12px; border-radius: 14px; font-size: 14px; font-variant-numeric: tabular-nums; }
  .round-rec video { width: 100%; height: 100%; object-fit: cover; border-radius: 50%; transform: scaleX(-1); background: #000; box-shadow: var(--shadow); }
  .round-rec svg { position: absolute; inset: -6px; width: calc(100% + 12px); height: calc(100% + 12px); transform: rotate(-90deg); fill: none; stroke: var(--accent); stroke-width: 2; }
  .readonly { justify-content: center; padding: 18px; font-size: 14px; background: var(--bg-header); max-width: none; }
  .placeholder { align-items: center; justify-content: center; }
  .spin-sm { display: inline-block; width: 12px; height: 12px; margin-right: 6px; vertical-align: -1px; border: 2px solid currentColor;
    border-right-color: transparent; border-radius: 50%; animation: spin-sm .8s linear infinite; }
  @keyframes spin-sm { to { transform: rotate(360deg) } }
  .pill { background: rgba(0, 0, 0, 0.35); padding: 6px 14px; border-radius: 14px; font-size: 14px; color: #d4dde6; }
  .dropzone { position: absolute; inset: 12px; border: 2px dashed var(--accent); border-radius: 16px; background: color-mix(in srgb, var(--bg-chat) 88%, transparent);
    display: flex; align-items: center; justify-content: center; text-align: center; color: var(--accent-light); font-size: 18px; pointer-events: none;
    z-index: 20; opacity: 0; visibility: hidden; transform: scale(.98); transition: opacity .15s, transform .15s, visibility .15s; }
  /* Shown while files are dragged over the chat (DOM drag, or the desktop runtime's hover class). */
  .chat-view.dragging .dropzone, :global(.chat-view.file-drop-target-active) .dropzone { opacity: 1; visibility: visible; transform: none; }
  @media (max-width: 720px) {
    .back { display: inline-flex; }
    footer { padding: 6px 8px 10px; }
  }
</style>
