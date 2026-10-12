<script lang="ts">
  // In-chat search (replaces the header): results newest first, ↑/↓ walk
  // through them and jump to each one.
  import { app } from '../lib/app.svelte'
  import { Messenger, type Message } from '../lib/api'
  import { plain } from '../lib/format'
  import { timeHM } from '../lib/util'
  import Icon from './Icon.svelte'

  let { chatId, onclose }: { chatId: string; onclose: () => void } = $props()
  let q = $state('')
  let results = $state<Message[]>([])
  let pos = $state(-1)
  let busy = $state(false)
  let showList = $state(false)
  let input: HTMLInputElement | undefined = $state()
  let timer: ReturnType<typeof setTimeout> | undefined
  let seq = 0

  $effect(() => { input?.focus() })

  function onInput() {
    clearTimeout(timer)
    timer = setTimeout(run, 250)
  }
  async function run() {
    const query = q.trim()
    const n = ++seq
    if (!query) { results = []; pos = -1; return }
    busy = true
    const list = (await Messenger.Search(chatId, query).catch(() => null)) ?? []
    if (n !== seq) return
    busy = false
    results = list
    pos = list.length ? 0 : -1
    if (list.length) app.jumpTo(chatId, list[0].id)
  }
  function go(i: number) {
    if (i < 0 || i >= results.length) return
    pos = i
    showList = false
    app.jumpTo(chatId, results[i].id)
  }
  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') onclose()
    else if (e.key === 'Enter') { e.preventDefault(); go(e.shiftKey ? pos - 1 : pos + 1) }
  }
  function snippet(m: Message) {
    const t = plain(m.text || m.file?.caption || '')
    const i = t.toLowerCase().indexOf(q.trim().toLowerCase())
    return i > 30 ? '…' + t.slice(i - 20) : t
  }
</script>

<header class="search">
  <button class="icon-btn" aria-label="Закрыть поиск" onclick={onclose}><Icon name="back" /></button>
  <input bind:this={input} bind:value={q} oninput={onInput} onkeydown={key} placeholder="Поиск" autocomplete="off" />
  {#if busy}<span class="spin"></span>
  {:else if q.trim()}
    <button class="count" onclick={() => (showList = !showList)} disabled={!results.length}>{results.length ? `${pos + 1} из ${results.length}` : 'Не найдено'}</button>
  {/if}
  <button class="icon-btn" aria-label="Более раннее" disabled={pos >= results.length - 1} onclick={() => go(pos + 1)}><Icon name="up" size={20} /></button>
  <button class="icon-btn" aria-label="Более позднее" disabled={pos <= 0} onclick={() => go(pos - 1)}><Icon name="down" size={20} /></button>
</header>
{#if showList && results.length}
  <div class="list">
    {#each results as m, i (m.id)}
      <button class="hit" class:cur={i === pos} onclick={() => go(i)}>
        <span class="who">{m.outgoing ? 'Вы' : m.sender}</span>
        <span class="when muted">{new Date(m.sentAt).toLocaleDateString('ru')} {timeHM(m.sentAt)}</span>
        <span class="what">{snippet(m)}</span>
      </button>
    {/each}
  </div>
{/if}

<style>
  .search { display: flex; align-items: center; gap: 6px; height: 56px; padding: 0 8px; background: var(--bg-header); border-bottom: 1px solid var(--border); }
  input { flex: 1; min-width: 0; height: 38px; border-radius: 19px; padding: 0 14px; background: var(--bg-input); border: 0; outline: 0; font-size: 15px; }
  .count { font-size: 13px; color: var(--text-muted); white-space: nowrap; padding: 4px 6px; border-radius: 8px; }
  .count:not(:disabled):hover { background: var(--bg-hover); }
  .icon-btn:disabled { opacity: .35; }
  .spin { width: 16px; height: 16px; border: 2px solid var(--accent-light); border-right-color: transparent; border-radius: 50%; animation: sp .8s linear infinite; }
  @keyframes sp { to { transform: rotate(360deg) } }
  .list { position: absolute; top: 56px; left: 0; right: 0; z-index: 15; max-height: 50%; overflow-y: auto; background: var(--bg-sidebar);
    border-bottom: 1px solid var(--border); box-shadow: var(--shadow); }
  .hit { display: grid; grid-template-columns: 1fr auto; gap: 2px 8px; width: 100%; padding: 8px 16px; text-align: left; }
  .hit:hover, .hit.cur { background: var(--bg-hover); }
  .who { font-weight: 600; font-size: 14px; }
  .when { font-size: 12px; }
  .what { grid-column: 1 / -1; font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--text-muted); }
</style>
