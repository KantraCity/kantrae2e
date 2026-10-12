<script lang="ts">
  // Telegram's "Send N photos / files" dialog: preview, caption, compress.
  import { onDestroy } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { fileSize, plural } from '../lib/util'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'

  let { files }: { files: File[] } = $props()
  const visual = (f: File) => f.type.startsWith('image/') || f.type.startsWith('video/')
  const allVisual = $derived(files.every(visual))
  let compress = $state(true)
  let asSticker = $state(false)
  const oneImage = $derived(files.length === 1 && files[0].type.startsWith('image/'))
  let caption = $state('')
  let busy = $state(false)
  // svelte-ignore state_referenced_locally
  const previews = files.map((f) => (visual(f) ? URL.createObjectURL(f) : ''))
  onDestroy(() => previews.forEach((u) => u && URL.revokeObjectURL(u)))

  const title = $derived(
    allVisual && compress
      ? `Отправить ${files.length > 1 ? files.length + ' ' + plural(files.length, 'медиафайл', 'медиафайла', 'медиафайлов') : files[0].type.startsWith('video/') ? 'видео' : 'фото'}`
      : `Отправить ${files.length > 1 ? files.length + ' ' + plural(files.length, 'файл', 'файла', 'файлов') : 'файл'}`,
  )

  async function send(e?: Event) {
    e?.preventDefault()
    if (busy) return
    busy = true
    const list = [...files], cap = caption, comp = compress && allVisual, st = asSticker && oneImage
    app.modal = null
    await app.sendPrepared(list, comp, cap, st)
  }
</script>

<Modal {title} onclose={() => (app.modal = null)} width={440}>
  <form onsubmit={send}>
    <div class="grid" class:single={files.length === 1}>
      {#each files as f, i}
        {#if previews[i] && compress && allVisual}
          {#if f.type.startsWith('video/')}
            <!-- svelte-ignore a11y_media_has_caption -->
            <video src={previews[i]} muted playsinline preload="metadata"></video>
          {:else}
            <img src={previews[i]} alt={f.name} />
          {/if}
        {:else}
          <div class="file"><span class="fi"><Icon name="file" size={22} /></span><span class="fn">{f.name}<br /><small class="muted">{fileSize(f.size)}</small></span></div>
        {/if}
      {/each}
    </div>
    {#if allVisual && !asSticker}
      <label class="check"><input type="checkbox" bind:checked={compress} /> Сжать изображения</label>
    {/if}
    {#if oneImage}
      <label class="check"><input type="checkbox" bind:checked={asSticker} /> Отправить как стикер</label>
    {/if}
    <!-- svelte-ignore a11y_autofocus -->
    <input class="field" placeholder="Подпись" bind:value={caption} autofocus maxlength="1024" />
    <div class="actions">
      <button type="button" class="btn flat" onclick={() => (app.modal = null)}>Отмена</button>
      <button class="btn" disabled={busy}>Отправить</button>
    </div>
  </form>
</Modal>

<style>
  form { display: flex; flex-direction: column; gap: 12px; }
  .grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 4px; max-height: 50vh; overflow-y: auto; }
  .grid.single { grid-template-columns: 1fr; }
  .grid img, .grid video { width: 100%; aspect-ratio: 1; object-fit: cover; border-radius: 6px; background: #000; }
  .grid.single img, .grid.single video { aspect-ratio: auto; max-height: 360px; object-fit: contain; }
  .file { grid-column: 1 / -1; display: flex; align-items: center; gap: 12px; }
  .fi { width: 44px; height: 44px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .fn { word-break: break-all; }
  .check { display: flex; gap: 8px; align-items: center; cursor: pointer; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
