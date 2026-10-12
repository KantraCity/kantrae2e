<script lang="ts">
  // The attach sheet, like Telegram's on phones: recent photos and videos of
  // the gallery (numbered multi-select, sent as an album), a camera tile,
  // and buttons for the system gallery and for files. Without the native
  // gallery (desktop, browser) it is a small menu.
  import { onMount } from 'svelte'
  import { fade, fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { app } from '../lib/app.svelte'
  import { duration } from '../lib/media'
  import Icon from './Icon.svelte'

  let { onclose }: { onclose: () => void } = $props()

  type Item = { id: number; video: boolean; mime: string; w: number; h: number; dur: number; size: number; name: string }
  type Gallery = { state(): string; request(): void; list(offset: number, limit: number): string }
  const native = (window as unknown as { KantraGallery?: Gallery }).KantraGallery

  let perm = $state(native ? native.state() : 'none')
  let items = $state<Item[]>([])
  let selected = $state<number[]>([])
  let caption = $state('')
  let done = false
  let sending = $state(false)
  let mediaInput: HTMLInputElement | undefined = $state()
  let fileInput: HTMLInputElement | undefined = $state()
  let cameraInput: HTMLInputElement | undefined = $state()

  function more() {
    if (!native || done || perm === 'denied') return
    const next = JSON.parse(native.list(items.length, 60)) as Item[]
    if (next.length < 60) done = true
    items = [...items, ...next]
  }

  onMount(() => {
    // The permission dialog answers through this hook (KantraGallery.java).
    ;(window as unknown as { kantraGalleryChanged?: () => void }).kantraGalleryChanged = () => {
      perm = native?.state() ?? 'none'
      items = []
      done = false
      more()
    }
    if (perm === 'denied') native?.request()
    else more()
  })

  function toggle(it: Item) {
    selected = selected.includes(it.id) ? selected.filter((x) => x !== it.id) : [...selected, it.id]
    navigator.vibrate?.(8)
  }

  function onScroll(e: Event) {
    const el = e.currentTarget as HTMLElement
    if (el.scrollTop + el.clientHeight > el.scrollHeight - 400) more()
  }

  async function sendSelected() {
    if (!selected.length || sending) return
    sending = true
    try {
      const files = await Promise.all(selected.map(async (id) => {
        const it = items.find((x) => x.id === id)!
        const blob = await (await fetch(`/__gallery/file/${id}`)).blob()
        return new File([blob], it.name || (it.video ? 'video.mp4' : 'photo.jpg'), { type: it.mime || blob.type })
      }))
      const cap = caption
      onclose()
      await app.sendPrepared(files, true, cap)
    } catch {
      app.toast('Не удалось прочитать файлы из галереи', 'error')
      sending = false
    }
  }

  function poll() {
    const id = app.currentId
    onclose()
    if (id) app.modal = { kind: 'poll', chatId: id }
  }

  function picked(list: FileList | null | undefined, asMedia: boolean) {
    const files = Array.from(list ?? [])
    if (!files.length) return
    onclose()
    if (asMedia) app.sendPrepared(files, true, '')
    else app.pickFiles(files)
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onclose()} />

<input type="file" hidden multiple accept="image/*,video/*" bind:this={mediaInput} onchange={(e) => picked((e.currentTarget as HTMLInputElement).files, !native)} />
<input type="file" hidden multiple accept="*/*" bind:this={fileInput} onchange={(e) => picked((e.currentTarget as HTMLInputElement).files, false)} />
<input type="file" hidden accept="image/*,video/*" capture="environment" bind:this={cameraInput} onchange={(e) => picked((e.currentTarget as HTMLInputElement).files, false)} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" class:native onclick={onclose} transition:fade={{ duration: 160 }}></div>

{#if native}
  <div class="sheet" transition:fly={{ y: 500, duration: 260, easing: cubicOut, opacity: 1 }}>
    <div class="handle"></div>
    <div class="head">
      <span class="title">{selected.length ? `Выбрано: ${selected.length}` : 'Галерея'}</span>
      <button class="icon-btn" aria-label="Закрыть" onclick={onclose}><Icon name="close" size={20} /></button>
    </div>
    <div class="grid" onscroll={onScroll}>
      <button class="tile camera" onclick={() => cameraInput?.click()} aria-label="Камера"><Icon name="camera" size={30} /></button>
      {#if perm === 'denied'}
        <div class="perm">
          <p>Разрешите доступ к фото и видео, чтобы выбирать их здесь.</p>
          <button class="btn" onclick={() => native?.request()}>Разрешить</button>
        </div>
      {/if}
      {#each items as it (it.id)}
        {@const n = selected.indexOf(it.id) + 1}
        <button class="tile" class:sel={n > 0} onclick={() => toggle(it)}>
          <img src={`/__gallery/thumb/${it.id}`} alt="" loading="lazy" decoding="async" />
          {#if it.video}<span class="dur"><Icon name="play" size={10} /> {duration(it.dur)}</span>{/if}
          <span class="check" class:on={n > 0}>{n || ''}</span>
        </button>
      {/each}
    </div>
    {#if selected.length}
      <div class="send-bar" transition:fly={{ y: 40, duration: 180 }}>
        <input class="field" placeholder="Подпись" bind:value={caption} maxlength="1024" />
        <button class="send" disabled={sending} onclick={sendSelected} aria-label="Отправить">
          {#if sending}<span class="spin"></span>{:else}<Icon name="send" size={22} />{/if}
          <span class="count">{selected.length}</span>
        </button>
      </div>
    {:else}
      <div class="actions">
        <button onclick={() => mediaInput?.click()}><span class="ai blue"><Icon name="image" size={22} /></span>Галерея</button>
        <button onclick={() => fileInput?.click()}><span class="ai green"><Icon name="file" size={22} /></span>Файл</button>
        <button onclick={() => cameraInput?.click()}><span class="ai orange"><Icon name="camera" size={22} /></span>Камера</button>
        <button onclick={poll}><span class="ai yellow"><Icon name="poll" size={22} /></span>Опрос</button>
      </div>
    {/if}
  </div>
{:else}
  <div class="menu" transition:fly={{ y: 10, duration: 160 }}>
    <button onclick={() => mediaInput?.click()}><Icon name="image" size={20} /> Фото или видео</button>
    <button onclick={() => fileInput?.click()}><Icon name="file" size={20} /> Файл</button>
    <button onclick={poll}><Icon name="poll" size={20} /> Опрос</button>
  </div>
{/if}

<style>
  .backdrop { position: fixed; inset: 0; z-index: 40; }
  .backdrop.native { background: rgba(0,0,0,.45); }
  .sheet { position: fixed; left: 0; right: 0; bottom: 0; z-index: 41; height: min(72vh, 620px); display: flex; flex-direction: column;
    background: var(--bg-sidebar); border-radius: 16px 16px 0 0; box-shadow: var(--shadow); padding-bottom: env(safe-area-inset-bottom); }
  .handle { width: 40px; height: 4px; border-radius: 2px; background: rgba(255,255,255,.25); margin: 8px auto 2px; }
  .head { display: flex; align-items: center; justify-content: space-between; padding: 2px 8px 6px 16px; }
  .title { font-weight: 600; font-size: 16px; }
  .grid { flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain; display: grid; grid-template-columns: repeat(3, 1fr);
    gap: 2px; align-content: start; }
  .tile { position: relative; aspect-ratio: 1; background: rgba(255,255,255,.05); overflow: hidden; }
  .tile img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; transition: transform .15s; }
  .tile.sel img { transform: scale(.88); border-radius: 8px; }
  .camera { display: flex; align-items: center; justify-content: center; color: var(--text-muted); background: #0b121a; }
  .check { position: absolute; top: 6px; right: 6px; width: 24px; height: 24px; border-radius: 50%; border: 2px solid #fff;
    background: rgba(0,0,0,.25); color: #fff; font-size: 13px; font-weight: 600; display: flex; align-items: center; justify-content: center;
    transition: background .15s, transform .15s; }
  .check.on { background: var(--accent); border-color: var(--accent); transform: scale(1.05); }
  .dur { position: absolute; left: 5px; bottom: 5px; display: inline-flex; align-items: center; gap: 3px; font-size: 11px; color: #fff;
    background: rgba(0,0,0,.5); border-radius: 8px; padding: 1px 6px; }
  .perm { grid-column: 2 / -1; display: flex; flex-direction: column; justify-content: center; gap: 8px; padding: 10px; font-size: 13px; color: var(--text-muted); }
  .perm p { margin: 0; }
  .actions { display: flex; justify-content: space-around; padding: 10px 8px 14px; border-top: 1px solid var(--border); }
  .actions button { display: flex; flex-direction: column; align-items: center; gap: 6px; font-size: 13px; color: var(--text-muted); }
  .ai { width: 50px; height: 50px; border-radius: 50%; display: flex; align-items: center; justify-content: center; color: #fff; }
  .ai.blue { background: #3d8be0; }
  .ai.green { background: #3fbf5f; }
  .ai.orange { background: #f0a24a; }
  .ai.yellow { background: #e0b12f; }
  .send-bar { display: flex; align-items: center; gap: 8px; padding: 8px 10px 12px; border-top: 1px solid var(--border); }
  .send-bar .field { flex: 1; }
  .send { position: relative; width: 52px; height: 52px; border-radius: 50%; background: var(--accent); color: #fff; display: flex;
    align-items: center; justify-content: center; flex-shrink: 0; }
  .count { position: absolute; top: -4px; right: -4px; min-width: 22px; height: 22px; border-radius: 11px; padding: 0 6px; background: #fff;
    color: var(--accent); font-size: 12px; font-weight: 700; display: flex; align-items: center; justify-content: center; }
  .spin { width: 20px; height: 20px; border: 2px solid #fff; border-right-color: transparent; border-radius: 50%; animation: sp .8s linear infinite; }
  @keyframes sp { to { transform: rotate(360deg) } }
  .menu { position: absolute; left: 12px; bottom: calc(100% + 6px); z-index: 41; width: 220px; padding: 6px 0; background: var(--bg-menu, #212d3b);
    border-radius: 12px; box-shadow: var(--shadow); display: flex; flex-direction: column; }
  .menu button { display: flex; align-items: center; gap: 14px; padding: 10px 16px; font-size: 15px; text-align: left; }
  .menu button:hover { background: var(--bg-hover); }
</style>
