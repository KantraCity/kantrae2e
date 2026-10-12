<script lang="ts">
  // Context menu of a chat in the list (right click / long press).
  import { app } from '../lib/app.svelte'
  import { scale } from 'svelte/transition'
  import Icon from './Icon.svelte'

  const menu = app.chatMenu!
  const chat = menu.chat
  const pos = (() => {
    const w = 230, h = 180
    return { x: Math.max(8, Math.min(menu.x, window.innerWidth - w - 8)), y: menu.y + h > window.innerHeight ? Math.max(8, menu.y - h) : menu.y }
  })()

  function close() { app.chatMenu = null }
  function act(fn: () => void) { close(); fn() }
  const ask = (action: 'delete' | 'clear' | 'leave') => act(() => (app.modal = { kind: 'deleteChat', chatId: chat.id, action }))
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && close()} onresize={close} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={close} oncontextmenu={(e) => { e.preventDefault(); close() }}></div>
<div class="menu" transition:scale|global={{ start: 0.9, duration: 140 }} style="left:{pos.x}px;top:{pos.y}px" role="menu">
  <button onclick={() => act(() => app.openChat(chat.id))}><Icon name="chat" size={20} /> Открыть</button>
  <button onclick={() => act(() => (app.modal = { kind: 'chatInfo', chatId: chat.id }))}><Icon name="info" size={20} /> {chat.kind === 'group' ? 'Информация о группе' : 'Профиль'}</button>
  <button onclick={() => ask('clear')}><Icon name="broom" size={20} /> Очистить историю</button>
  {#if chat.kind === 'group'}
    <button class="danger" onclick={() => ask('leave')}><Icon name="leave" size={20} /> Покинуть группу</button>
  {:else}
    <button class="danger" onclick={() => ask('delete')}><Icon name="trash" size={20} /> Удалить чат</button>
  {/if}
</div>

<style>
  .backdrop { position: fixed; inset: 0; z-index: 60; }
  .menu { position: fixed; z-index: 61; width: 230px; background: var(--bg-menu, #212d3b); border-radius: 12px; box-shadow: var(--shadow);
    padding: 6px 0; display: flex; flex-direction: column; transform-origin: top left; }
  .menu > button { display: flex; align-items: center; gap: 14px; padding: 9px 16px; text-align: left; font-size: 15px; }
  .menu > button:hover { background: var(--bg-hover); }
  .menu .danger { color: #e17076; }
</style>
