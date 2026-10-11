<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { chatListTime } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  const q = $derived(app.search.trim().replace(/^@/, '').toLowerCase())
  const filtered = $derived(q ? app.chats.filter((c) => c.name.toLowerCase().includes(q)) : app.chats)
  // People found globally that are not already a private chat in the list.
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

  function preview(c: (typeof app.chats)[number]): string {
    if (!c.last) return c.active ? (c.kind === 'direct' ? 'Личный чат' : 'Нет сообщений') : 'Вы не участник'
    // Like Telegram: sender names only in groups.
    const who = c.last.outgoing ? 'Вы: ' : c.kind === 'group' && c.last.sender ? `${c.last.sender}: ` : ''
    const body = c.last.kind === 'file' ? `📎 ${c.last.file?.name ?? 'файл'}` : c.last.text
    return who + body
  }
</script>

<aside class="sidebar">
  <header>
    <button class="icon-btn" aria-label="Меню" onclick={() => (app.modal = { kind: 'settings' })}><Icon name="menu" /></button>
    <div class="search">
      <Icon name="search" size={18} />
      <input placeholder={app.online ? 'Поиск' : 'Соединение…'} bind:value={app.search} oninput={searchInput}
        onkeydown={(e) => e.key === 'Escape' && clearSearch()} />
      {#if app.search}<button class="clear" aria-label="Очистить" onclick={clearSearch}><Icon name="close" size={16} /></button>{/if}
    </div>
  </header>

  {#if app.links.length}
    <button class="link-banner" onclick={() => (app.modal = { kind: 'approveLink', deviceId: app.links[0].deviceId })}>
      <Icon name="device" size={20} />
      <span>Запрос на вход: <b>{app.links[0].name || 'новое устройство'}</b></span>
    </button>
  {/if}

  <div class="list">
    {#if q && filtered.length}<div class="section-title">Чаты</div>{/if}
    {#each filtered as c (c.id)}
      <button class="chat-item" class:active={c.id === app.currentId} onclick={() => app.openChat(c.id)}>
        <Avatar name={c.name} id={c.peerId || c.id} size={54} />
        <div class="meta">
          <div class="row">
            <span class="name">
              {#if !c.active}<span class="inactive" title="Вы не участник"><Icon name="lock" size={13} /></span>{/if}
              {c.name}
            </span>
            <span class="time">{chatListTime(c.last?.sentAt ?? c.updatedAt)}</span>
          </div>
          <div class="row">
            <span class="preview">{preview(c)}</span>
            {#if (app.unread[c.id] ?? 0) > 0}<span class="badge">{app.unread[c.id]}</span>{/if}
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
          <Avatar name={u.username} id={u.userId} size={54} />
          <div class="meta">
            <span class="name">{u.username}</span>
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
  .list { flex: 1; overflow-y: auto; padding: 0 6px 80px; }
  .chat-item { width: 100%; display: flex; gap: 12px; align-items: center; padding: 7px 10px; border-radius: 10px; text-align: left; }
  .chat-item:hover { background: var(--bg-hover); }
  .chat-item.active { background: var(--bg-active); }
  .meta { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px; }
  .row { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .name { font-weight: 600; font-size: 15px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; display: flex; align-items: center; gap: 4px; }
  .inactive { color: var(--text-muted); display: inline-flex; }
  .time { color: var(--text-muted); font-size: 12px; flex-shrink: 0; }
  .chat-item.active .time, .chat-item.active .preview { color: #cfe2f6; }
  .preview { color: var(--text-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: 14px; }
  .badge { background: var(--accent); color: #fff; font-size: 12px; font-weight: 600; min-width: 22px; height: 22px; padding: 0 7px; border-radius: 11px; display: flex; align-items: center; justify-content: center; }
  .chat-item.active .badge { background: #fff; color: var(--bg-active); }
  .empty { text-align: center; padding: 40px 20px; line-height: 1.5; }
  .empty.small { padding: 16px; font-size: 14px; }
  .clear { color: var(--text-muted); display: inline-flex; }
  .section-title { font-size: 14px; font-weight: 500; color: var(--text-muted); padding: 10px 12px 6px; background: var(--bg-sidebar); }
  .link-banner { display: flex; align-items: center; gap: 10px; margin: 0 12px 6px; padding: 10px 14px; border-radius: 10px; background: var(--bg-active); color: #fff; text-align: left; font-size: 14px; }
  .menu-backdrop { position: absolute; inset: 0; z-index: 2; }
  .fab-menu { position: absolute; right: 20px; bottom: 84px; z-index: 3; background: var(--bg-menu, #212d3b); border-radius: 12px; padding: 6px 0; box-shadow: var(--shadow);
    display: flex; flex-direction: column; min-width: 200px; animation: pop .12s ease-out; transform-origin: bottom right; }
  .fab-menu button { display: flex; align-items: center; gap: 14px; padding: 10px 18px; font-size: 15px; text-align: left; }
  .fab-menu button:hover { background: var(--bg-hover); }
  @keyframes pop { from { transform: scale(.85); opacity: 0; } }
  .fab { z-index: 3; }
  .fab.open { transform: rotate(90deg); }
  .fab { position: absolute; right: 20px; bottom: 20px; width: 54px; height: 54px; border-radius: 50%; background: var(--accent); color: #fff;
    display: flex; align-items: center; justify-content: center; box-shadow: var(--shadow); transition: transform .15s, filter .15s; }
  .fab:hover { filter: brightness(1.1); transform: scale(1.04); }
</style>
