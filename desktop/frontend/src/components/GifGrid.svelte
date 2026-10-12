<script lang="ts">
  // Saved GIFs (the GIF tab of the emoji panel): tap to send, right click /
  // long press to remove. Synced across the account's devices.
  import { onMount } from 'svelte'
  import { app } from '../lib/app.svelte'
  import type { Message } from '../lib/api'
  import Icon from './Icon.svelte'

  let { onpick, kind = 'gif' }: { onpick: () => void; kind?: 'gif' | 'sticker' } = $props()
  // svelte-ignore state_referenced_locally
  let loading = $state(!app.savedLoaded[kind]) // the grid is re-created per kind ({#key})
  let fileInput: HTMLInputElement | undefined = $state()
  let adding = $state(false)
  async function add(files: FileList | null) {
    const f = files?.[0]
    if (fileInput) fileInput.value = ''
    if (!f) return
    adding = true
    await app.addSaved(f, kind)
    adding = false
  }

  onMount(() => {
    app.loadSaved(kind).finally(() => (loading = false))
  })

  function send(g: Message) {
    onpick()
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

<div class="grid">
    <button class="add" onclick={() => fileInput?.click()} disabled={adding} title={kind === 'gif' ? 'Добавить GIF или видео' : 'Добавить картинку'}>
      {#if adding}<span class="spinner"></span>{:else}<Icon name="plus" size={26} /><span>{kind === 'gif' ? 'GIF' : 'Стикер'}</span>{/if}
    </button>
    <input type="file" hidden bind:this={fileInput} accept={kind === 'gif' ? 'image/gif,video/*' : 'image/*'} onchange={(e) => add((e.currentTarget as HTMLInputElement).files)} />
    {#each app.saved[kind] as g (g.id)}
      <div class="cell" class:removing={removing === g.id}>
        <button class="gif" title="Отправить" onclick={() => click(g)}
          oncontextmenu={(e) => { e.preventDefault(); removing = g.id }}
          ontouchstart={() => pressStart(g)} ontouchend={pressEnd} ontouchmove={pressEnd}>
          {#if g.file?.thumb && kind === 'gif'}<img class="thumb" src={g.file.thumb} alt="" />{/if}
          {#if g.file?.mime?.startsWith('video/')}
            <video class="full" src={g.file?.url} autoplay loop muted playsinline></video>
          {:else}
            <img class="full" class:sticker={kind === 'sticker'} src={g.file?.url} alt="" loading="lazy" />
          {/if}
        </button>
        {#if removing === g.id}
          <button class="remove" onclick={() => { removing = null; app.removeSaved(g) }}><Icon name="trash" size={16} /> Удалить</button>
        {/if}
      </div>
    {:else}
      <div class="empty muted">
        {#if loading}Загрузка…{:else}
          {kind === 'gif' ? 'Здесь будут ваши GIF.' : 'Здесь будут ваши стикеры.'}<br />
          Нажмите правой кнопкой (на телефоне — удерживайте) по {kind === 'gif' ? 'GIF или видео' : 'стикеру или фото'} в чате → «Сохранить», или добавьте свой файл кнопкой +.
        {/if}
      </div>
    {/each}
</div>

<style>
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(96px, 1fr)); gap: 4px; padding: 6px; overflow-y: auto; min-height: 0; flex: 1;
    align-content: start; overscroll-behavior: contain; }
  .cell { position: relative; aspect-ratio: 1; }
  .gif { position: relative; width: 100%; height: 100%; border-radius: 10px; overflow: hidden; background: rgba(0,0,0,.25); display: block; transition: transform .12s; }
  .gif:hover { transform: scale(.97); }
  .gif img, .gif video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .gif img.sticker { object-fit: contain; }
  .add { aspect-ratio: 1; border-radius: 10px; border: 2px dashed rgba(255,255,255,.18); color: var(--text-muted); display: flex; flex-direction: column;
    align-items: center; justify-content: center; gap: 2px; font-size: 12px; transition: border-color .15s, color .15s; }
  .add:hover { border-color: var(--accent); color: var(--accent-light); }
  .spinner { width: 20px; height: 20px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: spin .8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg) } }
  .thumb { filter: blur(8px); }
  .cell.removing .gif { filter: brightness(.45); }
  .remove { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); display: inline-flex; align-items: center; gap: 6px;
    background: var(--danger); color: #fff; font-size: 13px; font-weight: 500; padding: 6px 10px; border-radius: 14px; white-space: nowrap; }
  .empty { grid-column: 1 / -1; text-align: center; padding: 28px 16px; font-size: 14px; line-height: 1.5; }
</style>
