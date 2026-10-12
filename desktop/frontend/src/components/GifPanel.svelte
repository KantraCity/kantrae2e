<script lang="ts">
  // Saved GIFs, like Telegram's GIF tab: tap to send, right click / long
  // press to remove. Synced across the account's devices.
  import { onMount } from 'svelte'
  import { fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { app } from '../lib/app.svelte'
  import type { Message } from '../lib/api'
  import Icon from './Icon.svelte'

  let { onclose }: { onclose: () => void } = $props()
  let loading = $state(!app.gifsLoaded)

  onMount(() => {
    app.loadGifs().finally(() => (loading = false))
  })

  function send(g: Message) {
    onclose()
    app.sendGif(g)
  }

  let removing = $state<string | null>(null)
  let pressTimer: ReturnType<typeof setTimeout> | undefined
  let longPressed = false
  function pressStart(g: Message) {
    longPressed = false
    pressTimer = setTimeout(() => { longPressed = true; navigator.vibrate?.(15); removing = g.id }, 480)
  }
  function pressEnd() { clearTimeout(pressTimer) }
  function click(g: Message) {
    if (longPressed) { longPressed = false; return }
    if (removing) { removing = null; return }
    send(g)
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onclose()} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={onclose}></div>
<div class="panel" transition:fly={{ y: 16, duration: 200, easing: cubicOut }}>
  <div class="head">
    <span class="title"><Icon name="gif" size={18} /> Сохранённые GIF</span>
    <button class="icon-btn" aria-label="Закрыть" onclick={onclose}><Icon name="close" size={18} /></button>
  </div>
  <div class="grid">
    {#each app.gifs as g (g.id)}
      <div class="cell" class:removing={removing === g.id}>
        <button class="gif" title="Отправить" onclick={() => click(g)}
          oncontextmenu={(e) => { e.preventDefault(); removing = g.id }}
          ontouchstart={() => pressStart(g)} ontouchend={pressEnd} ontouchmove={pressEnd}>
          {#if g.file?.thumb}<img class="thumb" src={g.file.thumb} alt="" />{/if}
          <img class="full" src={g.file?.url} alt="GIF" loading="lazy" />
        </button>
        {#if removing === g.id}
          <button class="remove" onclick={() => { removing = null; app.removeGif(g) }}><Icon name="trash" size={16} /> Удалить</button>
        {/if}
      </div>
    {:else}
      <div class="empty muted">
        {#if loading}Загрузка…{:else}
          Здесь будут ваши GIF.<br />Нажмите правой кнопкой (на телефоне — удерживайте) по GIF в чате и выберите «Сохранить GIF».
        {/if}
      </div>
    {/each}
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; z-index: 25; }
  .panel { position: absolute; right: 12px; bottom: calc(100% + 8px); z-index: 26; width: min(380px, calc(100vw - 24px)); max-height: min(420px, 60vh);
    display: flex; flex-direction: column; background: var(--bg-menu, #212d3b); border-radius: 16px; box-shadow: var(--shadow); overflow: hidden; }
  .head { display: flex; align-items: center; justify-content: space-between; padding: 8px 8px 6px 14px; }
  .title { display: inline-flex; align-items: center; gap: 8px; font-weight: 600; font-size: 14px; color: var(--text-muted); }
  .grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 4px; padding: 0 6px 6px; overflow-y: auto; min-height: 0; overscroll-behavior: contain; }
  .cell { position: relative; aspect-ratio: 1; }
  .gif { position: relative; width: 100%; height: 100%; border-radius: 10px; overflow: hidden; background: rgba(0,0,0,.25); display: block; transition: transform .12s; }
  .gif:hover { transform: scale(.97); }
  .gif img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .thumb { filter: blur(8px); }
  .cell.removing .gif { filter: brightness(.45); }
  .remove { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); display: inline-flex; align-items: center; gap: 6px;
    background: var(--danger); color: #fff; font-size: 13px; font-weight: 500; padding: 6px 10px; border-radius: 14px; white-space: nowrap; }
  .empty { grid-column: 1 / -1; text-align: center; padding: 28px 16px; font-size: 14px; line-height: 1.5; }
</style>
