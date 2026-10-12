<script lang="ts">
  // Kantra's own message list.
  // - The scroller is `flex-direction: column-reverse`: the browser keeps it
  //   pinned to the newest message by itself (scrollTop 0 = bottom), so it
  //   never "fails to scroll down" when images load or messages arrive.
  // - Only the newest PAGE messages are rendered; scrolling up renders more
  //   (the DOM stays small, scrolling stays smooth).
  // - New messages slide in; a ⌄ button with a counter appears when you are
  //   reading older messages.
  import { onMount } from 'svelte'
  import { fly } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { app } from '../lib/app.svelte'
  import type { Message } from '../lib/api'
  import { dayLabel } from '../lib/util'
  import Bubble from './Bubble.svelte'
  import Icon from './Icon.svelte'

  let { chatId, group }: { chatId: string; group: boolean } = $props()

  const PAGE = 60
  let shown = $state(PAGE)
  let scroller: HTMLDivElement | undefined = $state()
  let sentinel: HTMLDivElement | undefined = $state()
  let atBottom = $state(true)
  let newBelow = $state(0)
  // Messages sent after the chat was opened slide in; history does not.
  const openedAt = Date.now() - 1500

  const all = $derived(app.messages[chatId] ?? [])
  const visible = $derived(all.slice(Math.max(0, all.length - shown)))

  type Row =
    | { kind: 'day'; label: string; key: string }
    | { kind: 'msg'; m: Message; first: boolean; last: boolean; key: string; animate: boolean; album?: Message[] }

  // Newest first (the DOM order of a column-reverse list).
  const rows = $derived.by(() => {
    const out: Row[] = []
    // Photos/videos sent together (same album) become one item.
    const ms: Message[] = []
    const albums = new Map<string, Message[]>()
    for (const m of visible) {
      const al = m.file?.album
      const prev = ms[ms.length - 1]
      if (al && prev?.file?.album === al && prev.senderId === m.senderId) {
        albums.get(prev.id)!.push(m)
        continue
      }
      ms.push(m)
      if (al) albums.set(m.id, [m])
    }
    const same = (a?: Message, b?: Message) =>
      !!a && !!b && a.senderId === b.senderId && a.outgoing === b.outgoing &&
      Math.abs(a.sentAt - b.sentAt) < 5 * 60000 && new Date(a.sentAt).toDateString() === new Date(b.sentAt).toDateString()
    for (let i = ms.length - 1; i >= 0; i--) {
      const m = ms[i]
      const album = albums.get(m.id)
      out.push({ kind: 'msg', m, first: !same(ms[i - 1], m), last: !same(m, ms[i + 1]), key: m.id,
        animate: m.sentAt > openedAt, album: album && album.length > 1 ? album : undefined })
      const day = new Date(m.sentAt).toDateString()
      if (i === 0 || new Date(ms[i - 1].sentAt).toDateString() !== day) {
        out.push({ kind: 'day', label: dayLabel(m.sentAt), key: 'd' + day })
      }
    }
    return out
  })

  // A new message grows in from the bottom: the list above slides up
  // smoothly instead of jumping (the scroller is anchored at the bottom).
  function grow(node: HTMLElement) {
    const h = node.offsetHeight
    return {
      duration: 260,
      easing: cubicOut,
      css: (t: number) => `height:${t * h}px;opacity:${Math.min(1, t * 1.6)};transform:translateY(${(1 - t) * 12}px) scale(${0.96 + 0.04 * t});` +
        'overflow:visible;transform-origin:bottom',
    }
  }

  function onScroll() {
    if (!scroller) return
    // In a column-reverse scroller scrollTop is 0 at the bottom, negative above.
    atBottom = Math.abs(scroller.scrollTop) < 80
    if (atBottom) newBelow = 0
  }

  export function toBottom(smooth = true) {
    scroller?.scrollTo({ top: 0, behavior: smooth ? 'smooth' : 'auto' })
    newBelow = 0
  }

  // Count messages that arrive while reading older ones; own ones scroll down.
  let lastLen = 0 // set on the first run below
  let primed = false
  $effect(() => {
    const len = all.length
    if (!primed) {
      primed = true
      lastLen = len
      return
    }
    if (len > lastLen) {
      const latest = all[len - 1]
      if (latest?.outgoing) queueMicrotask(() => toBottom())
      else if (!atBottom) newBelow += len - lastLen
    }
    lastLen = len
  })

  // app.jumpTo: render enough history, scroll the message to the middle, flash it.
  $effect(() => {
    const j = app.jump
    if (!j || j.chatId !== chatId) return
    const idx = all.findIndex((m) => m.id === j.id)
    if (idx < 0) return
    if (all.length - idx > shown) shown = all.length - idx + 10
    setTimeout(() => {
      const el = document.getElementById('msg-' + j.id)
      if (!el) return
      el.scrollIntoView({ behavior: 'smooth', block: 'center' })
      el.classList.remove('flash')
      void el.offsetWidth
      el.classList.add('flash')
      setTimeout(() => el.classList.remove('flash'), 1300)
    }, 30)
  })

  onMount(() => {
    const io = new IntersectionObserver((es) => {
      if (es.some((e) => e.isIntersecting) && shown < all.length) shown += PAGE
    }, { root: scroller, rootMargin: '600px 0px 0px 0px' })
    if (sentinel) io.observe(sentinel)
    return () => io.disconnect()
  })
</script>

<div class="messages" bind:this={scroller} onscroll={onScroll}>
  <div class="spacer"></div>
  {#each rows as r (r.key)}
    {#if r.kind === 'day'}
      <div class="day"><span>{r.label}</span></div>
    {:else if r.animate}
      <div class="row" in:grow>
        <Bubble m={r.m} first={r.first} last={r.last} {group} album={r.album} />
      </div>
    {:else}
      <div class="row"><Bubble m={r.m} first={r.first} last={r.last} {group} album={r.album} /></div>
    {/if}
  {/each}
  {#if shown >= all.length}
    <div class="e2e-note"><Icon name="lock" size={14} /> Сообщения защищены сквозным шифрованием (MLS)</div>
  {/if}
  <div class="sentinel" bind:this={sentinel}></div>
</div>

{#if !atBottom}
  <button class="down" aria-label="Вниз" onclick={() => toBottom()} transition:fly={{ y: 20, duration: 160 }}>
    <Icon name="down" size={24} />
    {#if newBelow}<span class="count">{newBelow}</span>{/if}
  </button>
{/if}

<style>
  .messages {
    flex: 1; min-height: 0; overflow-y: auto; overflow-x: hidden;
    display: flex; flex-direction: column-reverse;
    padding: 8px max(16px, calc((100% - 760px) / 2)); overscroll-behavior: contain;
    animation: list-in .18s ease-out;
  }
  /* Pushes a short history to the bottom (column-reverse: first child = bottom). */
  .spacer { height: 6px; flex-shrink: 0; }
  .row { flex-shrink: 0; }
  .sentinel { height: 1px; flex-shrink: 0; margin-top: auto; }
  .e2e-note { align-self: center; display: flex; align-items: center; gap: 6px; font-size: 13px; color: #d4dde6; flex-shrink: 0;
    background: rgba(0, 0, 0, 0.3); border-radius: 12px; padding: 6px 12px; margin: 12px 0; text-align: center; }
  .day { display: flex; justify-content: center; margin: 10px 0; flex-shrink: 0; }
  .day span { background: rgba(0, 0, 0, 0.35); color: #fff; font-size: 13px; font-weight: 500; padding: 4px 10px; border-radius: 12px; }
  .down { position: absolute; right: 20px; bottom: 90px; z-index: 3; width: 48px; height: 48px; border-radius: 50%;
    background: var(--bg-sidebar); color: var(--text-muted); box-shadow: 0 2px 8px rgba(0,0,0,.4);
    display: flex; align-items: center; justify-content: center; transition: color .15s, transform .15s; }
  .down:hover { color: var(--text); transform: translateY(-2px); }
  .count { position: absolute; top: -6px; right: -4px; min-width: 22px; height: 22px; padding: 0 6px; border-radius: 11px;
    background: var(--accent); color: #fff; font-size: 12px; font-weight: 600; display: flex; align-items: center; justify-content: center; }
  @keyframes list-in { from { opacity: 0; transform: translateY(6px); } }
  @media (max-width: 720px) {
    .messages { padding: 8px; }
    .down { right: 12px; bottom: 76px; }
  }
</style>
