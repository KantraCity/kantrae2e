<script lang="ts">
  import type { Message } from '../lib/api'
  import { app } from '../lib/app.svelte'
  import { timeHM, nameColor } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import MediaContent from './MediaContent.svelte'
  import { bigEmojiCount } from '../lib/emoji.svelte'

  let { m, first, last, group, album }: { m: Message; first: boolean; last: boolean; group: boolean; album?: Message[] } = $props()
  // The album's caption is on one of its items (the first, as sent).
  const albumCaption = $derived(album?.find((x) => x.file?.caption)?.file?.caption ?? '')
  // Round video messages have no bubble (as in Telegram); photos/videos a tight one.
  // 1–3 emoji alone are shown big without a bubble, like Telegram.
  const bigEmoji = $derived(m.kind === 'text' && !m.reply && !m.forwarded ? bigEmojiCount(m.text) : 0)
  const bare = $derived(((m.file?.kind === 'round' || m.file?.kind === 'sticker') && !m.reply && !m.forwarded) || bigEmoji > 0)
  const isVisual = $derived(!!album || m.file?.kind === 'photo' || m.file?.kind === 'video' || m.file?.kind === 'gif')

  // Context menu: right click, or a long press on touch screens.
  function openMenu(e: MouseEvent | { clientX: number; clientY: number }) {
    if ('preventDefault' in e) e.preventDefault()
    app.menu = { m, x: e.clientX, y: e.clientY }
  }
  let pressTimer: ReturnType<typeof setTimeout> | undefined
  function touchStart(e: TouchEvent) {
    const t = e.touches[0]
    pressTimer = setTimeout(() => openMenu({ clientX: t.clientX, clientY: t.clientY }), 450)
  }
  function touchEnd() { clearTimeout(pressTimer) }

  function jumpTo(id: string) {
    const el = document.getElementById('msg-' + id)
    if (!el) return
    el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    el.classList.add('flash')
    setTimeout(() => el.classList.remove('flash'), 1200)
  }

</script>

<div class="line" class:out={m.outgoing} class:last id={'msg-' + m.id}>
  {#if group && !m.outgoing}
    <div class="avatar-slot">{#if last}<Avatar name={m.sender} id={m.senderId} user={m.senderId} photo={m.senderAvatar} size={34} />{/if}</div>
  {/if}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="bubble" class:tail={last && !bare} class:joined-top={!first} class:joined-bottom={!last} class:bare class:visual={isVisual} oncontextmenu={openMenu} ontouchstart={touchStart} ontouchend={touchEnd} ontouchmove={touchEnd}>
    {#if group && first && !m.outgoing}
      <div class="sender" style="color:{nameColor(m.senderId)}">{m.sender}</div>
    {/if}
    {#if m.forwarded}
      <div class="fwd">Переслано от <b>{m.forwarded}</b></div>
    {/if}
    {#if m.reply}
      <button class="quote" onclick={() => jumpTo(m.reply!.id)}>
        <span class="q-name">{m.reply.sender || 'Сообщение'}</span>
        <span class="q-text">{m.reply.text}</span>
      </button>
    {/if}
    {#if m.origin === 'shared'}
      <div class="origin" title="Пересланo другим участником, пока ваших устройств не было в сети">
        <Icon name="history" size={13} /> из истории участника
      </div>
    {/if}
    {#if album}
      <div class="album" class:two={album.length === 2} class:many={album.length > 4}>
        {#each album as x (x.id)}
          <button class="album-item" onclick={() => (app.modal = { kind: 'viewer', m: x })}
            oncontextmenu={(e) => { e.preventDefault(); e.stopPropagation(); app.menu = { m: x, x: e.clientX, y: e.clientY } }}>
            {#if x.file?.thumb}<img class="a-thumb" src={x.file.thumb} alt="" />{/if}
            {#if x.file?.kind === 'video'}
              <span class="a-play"><Icon name="play" size={18} /></span>
            {:else if app.mediaUrl(x)}
              <img class="a-full" src={app.mediaUrl(x)} alt="" loading="lazy" onload={(e) => (e.currentTarget as HTMLImageElement).classList.add('show')} />
            {/if}
            {#if app.sending[x.id]}<span class="a-play"><span class="a-spin"></span></span>{/if}
          </button>
        {/each}
      </div>
      {#if albumCaption}<div class="album-caption">{albumCaption}</div>{/if}
    {:else if m.kind === 'file' && m.file}
      <MediaContent {m} />
    {:else}
      <span class="text" class:big-emoji={bigEmoji > 0} style={bigEmoji ? `--emoji-size:${[0, 64, 50, 40][bigEmoji]}px` : undefined}>{m.text}</span>
    {/if}
    {#if m.reactions?.length}
      <div class="reacts">
        {#each m.reactions as r (r.emoji)}
          <button class="react" class:mine={r.mine} onclick={() => app.react(m, r.emoji)}>{r.emoji}{#if r.count > 1} {r.count}{/if}</button>
        {/each}
      </div>
    {/if}
    <span class="time">
      {#if m.edited}<span class="edited">изменено</span>{/if}
      {timeHM(m.sentAt)}
      {#if app.failed[m.id]}
        <button class="failed" title="Не отправлено — нажмите, чтобы повторить" onclick={() => app.failed[m.id]?.()}>!</button>
      {:else if app.sending[m.id]}
        <span class="check pending" title="Отправляется"><Icon name="clock" size={14} /></span>
      {:else if m.outgoing}
        <span class="check" title={m.read ? 'Прочитано' : 'Доставлено'}><Icon name={m.read ? 'double' : 'check'} size={15} /></span>
      {/if}
    </span>
  </div>
</div>

<style>
  .line { display: flex; align-items: flex-end; gap: 8px; margin-bottom: 2px; }
  .line.last { margin-bottom: 8px; }
  .line.out { justify-content: flex-end; }
  .avatar-slot { width: 34px; flex-shrink: 0; }
  .bubble {
    position: relative; max-width: min(520px, 75%);
    background: var(--bubble-in); border-radius: var(--radius-bubble);
    padding: 6px 10px 7px 11px; line-height: 1.38; font-size: 15px;
    word-wrap: break-word; overflow-wrap: anywhere;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.25);
  }
  .out .bubble { background: var(--bubble-out); }
  /* Telegram groups consecutive messages: tighter inner corners. */
  .bubble.joined-top { border-top-left-radius: 6px; }
  .bubble.joined-bottom { border-bottom-left-radius: 6px; }
  .out .bubble.joined-top { border-top-left-radius: var(--radius-bubble); border-top-right-radius: 6px; }
  .out .bubble.joined-bottom { border-bottom-left-radius: var(--radius-bubble); border-bottom-right-radius: 6px; }
  .bubble.tail { border-bottom-left-radius: 4px; }
  .out .bubble.tail { border-bottom-left-radius: var(--radius-bubble); border-bottom-right-radius: 4px; }
  .bubble.tail::before {
    content: ''; position: absolute; bottom: 0; left: -7px; width: 12px; height: 16px;
    background: radial-gradient(circle at 0 0, transparent 11px, var(--bubble-in) 12px);
  }
  .out .bubble.tail::before { left: auto; right: -7px; background: radial-gradient(circle at 100% 0, transparent 11px, var(--bubble-out) 12px); }
  .text { white-space: pre-wrap; }
  .bubble.bare { background: transparent; box-shadow: none; padding: 0; }
  .bubble.bare .time { position: absolute; right: 6px; bottom: 6px; margin: 0; background: rgba(0,0,0,.45); color: #fff; padding: 1px 6px; border-radius: 10px; }
  .bubble.visual { padding: 4px 4px 6px; }
  .bubble.visual .time { margin-right: 6px; }
  .fwd { font-size: 14px; color: var(--accent-light); margin-bottom: 2px; }
  .out .fwd { color: #b2d7ff; }
  .quote { display: flex; flex-direction: column; width: 100%; text-align: left; border-left: 3px solid var(--accent-light); background: rgba(255,255,255,.06);
    border-radius: 4px; padding: 3px 8px; margin: 2px 0 4px; font-size: 14px; line-height: 1.3; }
  .out .quote { border-left-color: #fff; background: rgba(255,255,255,.1); }
  .q-name { font-weight: 600; color: var(--accent-light); }
  .out .q-name { color: #fff; }
  .q-text { color: var(--text); opacity: .85; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 420px; }
  .reacts { display: flex; flex-wrap: wrap; gap: 4px; margin: 6px 0 2px; }
  .react { background: rgba(82, 136, 193, .25); border-radius: 14px; padding: 2px 8px; font-size: 14px; line-height: 1.4; }
  .react.mine { background: var(--accent); color: #fff; }
  .out .react { background: rgba(255,255,255,.15); }
  .out .react.mine { background: #fff; color: var(--bubble-out); }
  .edited { font-style: italic; }
  .pending { opacity: .7; }
  .failed { width: 18px; height: 18px; border-radius: 50%; background: var(--danger); color: #fff; font-weight: 700; font-size: 12px; line-height: 18px; }
  :global(.line.flash .bubble) { animation: flash 1.2s; }
  @keyframes flash { 30% { filter: brightness(1.6); } }
  .sender { font-weight: 600; font-size: 14px; margin-bottom: 2px; white-space: nowrap; }
  .origin { display: flex; align-items: center; gap: 4px; font-size: 12px; color: var(--text-bubble-muted); margin-bottom: 3px; white-space: nowrap; }
  .time { float: right; margin: 8px 0 -4px 12px; font-size: 12px; color: var(--text-muted); display: inline-flex; align-items: center; gap: 3px; white-space: nowrap; user-select: none; }
  .out .time { color: var(--text-bubble-muted); }
  .check { color: var(--accent-light); display: inline-flex; }
  .big-emoji { display: block; font-size: var(--emoji-size); line-height: 1.15; padding: 2px 0;
    font-family: "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", sans-serif; }
  .bare:has(.big-emoji) .time { position: static; float: none; display: flex; justify-content: flex-end; margin: 0; width: max-content; margin-left: auto; }
  .album { display: grid; grid-template-columns: repeat(2, 1fr); gap: 3px; width: min(360px, 72vw); border-radius: 12px; overflow: hidden; }
  .album.many { grid-template-columns: repeat(3, 1fr); }
  .album-item { position: relative; aspect-ratio: 1; background: rgba(0,0,0,.25); overflow: hidden; }
  .album:not(.two):not(.many) .album-item:first-child:nth-last-child(odd) { grid-column: 1 / -1; aspect-ratio: 2 / 1; }
  .album-item img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .a-thumb { filter: blur(8px); transform: scale(1.1); }
  .a-full { opacity: 0; transition: opacity .25s; }
  :global(.a-full.show) { opacity: 1; }
  .a-play { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); width: 40px; height: 40px; border-radius: 50%;
    background: rgba(0,0,0,.45); color: #fff; display: flex; align-items: center; justify-content: center; }
  .a-spin { width: 18px; height: 18px; border: 2px solid #fff; border-right-color: transparent; border-radius: 50%; animation: aspin .8s linear infinite; }
  @keyframes aspin { to { transform: rotate(360deg) } }
  .album-caption { white-space: pre-wrap; margin-top: 6px; padding: 0 4px; }
</style>
