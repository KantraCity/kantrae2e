<script lang="ts">
  // Media inside a bubble, like Telegram: inline photos, GIFs and videos
  // with a blurred preview until loaded, round video messages, voice
  // messages with a waveform and playback speed, and files.
  import type { Message } from '../lib/api'
  import { app } from '../lib/app.svelte'
  import { duration } from '../lib/media'
  import { fileSize } from '../lib/util'
  import Icon from './Icon.svelte'

  let { m }: { m: Message } = $props()
  const f = $derived(m.file!)
  const url = $derived(app.mediaUrl(m))
  const sending = $derived(!!app.sending[m.id])

  // Display box for photos/videos/GIFs: fit 360x360 keeping the aspect ratio.
  const box = $derived.by(() => {
    const w = f.w || 320, h = f.h || 240
    const s = Math.min(1, 360 / w, 360 / h)
    return { w: Math.max(160, Math.round(w * s)), h: Math.max(100, Math.round(h * s)) }
  })

  let loaded = $state(false)

  // ---- voice ----
  let audio: HTMLAudioElement | undefined = $state()
  let playing = $state(false)
  let pos = $state(0)
  let played = $state(false)
  const SPEEDS = [1, 1.5, 2]
  let speed = $state(1)
  function toggleVoice() {
    if (!audio) return
    if (audio.paused) {
      // One voice/round message at a time.
      document.querySelectorAll('audio, video.round-video').forEach((a) => a !== audio && (a as HTMLMediaElement).pause())
      audio.playbackRate = speed
      audio.play()
      played = true
    } else audio.pause()
  }
  function nextSpeed() {
    speed = SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length]
    if (audio) audio.playbackRate = speed
  }
  function seek(e: MouseEvent) {
    if (!audio || !f.dur) return
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    audio.currentTime = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width)) * (audio.duration || f.dur)
    if (audio.paused) toggleVoice()
  }
  // Waveform resampled to 40 bars.
  const bars = $derived.by(() => {
    const src = f.wave?.length ? f.wave : Array(40).fill(60)
    const n = 40
    return Array.from({ length: n }, (_, i) => src[Math.floor((i * src.length) / n)] ?? 0)
  })

  // ---- round ----
  let round: HTMLVideoElement | undefined = $state()
  let roundSound = $state(false)
  let roundPaused = $state(false)
  let roundPos = $state(0)
  function toggleRound() {
    if (!round) return
    if (!roundSound) {
      document.querySelectorAll('audio').forEach((a) => a.pause())
      roundSound = true
      round.muted = false
      round.loop = false
      round.currentTime = 0
      round.play()
      roundPaused = false
    } else if (round.paused) {
      round.play()
      roundPaused = false
    } else {
      round.pause()
      roundPaused = true
    }
  }
  function roundEnded() {
    if (!round) return
    roundSound = false
    roundPaused = false
    round.muted = true
    round.loop = true
    round.play()
  }

  const ext = $derived((f.name.split('.').pop() ?? '').slice(0, 4).toUpperCase())
</script>

{#if f.kind === 'sticker'}
  <button class="sticker" onclick={() => (app.modal = { kind: 'viewer', m })} aria-label="Стикер">
    {#if url}<img src={url} alt="Стикер" class:show={loaded} onload={() => (loaded = true)} />{/if}
    {#if sending}<span class="center glass"><span class="spinner"></span></span>{/if}
  </button>
{:else if f.kind === 'gif' && f.mime?.startsWith('video/')}
  <button class="visual" style="width:{box.w}px;height:{box.h}px" onclick={() => (app.modal = { kind: 'viewer', m })} aria-label="GIF">
    {#if f.thumb && !loaded}<img class="thumb sharp" src={f.thumb} alt="" />{/if}
    {#if url}<video class="full" class:show={loaded} src={url} autoplay loop muted playsinline onloadeddata={() => (loaded = true)}></video>{/if}
    <span class="badge">GIF</span>
  </button>
{:else if f.kind === 'photo' || f.kind === 'gif'}
  <button class="visual" style="width:{box.w}px;height:{box.h}px" onclick={() => (app.modal = { kind: 'viewer', m })}
    aria-label={f.kind === 'gif' ? 'GIF' : 'Открыть фото'}>
    {#if f.thumb && !loaded}<img class="thumb" src={f.thumb} alt="" />{/if}
    {#if url}<img class="full" class:show={loaded} src={url} alt={f.name} onload={() => (loaded = true)} />{/if}
    {#if f.kind === 'gif'}<span class="badge">GIF</span>{/if}
    {#if sending}<span class="center glass"><span class="spinner"></span></span>{/if}
  </button>
{:else if f.kind === 'video'}
  <button class="visual" style="width:{box.w}px;height:{box.h}px" onclick={() => (app.modal = { kind: 'viewer', m })} aria-label="Смотреть видео">
    {#if f.thumb}<img class="thumb sharp" src={f.thumb} alt="" />{/if}
    {#if url && !f.thumb}<video class="full show" src={url} preload="metadata" muted playsinline></video>{/if}
    {#if f.dur}<span class="badge">{duration(f.dur)}</span>{/if}
    <span class="center glass big">{#if sending}<span class="spinner"></span>{:else}<Icon name="play" size={26} />{/if}</span>
  </button>
{:else if f.kind === 'round'}
  <button class="round" class:playing={roundSound} onclick={toggleRound} aria-label="Видеосообщение">
    {#if f.thumb && !loaded}<img class="thumb sharp" src={f.thumb} alt="" />{/if}
    {#if url}
      <video class="round-video" bind:this={round} src={url} autoplay muted loop playsinline class:show={loaded}
        onloadeddata={() => (loaded = true)} ontimeupdate={() => round && (roundPos = round.currentTime / (round.duration || f.dur || 1))}
        onended={roundEnded}></video>
    {/if}
    <svg class="ring" viewBox="0 0 100 100">
      <circle class="track" cx="50" cy="50" r="48.5" />
      {#if roundSound}<circle class="progress" cx="50" cy="50" r="48.5" pathLength="1" stroke-dasharray="1" stroke-dashoffset={1 - roundPos} />{/if}
    </svg>
    {#if roundPaused}<span class="center glass big"><Icon name="play" size={26} /></span>{/if}
    <span class="pill">
      {duration(roundSound && round ? round.currentTime : f.dur ?? 0)}
      {#if !roundSound}<Icon name="mute" size={13} />{/if}
    </span>
    {#if sending}<span class="center glass"><span class="spinner"></span></span>{/if}
  </button>
{:else if f.kind === 'voice'}
  <div class="voice">
    <button class="vbtn" class:on={playing} onclick={toggleVoice} aria-label={playing ? 'Пауза' : 'Слушать'}>
      {#if sending}<span class="spinner"></span>{:else}<Icon name={playing ? 'pause' : 'play'} size={20} />{/if}
    </button>
    <div class="vmain">
      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
      <div class="wave" onclick={seek}>
        {#each bars as b, i}
          <span class="bar" class:done={(i + 0.5) / bars.length <= pos} style="height:{Math.max(4, (b / 255) * 24)}px"></span>
        {/each}
      </div>
      <div class="vmeta">
        <span class="vtime">{duration(playing || pos > 0 ? (audio?.currentTime ?? 0) : f.dur ?? 0)}</span>
        {#if !played && !m.outgoing}<span class="unplayed"></span>{/if}
        {#if playing || pos > 0}<button class="speed" onclick={nextSpeed} title="Скорость">{speed}×</button>{/if}
      </div>
    </div>
    {#if url}
      <audio bind:this={audio} src={url} preload="none" onplay={() => (playing = true)} onpause={() => (playing = false)}
        onended={() => { playing = false; pos = 0 }} ontimeupdate={() => audio && (pos = audio.currentTime / (audio.duration || f.dur || 1))}></audio>
    {/if}
  </div>
{:else}
  <a class="file" href={url ? url + '?download=1' : undefined} download={f.name} title="Скачать">
    <span class="file-icon">
      {#if sending}<span class="spinner"></span>{:else}<span class="ext">{ext || 'FILE'}</span><span class="dl"><Icon name="download" size={20} /></span>{/if}
    </span>
    <span class="file-meta">
      <span class="file-name">{f.name}</span>
      <span class="file-size">{fileSize(f.size)}</span>
    </span>
  </a>
{/if}
{#if f.caption}<div class="caption">{f.caption}</div>{/if}

<style>
  /* ---- photos / GIFs / videos ---- */
  .visual { position: relative; display: block; border-radius: 12px; overflow: hidden; background: rgba(0,0,0,.25); max-width: 100%; }
  .visual img, .visual video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .visual:hover .full.show { filter: brightness(1.04); }
  .thumb { filter: blur(12px); transform: scale(1.1); }
  .thumb.sharp { filter: none; transform: none; }
  .full { opacity: 0; transition: opacity .25s, filter .2s; }
  .full.show { opacity: 1; }
  .badge { position: absolute; left: 8px; top: 8px; background: rgba(0,0,0,.5); backdrop-filter: blur(6px); -webkit-backdrop-filter: blur(6px);
    color: #fff; font-size: 12px; font-weight: 600; padding: 2px 8px; border-radius: 10px; letter-spacing: .3px; }
  .center { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); width: 44px; height: 44px; border-radius: 50%;
    color: #fff; display: flex; align-items: center; justify-content: center; transition: transform .15s; }
  .center.big { width: 54px; height: 54px; }
  .glass { background: rgba(0,0,0,.42); backdrop-filter: blur(8px); -webkit-backdrop-filter: blur(8px); box-shadow: 0 2px 12px rgba(0,0,0,.25); }
  .visual:hover .center.big { transform: translate(-50%, -50%) scale(1.08); }

  /* ---- stickers: no frame, transparent ---- */
  .sticker { position: relative; display: block; width: 180px; height: 180px; }
  .sticker img { width: 100%; height: 100%; object-fit: contain; opacity: 0; transition: opacity .2s, transform .15s; }
  .sticker img.show { opacity: 1; }
  .sticker:hover img { transform: scale(1.03); }

  /* ---- round video messages ---- */
  .round { position: relative; display: block; width: 240px; height: 240px; border-radius: 50%; overflow: hidden;
    background: #0b121a; box-shadow: 0 4px 18px rgba(0,0,0,.35); transition: transform .2s; }
  .round:hover { transform: scale(1.015); }
  .round img, .round video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; border-radius: 50%; }
  .round video { opacity: 0; transition: opacity .25s; }
  .round video.show { opacity: 1; }
  .ring { position: absolute; inset: 0; width: 100%; height: 100%; transform: rotate(-90deg); fill: none; pointer-events: none; }
  .ring .track { stroke: rgba(255,255,255,.18); stroke-width: 2.5; }
  .ring .progress { stroke: var(--accent-light); stroke-width: 3.5; stroke-linecap: round; transition: stroke-dashoffset .2s linear; }
  .pill { position: absolute; left: 50%; bottom: 14px; transform: translateX(-50%); display: inline-flex; align-items: center; gap: 5px;
    background: rgba(0,0,0,.45); backdrop-filter: blur(6px); -webkit-backdrop-filter: blur(6px); color: #fff; font-size: 12px;
    padding: 2px 9px; border-radius: 10px; font-variant-numeric: tabular-nums; }

  /* ---- voice messages ---- */
  .voice { display: flex; align-items: center; gap: 12px; min-width: 240px; padding: 2px 2px 0; }
  .vbtn { width: 46px; height: 46px; border-radius: 50%; color: #fff; flex-shrink: 0; display: flex; align-items: center; justify-content: center;
    background: linear-gradient(135deg, var(--accent-light), var(--accent)); box-shadow: 0 2px 8px rgba(82,136,193,.35); transition: transform .15s; }
  .vbtn:hover { transform: scale(1.06); }
  .vbtn:active { transform: scale(.95); }
  :global(.out) .vbtn { background: var(--out-accent); color: var(--out-on-accent); box-shadow: 0 2px 8px rgba(0,0,0,.2); }
  .vmain { display: flex; flex-direction: column; gap: 4px; flex: 1; min-width: 0; }
  .wave { display: flex; align-items: center; gap: 2px; height: 26px; cursor: pointer; }
  .bar { width: 3px; border-radius: 3px; background: var(--wave); flex-shrink: 0; transition: background .15s; }
  .bar.done { background: var(--accent-light); }
  :global(.out) .bar { background: var(--out-wave); }
  :global(.out) .bar.done { background: var(--out-accent); }
  .vmeta { display: flex; align-items: center; gap: 6px; height: 16px; }
  .vtime { font-size: 12px; color: var(--text-muted); font-variant-numeric: tabular-nums; }
  :global(.out) .vtime { color: var(--text-bubble-muted); }
  .unplayed { width: 6px; height: 6px; border-radius: 50%; background: var(--accent-light); }
  .speed { font-size: 11px; font-weight: 700; padding: 0 6px; height: 16px; border-radius: 8px; background: var(--tint-2); color: var(--text); }

  /* ---- files ---- */
  .file { display: flex; align-items: center; gap: 12px; text-align: left; padding: 4px 2px; color: inherit; text-decoration: none; }
  .file-icon { position: relative; width: 48px; height: 48px; border-radius: 12px; color: #fff; flex-shrink: 0; display: flex; align-items: center; justify-content: center;
    background: linear-gradient(135deg, var(--accent-light), var(--accent)); overflow: hidden; }
  :global(.out) .file-icon { background: var(--out-accent); color: var(--out-on-accent); }
  .ext { font-size: 12px; font-weight: 700; letter-spacing: .3px; transition: opacity .15s, transform .15s; }
  .dl { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; opacity: 0; transform: translateY(6px); transition: opacity .15s, transform .15s; }
  .file:hover .ext { opacity: 0; transform: translateY(-6px); }
  .file:hover .dl { opacity: 1; transform: none; }
  .file-meta { display: flex; flex-direction: column; min-width: 0; gap: 2px; }
  .file-name { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 260px; }
  .file-size { font-size: 13px; color: var(--text-muted); }
  :global(.out) .file-size { color: var(--text-bubble-muted); }

  .caption { white-space: pre-wrap; margin-top: 6px; padding: 0 4px; }
  .spinner { width: 20px; height: 20px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: spin .8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg) } }
  @media (max-width: 720px) {
    .round { width: 220px; height: 220px; }
  }
</style>
