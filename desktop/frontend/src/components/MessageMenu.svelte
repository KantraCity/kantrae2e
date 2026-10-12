<script lang="ts">
  // Telegram's message context menu: quick reactions + actions.
  import { app } from '../lib/app.svelte'
  import { scale } from 'svelte/transition'
  import Icon from './Icon.svelte'
  import { plain } from '../lib/format'

  import EmojiPanel from './EmojiPanel.svelte'
  import { emojiPrefs } from '../lib/emoji.svelte'

  // Quick reactions: the user's most used first, like Telegram.
  const QUICK = [...new Set([...emojiPrefs.recent.slice(0, 3), '👍', '❤️', '🔥', '😂', '😮', '😢', '🙏'])].slice(0, 7)
  let allReactions = $state(false)
  // Captured once: actions run after the menu (app.menu) is closed.
  const menu = app.menu!
  const m = menu.m
  const chat = $derived(app.chats.find((c) => c.id === m.chatId))
  const canEdit = $derived(m.outgoing && m.kind === 'text' && chat?.active)
  const canDeleteAll = $derived(m.outgoing || chat?.kind === 'direct')
  let el: HTMLDivElement | undefined = $state()

  // Keep the menu inside the window.
  const pos = $derived.by(() => {
    const w = 280, h = allReactions ? 640 : 380
    const x = Math.min(menu.x, window.innerWidth - w - 8)
    const y = menu.y + h > window.innerHeight ? Math.max(8, Math.min(menu.y, window.innerHeight - 8) - h) : menu.y
    return { x: Math.max(8, x), y: Math.max(8, y) }
  })

  function close() { app.menu = null }
  function act(fn: () => void) { close(); fn() }

  async function copy() {
    try {
      await navigator.clipboard.writeText(plain(m.text || m.file?.caption || ''))
      app.toast('Текст скопирован')
    } catch { app.toast('Не удалось скопировать', 'error') }
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && close()} onresize={close} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={close} oncontextmenu={(e) => { e.preventDefault(); close() }}></div>
<div class="menu" transition:scale|global={{ start: 0.9, duration: 140 }} bind:this={el} style="left:{pos.x}px;top:{pos.y}px" role="menu">
  {#if chat?.active}
    <div class="reactions">
      {#each QUICK as r}
        <button class="r" class:mine={m.reactions?.some((x) => x.mine && x.emoji === r)} onclick={() => act(() => { emojiPrefs.use(r); app.react(m, r) })}>{r}</button>
      {/each}
      <button class="r more" aria-label="Все реакции" class:open={allReactions} onclick={() => (allReactions = !allReactions)}><Icon name="down" size={18} /></button>
    </div>
    {#if allReactions}
      <div class="all-reactions">
        <EmojiPanel mode="reaction" onpick={(e) => act(() => app.react(m, e))} onclose={close} />
      </div>
    {/if}
  {/if}
  {#if chat?.active}<button onclick={() => act(() => app.startReply(m))}><Icon name="reply" size={20} /> Ответить</button>{/if}
  {#if m.text || m.file?.caption}<button onclick={() => act(copy)}><Icon name="copy" size={20} /> Копировать текст</button>{/if}
  {#if canEdit}<button onclick={() => act(() => app.startEdit(m))}><Icon name="pencil" size={20} /> Изменить</button>{/if}
  {#if !app.sending[m.id] && (m.file?.kind === 'gif' || (m.file?.kind === 'video' && (m.file.dur ?? 0) <= 60))}
    <button onclick={() => act(() => app.saveAs(m, 'gif'))}><Icon name="gif" size={20} /> Сохранить в GIF</button>
  {/if}
  {#if !app.sending[m.id] && (m.file?.kind === 'sticker' || m.file?.kind === 'photo')}
    <button onclick={() => act(() => app.saveAs(m, 'sticker'))}><Icon name="smile" size={20} /> {m.file?.kind === 'sticker' ? 'Сохранить стикер' : 'Сохранить как стикер'}</button>
  {/if}
  {#if m.file && !app.sending[m.id] && m.file.kind !== 'sticker'}
    <button onclick={() => act(() => app.saveMedia(m))}><Icon name="download" size={20} /> {m.file.kind === 'photo' || m.file.kind === 'video' || m.file.kind === 'gif' ? 'Сохранить в галерею' : 'Сохранить файл'}</button>
  {/if}
  {#if !m.id.startsWith('local-')}
    <button onclick={() => act(() => (app.modal = { kind: 'pinMsg', m }))}><Icon name="pin" size={20} /> {m.pinned ? 'Открепить' : 'Закрепить'}</button>
  {/if}
  <button onclick={() => act(() => (app.modal = { kind: 'forward', chatId: m.chatId, ids: [m.id] }))}><Icon name="forward" size={20} /> Переслать</button>
  {#if !m.id.startsWith('local-')}
    <button onclick={() => act(() => app.toggleSelect(m.id))}><Icon name="select" size={20} /> Выделить</button>
  {/if}
  <button class="danger" onclick={() => act(() => (app.modal = { kind: 'deleteMsg', chatId: m.chatId, ids: [m.id], canAll: !!(canDeleteAll && chat?.active) }))}>
    <Icon name="trash" size={20} /> Удалить
  </button>
</div>

<style>
  .backdrop { position: fixed; inset: 0; z-index: 60; }
  .menu { position: fixed; z-index: 61; width: 280px; max-height: calc(100vh - 16px); overflow-y: auto; background: var(--bg-menu); border-radius: 12px; box-shadow: var(--shadow);
    padding: 6px 0; display: flex; flex-direction: column; transform-origin: top left; }
  .menu > button { display: flex; align-items: center; gap: 14px; padding: 9px 16px; text-align: left; font-size: 15px; }
  .menu > button:hover { background: var(--bg-hover); }
  .menu .danger { color: #e17076; }
  .reactions { display: flex; justify-content: space-between; padding: 2px 8px 6px; border-bottom: 1px solid var(--border); margin-bottom: 4px; }
  .r { font-size: 20px; width: 30px; height: 30px; border-radius: 50%; transition: transform .1s; }
  .r:hover { transform: scale(1.25); }
  .r.mine { background: var(--bg-active); }
  .r.more { color: var(--text-muted); display: inline-flex; align-items: center; justify-content: center; transition: transform .15s; }
  .r.more.open { transform: rotate(180deg); }
  .all-reactions { height: 300px; margin: -4px 0 6px; border-bottom: 1px solid var(--border); }
  @keyframes pop { from { transform: scale(.9); opacity: 0; } }
</style>
