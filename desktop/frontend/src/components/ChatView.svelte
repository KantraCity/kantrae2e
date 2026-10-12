<script lang="ts">
  import { tick } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import { plural } from '../lib/util'
  import { loadDraft, saveDraft } from '../lib/persist'
  import Avatar from './Avatar.svelte'
  import MessageList from './MessageList.svelte'
  import EmojiPanel from './EmojiPanel.svelte'
  import AttachSheet from './AttachSheet.svelte'
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
    if (id && active && chat?.kind === 'group') Messenger.Members(id).then((ms) => (memberCount = new Set((ms ?? []).map((x) => x.userId)).size)).catch(() => {})
    input?.focus()
  })

  async function send() {
    const t = text
    if (!t.trim()) return
    text = ''
    resize()
    input?.focus()
    // On failure put the text back unless the user already typed something new.
    if (!(await app.send(t)) && !text) text = t
  }

  function key(e: KeyboardEvent) {
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
    <header>
      <button class="icon-btn back" aria-label="Назад" onclick={() => app.openChat(null)}><Icon name="back" /></button>
      <button class="title" onclick={() => (app.modal = { kind: 'chatInfo', chatId: chat.id })}>
        <Avatar name={chat.name} id={chat.peerId || chat.id} user={chat.peerId} photo={chat.peerAvatar} size={42} />
        <div class="title-text">
          <div class="name">{chat.name}</div>
          <div class="status muted">
            {#if !chat.active}{chat.kind === 'direct' ? 'чат недоступен' : 'вы не участник этой группы'}
            {:else if !app.online}соединение…
            {:else if app.typingIn(chat.id)}<span class="typing">{chat.kind === 'direct' ? 'печатает' : `${app.typingIn(chat.id)} печатает`}<span class="dots"></span></span>
            {:else if chat.kind === 'direct'}@{chat.peer}
            {:else if memberCount !== null}{memberCount} {plural(memberCount, 'участник', 'участника', 'участников')}
            {:else}&nbsp;{/if}
          </div>
        </div>
      </button>
      {#if chat.active}
        <button class="icon-btn" aria-label="Аудиозвонок" title="Позвонить" onclick={() => calls.start(chat.id, false)}><Icon name="phone" /></button>
        <button class="icon-btn" aria-label="Видеозвонок" title="Видеозвонок" onclick={() => calls.start(chat.id, true)}><Icon name="video" /></button>
      {/if}
      <button class="icon-btn" aria-label="Информация" onclick={() => (app.modal = { kind: 'chatInfo', chatId: chat.id })}><Icon name="dots" /></button>
    </header>
    {#if calls.active[chat.id] && calls.call?.chatId !== chat.id && chat.kind === 'group'}
      <button class="call-banner" onclick={() => calls.join(chat.id)}>
        <span class="cb-dot"></span> Идёт групповой {calls.active[chat.id].video ? 'видеозвонок' : 'звонок'}
        <span class="cb-join">Присоединиться</span>
      </button>
    {/if}

    {#key chat.id}
      <MessageList chatId={chat.id} group={chat.kind === 'group'} />
    {/key}

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
    {#if chat.active}
      <footer>
        {#if attachOpen}<AttachSheet onclose={() => (attachOpen = false)} />{/if}
        {#if panelOpen}
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <div class="panel-backdrop" onclick={() => (panelOpen = false)}></div>
          <div class="emoji-pop" transition:fly={{ y: 12, duration: 180 }}>
            <EmojiPanel onpick={insertEmoji} onclose={() => (panelOpen = false)} ongif={() => (panelOpen = false)} />
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
              onfocus={() => { if (narrow()) panelOpen = false }}
              oninput={() => { resize(); app.typingTick() }}></textarea>
            {#if !app.editing}
              <button class="icon-btn emoji-btn" class:on={panelOpen} aria-label={panelOpen ? 'Клавиатура' : 'Эмодзи, стикеры и GIF'}
                title="Эмодзи, стикеры и GIF" onclick={togglePanel}><Icon name={panelOpen && narrow() ? 'keyboard' : 'smile'} /></button>
            {/if}
          {/if}
          <input type="file" multiple hidden accept="*/*" bind:this={fileInput} onchange={(e) => pick((e.currentTarget as HTMLInputElement).files)} />
        </div>
        {#if text.trim() || app.editing}
          <button class="send" aria-label="Отправить" onclick={send}><Icon name={app.editing ? 'check' : 'send'} /></button>
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
    <span class="pill">Выберите чат, чтобы начать общение</span>
  </section>
{/if}

<style>
  .chat-view {
    position: relative; display: flex; flex-direction: column; height: 100%; min-width: 0; min-height: 0; overflow: hidden;
    background-color: var(--bg-chat);
    background-image:
      radial-gradient(circle at 20% 20%, rgba(82, 136, 193, 0.08), transparent 45%),
      radial-gradient(circle at 80% 70%, rgba(106, 90, 205, 0.07), transparent 50%);
  }
  header { display: flex; align-items: center; gap: 6px; height: 56px; padding: 0 8px 0 12px; background: var(--bg-header); border-bottom: 1px solid var(--border); }
  .back { display: none; }
  .title { flex: 1; display: flex; align-items: center; gap: 12px; text-align: left; min-width: 0; }
  .title-text { min-width: 0; }
  .name { font-weight: 600; font-size: 15px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .status { font-size: 13px; }
  footer { position: relative; display: flex; align-items: flex-end; gap: 8px; padding: 8px 16px 14px; max-width: 792px; width: 100%; margin: 0 auto; }
  .emoji-btn.on { color: var(--accent-light); }
  .panel-backdrop { position: fixed; inset: 0; z-index: 25; }
  .emoji-pop { position: absolute; right: 16px; bottom: calc(100% + 6px); z-index: 26; width: 380px; height: 430px; max-height: 60vh;
    border-radius: 16px; overflow: hidden; box-shadow: var(--shadow); }
  @media (max-width: 720px) {
    footer { flex-wrap: wrap; }
    /* Phones: docked under the field, where the keyboard was. */
    .panel-backdrop { display: none; }
    .emoji-pop { position: static; order: 10; flex-basis: 100%; width: auto; height: 300px; max-height: 45vh; border-radius: 0;
      box-shadow: none; margin: 6px -8px -10px; }
  }
  .call-banner { display: flex; align-items: center; gap: 10px; padding: 8px 16px; background: var(--bg-header); border-bottom: 1px solid var(--border);
    font-size: 14px; color: var(--text); text-align: left; }
  .call-banner:hover { background: var(--bg-hover); }
  .cb-dot { width: 8px; height: 8px; border-radius: 50%; background: #4dcd5e; animation: blink 1.4s infinite; }
  .cb-join { margin-left: auto; color: #fff; background: #3fbf5f; border-radius: 14px; padding: 4px 12px; font-weight: 500; }
  .composer { flex: 1; display: flex; align-items: flex-end; background: var(--bg-sidebar); border-radius: 18px; padding: 4px 6px; box-shadow: 0 1px 3px rgba(0,0,0,.3); }
  textarea { flex: 1; resize: none; border: 0; outline: 0; background: transparent; padding: 10px 6px; font-size: 15px; line-height: 1.35; max-height: 180px; }
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
  .pill { background: rgba(0, 0, 0, 0.35); padding: 6px 14px; border-radius: 14px; font-size: 14px; color: #d4dde6; }
  .dropzone { position: absolute; inset: 12px; border: 2px dashed var(--accent); border-radius: 16px; background: rgba(14, 22, 33, 0.85);
    display: flex; align-items: center; justify-content: center; text-align: center; color: var(--accent-light); font-size: 18px; pointer-events: none;
    z-index: 20; opacity: 0; visibility: hidden; transform: scale(.98); transition: opacity .15s, transform .15s, visibility .15s; }
  /* Shown while files are dragged over the chat (DOM drag, or the desktop runtime's hover class). */
  .chat-view.dragging .dropzone, :global(.chat-view.file-drop-target-active) .dropzone { opacity: 1; visibility: visible; transform: none; }
  @media (max-width: 720px) {
    .back { display: inline-flex; }
    footer { padding: 6px 8px 10px; }
  }
</style>
