<script lang="ts">
  import { plain } from '../lib/format'
  import { app } from '../lib/app.svelte'
  import { Messenger, type Chat } from '../lib/api'
  import { chatListTime } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  const q = $derived(app.search.trim().replace(/^@/, '').toLowerCase())
  const filtered = $derived(q ? app.chats.filter((c) => c.name.toLowerCase().includes(q)) : app.chats)
  // People found globally that are not already a private chat in the list.
  // An invite link pasted into the search joins its group.
  const inviteLink = $derived(/\/join\/[^#\s]+#\S+/.test(app.search.trim()))
  async function joinLink() {
    const id = await app.try(() => Messenger.JoinByLink(app.search.trim()))
    if (id === undefined) return
    app.search = ''
    if (app.chats.some((c) => c.id === id)) app.openChat(id)
    else app.toast('Запрос отправлен — вас добавят, как только кто-то из участников будет в сети', 'info', 6000)
  }
  const people = $derived(app.userHits.filter((u) => !filtered.some((c) => c.kind === 'direct' && c.peerId === u.userId)))
  let menu = $state(false)
  let timer: ReturnType<typeof setTimeout> | undefined

  function searchInput() {
    clearTimeout(timer)
    timer = setTimeout(() => app.searchUsers(app.search), 250)
  }

  function clearSearch() {
    app.search = ''
    app.searchUsers('')
  }

  const MEDIA: Record<string, string> = { photo: '🖼 Фото', video: '📹 Видео', voice: '🎤 Голосовое сообщение', round: '⏺ Видеосообщение', gif: 'GIF', sticker: 'Стикер' }

  function preview(c: (typeof app.chats)[number]): string {
    if (!c.last) return c.active ? (c.kind === 'direct' ? 'Личный чат' : 'Нет сообщений') : 'Вы не участник'
    if (c.last.kind === 'system') return c.last.text
    // Like Telegram: sender names only in groups.
    const who = c.last.outgoing ? 'Вы: ' : c.kind === 'group' && c.last.sender ? `${c.last.sender}: ` : ''
    const f = c.last.file
    const body = c.last.kind === 'file' && f ? (MEDIA[f.kind] ?? `📎 ${f.name}`) + (f.caption ? ', ' + plain(f.caption) : '')
      : c.last.kind === 'poll' && c.last.poll ? '📊 ' + c.last.poll.question : plain(c.last.text)
    return who + body
  }

  // Long press on a chat (phones) opens its menu, like a right click.
  let pressTimer: ReturnType<typeof setTimeout> | undefined
  let longPressed = false
  function pressStart(e: TouchEvent, c: Chat) {
    const t = e.touches[0]
    longPressed = false
    pressTimer = setTimeout(() => {
      longPressed = true
      navigator.vibrate?.(15)
      app.chatMenu = { chat: c, x: t.clientX, y: t.clientY }
    }, 480)
  }
  function pressEnd() { clearTimeout(pressTimer) }
</script>

<aside class="sidebar">
  <header>
    <button class="icon-btn" aria-label="Меню" onclick={() => (app.modal = { kind: 'drawer' })}><Icon name="menu" /></button>
    <div class="search">
      <Icon name="search" size={18} />
      <input placeholder={app.online ? 'Поиск' : 'Соединение…'} bind:value={app.search} oninput={searchInput}
        onkeydown={(e) => e.key === 'Escape' && clearSearch()} />
      {#if app.search}<button class="clear" aria-label="Очистить" onclick={clearSearch}><Icon name="close" size={16} /></button>{/if}
    </div>
    {#if app.passcode.enabled}<button class="icon-btn" aria-label="Заблокировать" title="Заблокировать (код-пароль)" onclick={() => app.lock()}><Icon name="lock" size={20} /></button>{/if}
  </header>

  {#if app.update && (app.update.state === 'ready' || app.update.state === 'manual')}
    <button class="link-banner update-banner" onclick={() => app.applyUpdate()}>
      <Icon name="download" size={20} />
      <span class="grow">{app.update.state === 'manual'
        ? `Вышла версия ${app.update.latest} — обновите в AltStore / SideStore`
        : app.update.install === 'restart'
          ? `Обновление ${app.update.latest} установится при перезапуске — перезапустить сейчас`
          : `Доступна версия ${app.update.latest} — установить`}</span>
    </button>
  {/if}
  {#if app.links.length}
    <button class="link-banner" onclick={() => (app.modal = { kind: 'approveLink', deviceId: app.links[0].deviceId })}>
      <Icon name="device" size={20} />
      <span>Запрос на вход: <b>{app.links[0].name || 'новое устройство'}</b></span>
    </button>
  {/if}

  <div class="list">
    {#if inviteLink}
      <button class="chat-item" onclick={joinLink}>
        <span class="join-ic"><Icon name="link" size={24} /></span>
        <div class="meta"><span class="name">Вступить в группу</span><span class="preview">по ссылке-приглашению</span></div>
      </button>
    {/if}
    {#if q && filtered.length}<div class="section-title">Чаты</div>{/if}
    {#each filtered as c (c.id)}
      <button class="chat-item" class:active={c.id === app.currentId} onclick={() => { if (longPressed) longPressed = false; else app.openChat(c.id) }}
        oncontextmenu={(e) => { e.preventDefault(); app.chatMenu = { chat: c, x: e.clientX, y: e.clientY } }}
        ontouchstart={(e) => pressStart(e, c)} ontouchend={pressEnd} ontouchmove={pressEnd}>
        <Avatar name={c.name} id={c.peerId || c.id} user={c.peerId} photo={c.peerAvatar} src={c.photo} size={54} />
        <div class="meta">
          <div class="row">
            <span class="name">
              {#if !c.active}<span class="inactive" title="Вы не участник"><Icon name="lock" size={13} /></span>{/if}
              <span class="nm">{c.name}</span>
              {#if (c.mutedUntil ?? 0) > Date.now()}<span class="muted-ic" title="Без звука"><Icon name="bell-off" size={14} /></span>{/if}
            </span>
            <span class="time">{chatListTime(c.last?.sentAt ?? c.updatedAt)}</span>
          </div>
          <div class="row">
            {#if app.typingIn(c.id)}
              <span class="preview typing">{c.kind === 'direct' ? 'печатает…' : `${app.typingIn(c.id)} печатает…`}</span>
            {:else}
              <span class="preview">{preview(c)}</span>
            {/if}
            {#if (app.unread[c.id] ?? 0) > 0}<span class="badge" class:quiet={(c.mutedUntil ?? 0) > Date.now()}>{app.unread[c.id]}</span>
            {:else if c.pinned}<span class="pin-ic" title="Закреплён"><Icon name="pin" size={16} /></span>{/if}
          </div>
        </div>
      </button>
    {:else}
      {#if !q}
        <div class="empty muted">Пока нет чатов.<br />Найдите собеседника по нику в поиске выше.</div>
      {/if}
    {/each}

    {#if q}
      <div class="section-title">Глобальный поиск</div>
      {#each people as u (u.userId)}
        <button class="chat-item" onclick={() => app.openDirect(u.username)}>
          <Avatar name={u.name || u.username} id={u.userId} user={u.userId} photo={u.avatarId} size={54} />
          <div class="meta">
            <span class="name">{u.name || u.username}</span>
            <span class="preview">@{u.username}</span>
          </div>
        </button>
      {:else}
        <div class="empty muted small">
          {#if app.searching}Поиск…{:else if q.length < 2}Введите ник (от 2 символов){:else}Пользователи не найдены{/if}
        </div>
      {/each}
    {/if}
  </div>

  {#if menu}
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div class="menu-backdrop" onclick={() => (menu = false)}></div>
    <div class="fab-menu">
      <button onclick={() => { menu = false; app.modal = { kind: 'newDirect' } }}><Icon name="user" size={20} /> Новый чат</button>
      <button onclick={() => { menu = false; app.modal = { kind: 'newGroup' } }}><Icon name="group" size={20} /> Новая группа</button>
    </div>
  {/if}
  <button class="fab" class:open={menu} aria-label="Новое сообщение" title="Новое сообщение" onclick={() => (menu = !menu)}>
    <Icon name={menu ? 'close' : 'pencil'} />
  </button>
</aside>

<style>
  .sidebar { position: relative; display: flex; flex-direction: column; background: var(--bg-sidebar); border-right: 1px solid var(--border); min-width: 0; height: 100%; }
  header { display: flex; align-items: center; gap: 8px; padding: 8px 12px; height: 56px; }
  .search { flex: 1; display: flex; align-items: center; gap: 8px; background: var(--bg-input); border-radius: 20px; padding: 0 14px; height: 38px; color: var(--text-muted); }
  .search input { flex: 1; background: transparent; border: 0; outline: 0; min-width: 0; }
  .search input::placeholder { color: var(--text-muted); }
  .list { flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain; -webkit-overflow-scrolling: touch; padding: 0 6px 80px; }
  .chat-item { width: 100%; display: flex; gap: 12px; align-items: center; padding: 7px 10px; border-radius: 10px; text-align: left;
    transition: background .15s, transform .1s; }
  .chat-item:active { transform: scale(.985); }
  .chat-item:hover { background: var(--bg-hover); }
  .chat-item.active { background: var(--bg-active); color: var(--active-text); }
  .meta { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px; }
  .row { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .name { font-weight: 600; font-size: 15px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; display: flex; align-items: center; gap: 4px; }
  .inactive { color: var(--text-muted); display: inline-flex; }
  .time { color: var(--text-muted); font-size: 12px; flex-shrink: 0; }
  .chat-item.active .time, .chat-item.active .preview { color: var(--active-sub); }
  .preview { color: var(--text-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: 14px; }
  .typing { color: var(--accent-light); }
  .badge { background: var(--accent); color: #fff; font-size: 12px; font-weight: 600; min-width: 22px; height: 22px; padding: 0 7px; border-radius: 11px; display: flex; align-items: center; justify-content: center; }
  .chat-item.active .badge { background: #fff; color: var(--bg-active); }
  .badge.quiet { background: var(--quiet); }
  .nm { overflow: hidden; text-overflow: ellipsis; }
  .muted-ic, .pin-ic { color: var(--text-muted); display: inline-flex; flex-shrink: 0; }
  .chat-item.active .muted-ic, .chat-item.active .pin-ic { color: var(--active-sub); }
  .empty { text-align: center; padding: 40px 20px; line-height: 1.5; }
  .empty.small { padding: 16px; font-size: 14px; }
  .clear { color: var(--text-muted); display: inline-flex; }
  .section-title { font-size: 14px; font-weight: 500; color: var(--text-muted); padding: 10px 12px 6px; background: var(--bg-sidebar); }
  .update-banner { background: #2f6f3e; }
  .grow { flex: 1; }
  .link-banner { display: flex; align-items: center; gap: 10px; margin: 0 12px 6px; padding: 10px 14px; border-radius: 10px; background: var(--bg-active); color: #fff; text-align: left; font-size: 14px; }
  .menu-backdrop { position: absolute; inset: 0; z-index: 2; }
  .fab-menu { position: absolute; right: 20px; bottom: 84px; z-index: 3; background: var(--bg-menu); border-radius: 12px; padding: 6px 0; box-shadow: var(--shadow);
    display: flex; flex-direction: column; min-width: 200px; animation: pop .12s ease-out; transform-origin: bottom right; }
  .fab-menu button { display: flex; align-items: center; gap: 14px; padding: 10px 18px; font-size: 15px; text-align: left; }
  .fab-menu button:hover { background: var(--bg-hover); }
  @keyframes pop { from { transform: scale(.85); opacity: 0; } }
  .fab { z-index: 3; }
  .fab.open { transform: rotate(90deg); }
  .fab { position: absolute; right: 20px; bottom: 20px; width: 54px; height: 54px; border-radius: 50%; background: var(--accent); color: #fff;
    display: flex; align-items: center; justify-content: center; box-shadow: var(--shadow); transition: transform .15s, filter .15s; }
  .fab:hover { filter: brightness(1.1); transform: scale(1.04); }
  .join-ic { width: 54px; height: 54px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
</style>
