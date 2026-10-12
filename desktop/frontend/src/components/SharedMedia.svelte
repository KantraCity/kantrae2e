<script lang="ts">
  // The chat profile's shared media, like Telegram: Медиа / Файлы /
  // Голосовые / Ссылки. Loaded from this device's decrypted history.
  import { app } from '../lib/app.svelte'
  import { Messenger, type Message } from '../lib/api'
  import { duration } from '../lib/media'
  import { fileSize } from '../lib/util'
  import { plain } from '../lib/format'
  import Icon from './Icon.svelte'

  let { chatId }: { chatId: string } = $props()
  type Tab = 'media' | 'files' | 'voice' | 'links'
  const TABS: [Tab, string][] = [['media', 'Медиа'], ['files', 'Файлы'], ['voice', 'Голосовые'], ['links', 'Ссылки']]
  let tab = $state<Tab>('media')
  let lists = $state<Partial<Record<Tab, Message[]>>>({})

  $effect(() => {
    const t = tab
    if (lists[t]) return
    Messenger.SharedMedia(chatId, t).then((l) => (lists[t] = l ?? [])).catch(() => (lists[t] = []))
  })
  const list = $derived(lists[tab])

  function open(m: Message) {
    const back = app.modal
    app.modal = { kind: 'viewer', m, list: lists.media, back: back ?? undefined }
  }
  function linkOf(m: Message) {
    return m.link?.url || /https?:\/\/\S+/i.exec(m.text)?.[0] || ''
  }
  function jump(m: Message) {
    app.modal = null
    app.openChat(m.chatId).then(() => app.jumpTo(m.chatId, m.id))
  }
</script>

<div class="shared">
  <nav class="tabs">
    {#each TABS as [t, label]}
      <button class:on={tab === t} onclick={() => (tab = t)}>{label}</button>
    {/each}
  </nav>
  {#if !list}
    <p class="muted empty">Загрузка…</p>
  {:else if !list.length}
    <p class="muted empty">{({ media: 'Нет фото и видео', files: 'Нет файлов', voice: 'Нет голосовых сообщений', links: 'Нет ссылок' } as Record<Tab, string>)[tab]}</p>
  {:else if tab === 'media'}
    <div class="grid">
      {#each list as m (m.id)}
        <button class="cell" onclick={() => open(m)}>
          {#if m.file?.thumb}<img src={m.file.thumb} alt="" loading="lazy" />{:else if m.file?.kind !== 'video'}<img src={app.mediaUrl(m)} alt="" loading="lazy" />{/if}
          {#if m.file?.kind === 'video'}<span class="dur"><Icon name="play" size={10} /> {duration(m.file.dur ?? 0)}</span>{/if}
          {#if m.file?.kind === 'gif'}<span class="dur">GIF</span>{/if}
        </button>
      {/each}
    </div>
  {:else if tab === 'links'}
    {#each list as m (m.id)}
      {@const url = linkOf(m)}
      <div class="row">
        <span class="ic link"><Icon name="link" size={20} /></span>
        <div class="txt">
          <a href={url} target="_blank" rel="noopener noreferrer" class="ttl">{m.link?.title || url}</a>
          <span class="sub muted">{plain(m.text).slice(0, 120)}</span>
        </div>
        <button class="icon-btn" title="Показать в чате" aria-label="Показать в чате" onclick={() => jump(m)}><Icon name="forward" size={18} /></button>
      </div>
    {/each}
  {:else}
    {#each list as m (m.id)}
      <div class="row">
        <span class="ic" class:voice={tab === 'voice'}><Icon name={tab === 'voice' ? (m.file?.kind === 'round' ? 'video' : 'mic') : 'file'} size={20} /></span>
        <div class="txt">
          <span class="ttl">{tab === 'voice' ? (m.file?.kind === 'round' ? 'Видеосообщение' : 'Голосовое сообщение') : m.file?.name}</span>
          <span class="sub muted">{tab === 'voice' ? duration(m.file?.dur ?? 0) : fileSize(m.file?.size ?? 0)} · {m.outgoing ? 'Вы' : m.sender} · {new Date(m.sentAt).toLocaleDateString('ru')}</span>
        </div>
        {#if tab === 'files'}<button class="icon-btn" title="Сохранить" aria-label="Сохранить" onclick={() => app.saveMedia(m)}><Icon name="download" size={18} /></button>{/if}
        <button class="icon-btn" title="Показать в чате" aria-label="Показать в чате" onclick={() => jump(m)}><Icon name="forward" size={18} /></button>
      </div>
    {/each}
  {/if}
</div>

<style>
  .shared { margin-top: 8px; border-top: 1px solid var(--border); }
  .tabs { display: flex; gap: 2px; padding: 6px 0; position: sticky; top: 0; background: var(--bg-modal); z-index: 1; }
  .tabs button { flex: 1; padding: 8px 4px; border-radius: 8px; font-size: 14px; font-weight: 500; color: var(--text-muted); position: relative; }
  .tabs button.on { color: var(--accent-light); }
  .tabs button.on::after { content: ''; position: absolute; left: 20%; right: 20%; bottom: 0; height: 3px; border-radius: 2px; background: var(--accent-light); }
  .tabs button:hover { background: var(--bg-hover); }
  .empty { text-align: center; padding: 24px 0; }
  .grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 2px; }
  .cell { position: relative; aspect-ratio: 1; overflow: hidden; background: var(--tint-1); }
  .cell img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .dur { position: absolute; left: 4px; bottom: 4px; display: inline-flex; align-items: center; gap: 3px; font-size: 11px; color: #fff;
    background: rgba(0,0,0,.5); border-radius: 8px; padding: 1px 6px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 8px 4px; }
  .ic { width: 44px; height: 44px; border-radius: 10px; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .ic.voice { border-radius: 50%; }
  .ic.link { background: #3fbf5f; }
  .txt { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .ttl { font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--text); text-decoration: none; }
  a.ttl:hover { text-decoration: underline; }
  .sub { font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
