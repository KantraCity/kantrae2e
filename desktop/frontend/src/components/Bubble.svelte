<script lang="ts">
  import type { Message } from '../lib/api'
  import { app } from '../lib/app.svelte'
  import { timeHM, nameColor } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import MediaContent from './MediaContent.svelte'
  import RichText from './RichText.svelte'
  import Poll from './Poll.svelte'
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

  const jumpTo = (id: string) => app.jumpTo(m.chatId, id)

  // Selection mode: a click selects instead of opening anything.
  const selecting = $derived(app.selected.length > 0)
  const isSelected = $derived(app.selected.includes(m.id))
  function lineClick(e: MouseEvent) {
    if (!selecting || m.id.startsWith('local-')) return
    e.preventDefault()
    e.stopPropagation()
    app.toggleSelect(m.id)
  }

</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="line" class:out={m.outgoing} class:last class:selecting class:selected={isSelected} id={'msg-' + m.id} onclickcapture={lineClick}>
  {#if selecting}<span class="sel-check" class:on={isSelected}>{#if isSelected}<Icon name="check" size={14} />{/if}</span>{/if}
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
      {#if m.kind === 'poll' && m.poll}
        <Poll {m} />
      {:else}
        <span class="text selectable" class:big-emoji={bigEmoji > 0} style={bigEmoji ? `--emoji-size:${[0, 64, 50, 40][bigEmoji]}px` : undefined}>{#if bigEmoji}{m.text}{:else}<RichText text={m.text} />{/if}</span>
      {/if}
      {#if m.link}
        <a class="lp" href={m.link.url} target="_blank" rel="noopener noreferrer">
          <span class="lp-site">{m.link.site}</span>
          {#if m.link.title}<span class="lp-title">{m.link.title}</span>{/if}
          {#if m.link.desc}<span class="lp-desc">{m.link.desc}</span>{/if}
          {#if m.link.image}<img class="lp-img" src={m.link.image} alt="" />{/if}
        </a>
      {/if}
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
      {#if m.pinned}<span class="pin-mark" title="Закреплено"><Icon name="pin" size={12} /></span>{/if}
      {timeHM(m.sentAt)}
      {#if app.failed[m.id]}
        <button class="failed" title="Не отправлено — нажмите, чтобы повторить" onclick={() => app.failed[m.id]?.()}>!</button>
      {:else if m.pending === 2}
        <button class="failed" title="Не отправлено — нажмите, чтобы повторить" onclick={(e) => { e.stopPropagation(); app.retryPending(m) }}>!</button>
      {:else if m.pending === 1}
        <span class="check pending" title="Ждёт сети — уйдёт, когда появится соединение"><Icon name="clock" size={14} /></span>
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
  .out .bubble { background: var(--bubble-out); color: var(--out-text); }
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
  .out .fwd { color: var(--out-fwd); }
  .quote { display: flex; flex-direction: column; width: 100%; text-align: left; border-left: 3px solid var(--accent-light); background: var(--tint-1);
    border-radius: 4px; padding: 3px 8px; margin: 2px 0 4px; font-size: 14px; line-height: 1.3; }
  .out .quote { border-left-color: var(--out-accent); background: var(--tint-2); }
  .q-name { font-weight: 600; color: var(--accent-light); }
  .out .q-name { color: var(--out-accent); }
  .q-text { color: var(--text); opacity: .85; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 420px; }
  .reacts { display: flex; flex-wrap: wrap; gap: 4px; margin: 6px 0 2px; }
  .react { background: rgba(82, 136, 193, .25); border-radius: 14px; padding: 2px 8px; font-size: 14px; line-height: 1.4; }
  .react.mine { background: var(--accent); color: #fff; }
  .out .react { background: var(--tint-2); }
  .out .react.mine { background: var(--out-accent); color: var(--out-on-accent); }
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
  .lp { display: flex; flex-direction: column; gap: 2px; margin: 6px 0 2px; padding: 4px 0 4px 10px; border-left: 3px solid var(--accent-light);
    border-radius: 3px; text-decoration: none; color: inherit; max-width: 360px; }
  :global(.out) .lp { border-left-color: var(--out-accent); }
  .lp-site { font-weight: 600; font-size: 13px; color: var(--accent-light); }
  :global(.out) .lp-site { color: var(--out-accent); }
  .lp-title { font-weight: 600; font-size: 14px; }
  .lp-desc { font-size: 13px; opacity: .85; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
  .lp-img { margin-top: 6px; border-radius: 8px; max-width: 100%; max-height: 200px; object-fit: cover; }
  .pin-mark { display: inline-flex; opacity: .8; }
  .line.selecting { cursor: pointer; padding-left: 34px; position: relative; border-radius: 10px; transition: background .15s; }
  .line.selected { background: rgba(82, 136, 193, .18); }
  .sel-check { position: absolute; left: 6px; top: 50%; transform: translateY(-50%); width: 22px; height: 22px; border-radius: 50%;
    border: 2px solid var(--text-muted); display: flex; align-items: center; justify-content: center; color: #fff; transition: background .15s; }
  .sel-check.on { background: var(--accent); border-color: var(--accent); }
</style>
