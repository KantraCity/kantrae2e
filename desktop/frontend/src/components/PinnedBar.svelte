<script lang="ts">
  // Telegram's pinned bar under the chat header: shows one pinned message;
  // a tap jumps to it and moves on to the next (older) one.
  import { app } from '../lib/app.svelte'
  import { plain } from '../lib/format'
  import { fly } from 'svelte/transition'
  import Icon from './Icon.svelte'

  let { chatId }: { chatId: string } = $props()
  const list = $derived(app.pinned[chatId] ?? [])
  let idx = $state(0)
  const cur = $derived(list[Math.min(idx, list.length - 1)])

  $effect(() => { void chatId; idx = 0; app.loadPinned(chatId) })

  function preview(m: NonNullable<typeof cur>) {
    if (m.kind === 'poll' && m.poll) return '📊 ' + m.poll.question
    if (m.text) return plain(m.text)
    if (m.file) return m.file.caption || ({ photo: 'Фотография', video: 'Видео', voice: 'Голосовое сообщение', round: 'Видеосообщение', gif: 'GIF', sticker: 'Стикер' } as Record<string, string>)[m.file.kind] || m.file.name
    return ''
  }
  function tap() {
    if (!cur) return
    app.jumpTo(chatId, cur.id)
    idx = (idx + 1) % list.length
  }
</script>

{#if cur}
  <div class="pinned" transition:fly={{ y: -10, duration: 160 }}>
    {#if list.length > 1}
      <span class="ticks">{#each list.slice(0, 4) as _, i}<span class:on={i === Math.min(idx, 3)}></span>{/each}</span>
    {:else}
      <span class="ticks"><span class="on"></span></span>
    {/if}
    <button class="body" onclick={tap}>
      <span class="ttl">Закреплённое сообщение{list.length > 1 ? ` #${list.length - Math.min(idx, list.length - 1)}` : ''}</span>
      <span class="txt">{preview(cur)}</span>
    </button>
    <button class="icon-btn" aria-label="Открепить" title="Открепить" onclick={() => (app.modal = { kind: 'pinMsg', m: cur })}><Icon name="close" size={18} /></button>
  </div>
{/if}

<style>
  .pinned { display: flex; align-items: center; gap: 8px; padding: 4px 8px 4px 14px; background: var(--bg-header); border-bottom: 1px solid var(--border); min-height: 46px; }
  .ticks { display: flex; flex-direction: column; gap: 2px; align-self: stretch; padding: 4px 0; }
  .ticks span { flex: 1; width: 2px; border-radius: 1px; background: var(--accent-light); opacity: .35; }
  .ticks span.on { opacity: 1; }
  .body { flex: 1; min-width: 0; display: flex; flex-direction: column; text-align: left; }
  .ttl { color: var(--accent-light); font-size: 13px; font-weight: 600; }
  .txt { font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
