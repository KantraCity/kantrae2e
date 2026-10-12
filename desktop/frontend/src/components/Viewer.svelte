<script lang="ts">
  // Full-screen photo/video viewer; ← → browse the chat's media.
  import type { Message } from '../lib/api'
  import { app, type Modal } from '../lib/app.svelte'
  import Icon from './Icon.svelte'

  let { m: start, list, back }: { m: Message; list?: Message[]; back?: Modal } = $props()
  // svelte-ignore state_referenced_locally
  let m = $state(start)
  const items = $derived(list ?? (app.messages[m.chatId] ?? []).filter((x) => x.file && (x.file.kind === 'photo' || x.file.kind === 'video' || x.file.kind === 'gif' || x.file.kind === 'sticker')))
  const idx = $derived(items.findIndex((x) => x.id === m.id))
  const url = $derived(app.mediaUrl(m))

  // svelte-ignore state_referenced_locally
  const backTo = back ?? null
  const close = () => (app.modal = backTo)
  function go(d: number) {
    const n = items[idx + d]
    if (n) m = n
  }
  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') close()
    else if (e.key === 'ArrowLeft') go(-1)
    else if (e.key === 'ArrowRight') go(1)
  }
</script>

<svelte:window onkeydown={key} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="viewer" onclick={(e) => e.target === e.currentTarget && close()}>
  <header>
    <div class="who"><b>{m.outgoing ? 'Вы' : m.sender}</b><span>{new Date(m.sentAt).toLocaleString('ru-RU')}</span></div>
    <button class="icon-btn" aria-label="Сохранить" title="Сохранить" onclick={() => app.saveMedia(m)}><Icon name="download" /></button>
    <button class="icon-btn" aria-label="Закрыть" onclick={close}><Icon name="close" /></button>
  </header>
  {#key m.id}
    {#if m.file?.kind === 'video' || m.file?.mime?.startsWith('video/')}
      <!-- svelte-ignore a11y_media_has_caption -->
      <video src={url} controls autoplay playsinline poster={m.file.thumb || undefined}></video>
    {:else}
      <img src={url} alt={m.file?.name} />
    {/if}
  {/key}
  {#if idx > 0}<button class="nav prev" aria-label="Предыдущее" onclick={() => go(-1)}><Icon name="back" size={30} /></button>{/if}
  {#if idx >= 0 && idx < items.length - 1}<button class="nav next" aria-label="Следующее" onclick={() => go(1)}><Icon name="back" size={30} /></button>{/if}
  {#if m.file?.caption}<div class="caption">{m.file.caption}</div>{/if}
</div>

<style>
  .viewer { position: fixed; inset: 0; z-index: 70; background: rgba(0, 0, 0, .92); display: flex; align-items: center; justify-content: center; animation: fade .15s; }
  header { position: absolute; top: 0; left: 0; right: 0; display: flex; align-items: center; gap: 6px; padding: 10px 14px; color: #fff; }
  .who { flex: 1; display: flex; flex-direction: column; font-size: 14px; }
  .who span { color: #aaa; font-size: 13px; }
  header .icon-btn { color: #fff; }
  img, video { max-width: 92vw; max-height: 84vh; object-fit: contain; border-radius: 4px; }
  .nav { position: absolute; top: 50%; transform: translateY(-50%); color: #fff; width: 56px; height: 56px; border-radius: 50%; background: rgba(255,255,255,.08); display: flex; align-items: center; justify-content: center; }
  .nav:hover { background: rgba(255,255,255,.18); }
  .prev { left: 16px; }
  .next { right: 16px; transform: translateY(-50%) rotate(180deg); }
  .caption { position: absolute; bottom: 16px; left: 50%; transform: translateX(-50%); color: #fff; background: rgba(0,0,0,.5); padding: 6px 14px; border-radius: 12px; max-width: 80vw; }
  @keyframes fade { from { opacity: 0; } }
</style>
