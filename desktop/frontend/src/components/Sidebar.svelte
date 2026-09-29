<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { chatListTime } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  const filtered = $derived(
    app.search.trim()
      ? app.chats.filter((c) => c.name.toLowerCase().includes(app.search.trim().toLowerCase()))
      : app.chats,
  )

  function preview(c: (typeof app.chats)[number]): string {
    if (!c.last) return c.active ? 'Нет сообщений' : 'Вы не участник'
    const who = c.last.outgoing ? 'Вы: ' : c.last.sender ? `${c.last.sender}: ` : ''
    const body = c.last.kind === 'file' ? `📎 ${c.last.file?.name ?? 'файл'}` : c.last.text
    return who + body
  }
</script>

<aside class="sidebar">
  <header>
    <button class="icon-btn" aria-label="Меню" onclick={() => (app.modal = { kind: 'settings' })}><Icon name="menu" /></button>
    <div class="search">
      <Icon name="search" size={18} />
      <input placeholder={app.online ? 'Поиск' : 'Соединение…'} bind:value={app.search} />
    </div>
  </header>

  <div class="list">
    {#each filtered as c (c.id)}
      <button class="chat-item" class:active={c.id === app.currentId} onclick={() => app.openChat(c.id)}>
        <Avatar name={c.name} id={c.id} size={54} />
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
      <div class="empty muted">
        {#if app.search}Ничего не найдено{:else}Пока нет чатов.<br />Создайте группу кнопкой ниже.{/if}
      </div>
    {/each}
  </div>

  <button class="fab" aria-label="Новая группа" title="Новая группа" onclick={() => (app.modal = { kind: 'newChat' })}>
    <Icon name="pencil" />
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
  .fab { position: absolute; right: 20px; bottom: 20px; width: 54px; height: 54px; border-radius: 50%; background: var(--accent); color: #fff;
    display: flex; align-items: center; justify-content: center; box-shadow: var(--shadow); transition: transform .15s, filter .15s; }
  .fab:hover { filter: brightness(1.1); transform: scale(1.04); }
</style>
