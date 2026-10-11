<script lang="ts">
  // Telegram's message context menu: quick reactions + actions.
  import { app } from '../lib/app.svelte'
  import Icon from './Icon.svelte'

  const QUICK = ['👍', '❤️', '🔥', '😂', '😮', '😢', '🙏']
  // Captured once: actions run after the menu (app.menu) is closed.
  const menu = app.menu!
  const m = menu.m
  const chat = $derived(app.chats.find((c) => c.id === m.chatId))
  const canEdit = $derived(m.outgoing && m.kind === 'text' && chat?.active)
  const canDeleteAll = $derived(m.outgoing || chat?.kind === 'direct')
  let el: HTMLDivElement | undefined = $state()

  // Keep the menu inside the window.
  const pos = (() => {
    const w = 240, h = 330
    const x = Math.min(menu.x, window.innerWidth - w - 8)
    const y = menu.y + h > window.innerHeight ? Math.max(8, menu.y - h) : menu.y
    return { x: Math.max(8, x), y }
  })()

  function close() { app.menu = null }
  function act(fn: () => void) { close(); fn() }

  async function copy() {
    try {
      await navigator.clipboard.writeText(m.text || m.file?.caption || '')
      app.toast('Текст скопирован')
    } catch { app.toast('Не удалось скопировать', 'error') }
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && close()} onresize={close} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={close} oncontextmenu={(e) => { e.preventDefault(); close() }}></div>
<div class="menu" bind:this={el} style="left:{pos.x}px;top:{pos.y}px" role="menu">
  {#if chat?.active}
    <div class="reactions">
      {#each QUICK as r}
        <button class="r" class:mine={m.reactions?.some((x) => x.mine && x.emoji === r)} onclick={() => act(() => app.react(m, r))}>{r}</button>
      {/each}
    </div>
  {/if}
  {#if chat?.active}<button onclick={() => act(() => app.startReply(m))}><Icon name="reply" size={20} /> Ответить</button>{/if}
  {#if m.text || m.file?.caption}<button onclick={() => act(copy)}><Icon name="copy" size={20} /> Копировать текст</button>{/if}
  {#if canEdit}<button onclick={() => act(() => app.startEdit(m))}><Icon name="pencil" size={20} /> Изменить</button>{/if}
  <button onclick={() => act(() => (app.modal = { kind: 'forward', chatId: m.chatId, ids: [m.id] }))}><Icon name="forward" size={20} /> Переслать</button>
  <button class="danger" onclick={() => act(() => (app.modal = { kind: 'deleteMsg', chatId: m.chatId, ids: [m.id], canAll: !!(canDeleteAll && chat?.active) }))}>
    <Icon name="trash" size={20} /> Удалить
  </button>
</div>

<style>
  .backdrop { position: fixed; inset: 0; z-index: 60; }
  .menu { position: fixed; z-index: 61; width: 240px; background: var(--bg-menu, #212d3b); border-radius: 12px; box-shadow: var(--shadow);
    padding: 6px 0; display: flex; flex-direction: column; animation: pop .1s ease-out; }
  .menu > button { display: flex; align-items: center; gap: 14px; padding: 9px 16px; text-align: left; font-size: 15px; }
  .menu > button:hover { background: var(--bg-hover); }
  .menu .danger { color: #e17076; }
  .reactions { display: flex; justify-content: space-between; padding: 2px 8px 6px; border-bottom: 1px solid var(--border); margin-bottom: 4px; }
  .r { font-size: 20px; width: 30px; height: 30px; border-radius: 50%; transition: transform .1s; }
  .r:hover { transform: scale(1.25); }
  .r.mine { background: var(--bg-active); }
  @keyframes pop { from { transform: scale(.9); opacity: 0; } }
</style>
