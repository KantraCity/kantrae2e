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
  import { app } from '../lib/app.svelte'
  import type { Message } from '../lib/api'
  import { dayLabel } from '../lib/util'
  import Bubble from './Bubble.svelte'
  import Icon from './Icon.svelte'
  import { swipe, tick, type SwipeOpts } from '../lib/gestures'

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
      !!a && !!b && a.kind !== 'system' && b.kind !== 'system' && a.senderId === b.senderId && a.outgoing === b.outgoing &&
      Math.abs(a.sentAt - b.sentAt) < 5 * 60000 && new Date(a.sentAt).toDateString() === new Date(b.sentAt).toDateString()
    for (let i = ms.length - 1; i >= 0; i--) {
      const m = ms[i]
      const album = albums.get(m.id)
      // A sent message keeps the key of its temporary copy (app.settleSent):
      // the row is not re-created when the server's copy replaces it.
      out.push({ kind: 'msg', m, first: !same(ms[i - 1], m), last: !same(m, ms[i + 1]), key: (m as Message & { localKey?: string }).localKey ?? m.id,
        animate: m.sentAt > openedAt, album: album && album.length > 1 ? album : undefined })
      const day = new Date(m.sentAt).toDateString()
      if (i === 0 || new Date(ms[i - 1].sentAt).toDateString() !== day) {
        out.push({ kind: 'day', label: dayLabel(m.sentAt), key: 'd' + day })
      }
    }
    return out
  })

  // A new message arrives. Nothing is laid out again frame by frame (that
  // was the stutter): the row takes its place at once, the messages above,
  // pushed up by it, slide there smoothly (transform only, on the GPU), and
  // the new bubble flies in — from the message box or the sticker it was
  // sent from (app.flyFrom), from below for others' messages.
  const EASE = 'cubic-bezier(.22, .9, .3, 1)'
  function arrive(node: HTMLElement, m: Message) {
    const sc = scroller
    if (!sc || Math.abs(sc.scrollTop) > 80) return // reading older messages: just appear
    const h = node.getBoundingClientRect().height
    const view = sc.getBoundingClientRect()
    for (const el of sc.children) {
      if (el === node || !(el instanceof HTMLElement)) continue
      const r = el.getBoundingClientRect()
      if (r.bottom < view.top - h || r.top > view.bottom) continue
      el.animate([{ transform: `translateY(${h}px)` }, { transform: 'none' }], { duration: 320, easing: EASE })
    }
    const bubble = node.querySelector<HTMLElement>('.bubble') ?? node
    const from = app.flyFrom[m.id]
    delete app.flyFrom[m.id]
    if (from) {
      const t = bubble.getBoundingClientRect()
      const s = Math.max(0.35, Math.min(1.6, from.width / Math.max(1, t.width)))
      const dx = from.left + from.width / 2 - (t.left + t.width / 2)
      const dy = from.top + from.height / 2 - (t.top + t.height / 2)
      bubble.style.transformOrigin = 'center'
      // A slight arc: it lifts off, then settles into its place.
      bubble.animate([
        { transform: `translate(${dx}px, ${dy}px) scale(${s})`, opacity: 0.85 },
        { transform: `translate(${dx * 0.35}px, ${dy * 0.35 - 18}px) scale(${(s + 1) / 2})`, opacity: 1, offset: 0.55 },
        { transform: 'none', opacity: 1 },
      ], { duration: 460, easing: EASE })
    } else {
      bubble.style.transformOrigin = m.outgoing ? 'bottom right' : 'bottom left'
      bubble.animate([
        { transform: `translate(${m.outgoing ? 16 : -10}px, ${Math.min(h, 48)}px) scale(.9)`, opacity: 0 },
        { transform: 'none', opacity: 1 },
      ], { duration: 360, easing: EASE })
    }
  }

  // Phones: dragging the messages closes the keyboard (as in Telegram).
  let touchY: number | null = null
  function touchStartList(e: TouchEvent) { touchY = e.touches[0]?.clientY ?? null }
  function touchMoveList(e: TouchEvent) {
    if (touchY === null) return
    const y = e.touches[0]?.clientY ?? touchY
    if (Math.abs(y - touchY) < 10) return
    touchY = null
    const el = document.activeElement as HTMLElement | null
    if (el && (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT')) el.blur()
  }

  function onScroll() {
    if (!scroller) return
    // In a column-reverse scroller scrollTop is 0 at the bottom, negative above.
    atBottom = Math.abs(scroller.scrollTop) < 80
    if (atBottom) {
      newBelow = 0
      // Back at the newest messages: drop the rendered history (DOM stays small).
      if (shown > PAGE * 2) shown = PAGE
    }
  }

  // Swipe a message to the left to reply to it (Telegram): an arrow appears
  // at the right, a long enough swipe starts the reply.
  const REPLY_AT = 56
  function replySwipe(m: Message): SwipeOpts {
    let armed = false
    return {
      dir: 'left',
      enabled: !!app.chats.find((c) => c.id === chatId)?.active && !m.id.startsWith('local-') && !m.pending && app.selected.length === 0,
      ignore: 'audio, .wave, .speed, .poll',
      move(d) {
        const el = document.getElementById('row-' + m.id)
        if (!el) return
        const x = Math.min(d, REPLY_AT + 24) // resists past the mark
        el.style.transition = 'none'
        el.style.transform = `translateX(${-x}px)`
        el.style.setProperty('--reply-p', String(Math.min(1, d / REPLY_AT)))
        if (!armed && d >= REPLY_AT) { armed = true; tick() } else if (armed && d < REPLY_AT) armed = false
      },
      end(d) {
        const el = document.getElementById('row-' + m.id)
        if (el) {
          el.style.transition = 'transform .18s ease-out'
          el.style.transform = ''
          el.style.setProperty('--reply-p', '0')
        }
        if (d >= REPLY_AT) app.startReply(m)
        armed = false
      },
    }
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
  // Each jump is handled once: the effect also re-runs when messages change
  // (sending, typing a reply…), which used to scroll back to the last pinned
  // message / quote every time.
  let handledJump = 0
  $effect(() => {
    const j = app.jump
    if (!j || j.chatId !== chatId || j.n === handledJump) return
    const idx = all.findIndex((m) => m.id === j.id)
    if (idx < 0) return
    handledJump = j.n
    queueMicrotask(() => { if (app.jump?.n === j.n) app.jump = null }) // not again when the chat reopens
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
    // Near the top: render more of what is loaded, then load older pages.
    const io = new IntersectionObserver(async (es) => {
      if (!es.some((e) => e.isIntersecting)) return
      if (shown < all.length) shown += PAGE
      else if (app.hasOlder[chatId] && (await app.loadOlder(chatId))) shown += PAGE
    }, { root: scroller, rootMargin: '600px 0px 0px 0px' })
    if (sentinel) io.observe(sentinel)
    return () => io.disconnect()
  })
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="messages" bind:this={scroller} onscroll={onScroll} ontouchstart={touchStartList} ontouchmove={touchMoveList}>
  <div class="spacer"></div>
  {#each rows as r (r.key)}
    {#if r.kind === 'day'}
      <div class="day"><span>{r.label}</span></div>
    {:else if r.m.kind === 'system'}
      <div class="day sys" id={'msg-' + r.m.id} in:fly={{ y: 8, duration: r.animate ? 200 : 0 }}><span>{r.m.text}</span></div>
    {:else if r.animate}
      <div class="row" id={'row-' + r.m.id} use:arrive={r.m} use:swipe={replySwipe(r.m)}>
        <Bubble m={r.m} first={r.first} last={r.last} {group} album={r.album} />
        <span class="reply-hint"><Icon name="reply" size={18} /></span>
      </div>
    {:else}
      <div class="row" id={'row-' + r.m.id} use:swipe={replySwipe(r.m)}>
        <Bubble m={r.m} first={r.first} last={r.last} {group} album={r.album} />
        <span class="reply-hint"><Icon name="reply" size={18} /></span>
      </div>
    {/if}
  {/each}
  {#if shown < all.length || app.hasOlder[chatId]}
    <div class="older"><span class="spinner"></span></div>
  {:else}
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
  .day.sys span { font-weight: 400; max-width: 80%; text-align: center; line-height: 1.35; }
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
  .older { display: flex; justify-content: center; padding: 12px 0; }
  .older .spinner { width: 20px; height: 20px; border-radius: 50%; border: 2px solid var(--tint-3); border-top-color: var(--accent-light);
    animation: spin .8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg) } }
  .row { position: relative; }
  .reply-hint { position: absolute; right: -40px; top: 50%; width: 32px; height: 32px; margin-top: -16px; border-radius: 50%;
    display: flex; align-items: center; justify-content: center; color: #fff; background: rgba(0, 0, 0, .3);
    opacity: var(--reply-p, 0); transform: scale(calc(.5 + .5 * var(--reply-p, 0))); pointer-events: none; }
</style>
