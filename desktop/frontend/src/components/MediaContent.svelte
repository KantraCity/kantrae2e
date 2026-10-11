<script lang="ts">
  // Media inside a bubble, like Telegram: inline photos and videos with a
  // blurred preview until loaded, round video messages, voice messages
  // with a waveform, and plain files.
  import type { Message } from '../lib/api'
  import { app } from '../lib/app.svelte'
  import { duration } from '../lib/media'
  import { fileSize } from '../lib/util'
  import Icon from './Icon.svelte'

  let { m }: { m: Message } = $props()
  const f = $derived(m.file!)
  const url = $derived(app.mediaUrl(m))
  const sending = $derived(!!app.sending[m.id])

  // Display box for photos/videos: fit 360x360 keeping the aspect ratio.
  const box = $derived.by(() => {
    const w = f.w || 320, h = f.h || 240
    const s = Math.min(1, 360 / w, 360 / h)
    return { w: Math.max(140, Math.round(w * s)), h: Math.max(90, Math.round(h * s)) }
  })

  let loaded = $state(false)

  // ---- voice ----
  let audio: HTMLAudioElement | undefined = $state()
  let playing = $state(false)
  let pos = $state(0)
  function toggleVoice() {
    if (!audio) return
    if (audio.paused) {
      // One voice message at a time.
      document.querySelectorAll('audio').forEach((a) => a !== audio && a.pause())
      audio.play()
    } else audio.pause()
  }
  function seek(e: MouseEvent) {
    if (!audio || !f.dur) return
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    audio.currentTime = ((e.clientX - r.left) / r.width) * (audio.duration || f.dur)
    if (audio.paused) toggleVoice()
  }
  const bars = $derived(f.wave?.length ? f.wave : Array(40).fill(40))

  // ---- round ----
  let round: HTMLVideoElement | undefined = $state()
  let roundSound = $state(false)
  let roundPos = $state(0)
  function toggleRound() {
    if (!round) return
    if (!roundSound) {
      roundSound = true
      round.muted = false
      round.loop = false
      round.currentTime = 0
      round.play()
    } else if (round.paused) round.play()
    else round.pause()
  }
  function roundEnded() {
    if (!round) return
    roundSound = false
    round.muted = true
    round.loop = true
    round.play()
  }
</script>

{#if f.kind === 'photo'}
  <button class="visual" style="width:{box.w}px;height:{box.h}px" onclick={() => (app.modal = { kind: 'viewer', m })} aria-label="Открыть фото">
    {#if f.thumb && !loaded}<img class="thumb" src={f.thumb} alt="" />{/if}
    {#if url}<img class="full" class:show={loaded} src={url} alt={f.name} onload={() => (loaded = true)} />{/if}
    {#if sending}<span class="progress"><span class="spinner"></span></span>{/if}
  </button>
{:else if f.kind === 'video'}
  <button class="visual" style="width:{box.w}px;height:{box.h}px" onclick={() => (app.modal = { kind: 'viewer', m })} aria-label="Смотреть видео">
    {#if f.thumb}<img class="thumb sharp" src={f.thumb} alt="" />{/if}
    {#if url && !f.thumb}<video class="full show" src={url} preload="metadata" muted playsinline></video>{/if}
    {#if f.dur}<span class="badge">{duration(f.dur)}</span>{/if}
    <span class="play">{#if sending}<span class="spinner"></span>{:else}<Icon name="play" size={26} />{/if}</span>
  </button>
{:else if f.kind === 'round'}
  <button class="round" onclick={toggleRound} aria-label="Видеосообщение">
    {#if f.thumb && !loaded}<img class="thumb sharp" src={f.thumb} alt="" />{/if}
    {#if url}
      <video bind:this={round} src={url} autoplay muted loop playsinline class:show={loaded}
        onloadeddata={() => (loaded = true)} ontimeupdate={() => round && (roundPos = round.currentTime / (round.duration || f.dur || 1))}
        onended={roundEnded}></video>
    {/if}
    {#if roundSound}
      <svg class="ring" viewBox="0 0 100 100"><circle cx="50" cy="50" r="48" pathLength="1" stroke-dasharray="1" stroke-dashoffset={1 - roundPos} /></svg>
    {/if}
    <span class="badge">{duration(roundSound && round ? round.currentTime : f.dur ?? 0)}{#if !roundSound} 🔇{/if}</span>
    {#if sending}<span class="progress"><span class="spinner"></span></span>{/if}
  </button>
{:else if f.kind === 'voice'}
  <div class="voice">
    <button class="vbtn" onclick={toggleVoice} aria-label={playing ? 'Пауза' : 'Слушать'}>
      {#if sending}<span class="spinner"></span>{:else}<Icon name={playing ? 'pause' : 'play'} size={20} />{/if}
    </button>
    <div class="vmain">
      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
      <div class="wave" onclick={seek}>
        {#each bars as b, i}
          <span class="bar" class:done={i / bars.length < pos} style="height:{Math.max(3, (b / 255) * 22)}px"></span>
        {/each}
      </div>
      <span class="vtime">{duration(playing || pos > 0 ? (audio?.currentTime ?? 0) : f.dur ?? 0)}</span>
    </div>
    {#if url}
      <audio bind:this={audio} src={url} preload="none" onplay={() => (playing = true)} onpause={() => (playing = false)}
        onended={() => { playing = false; pos = 0 }} ontimeupdate={() => audio && (pos = audio.currentTime / (audio.duration || f.dur || 1))}></audio>
    {/if}
  </div>
{:else}
  <a class="file" href={url ? url + '?download=1' : undefined} download={f.name} title="Скачать">
    <span class="file-icon">{#if sending}<span class="spinner"></span>{:else}<Icon name="file" size={22} />{/if}</span>
    <span class="file-meta">
      <span class="file-name">{f.name}</span>
      <span class="file-size">{fileSize(f.size)}</span>
    </span>
  </a>
{/if}
{#if f.caption}<div class="caption">{f.caption}</div>{/if}

<style>
  .visual { position: relative; display: block; border-radius: 12px; overflow: hidden; background: rgba(0,0,0,.25); margin: -2px -6px 2px; max-width: calc(100% + 12px); }
  .visual img, .visual video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .thumb { filter: blur(10px); transform: scale(1.1); }
  .thumb.sharp { filter: none; transform: none; }
  .full { opacity: 0; transition: opacity .2s; }
  .full.show { opacity: 1; }
  .badge { position: absolute; left: 8px; top: 8px; background: rgba(0,0,0,.45); color: #fff; font-size: 12px; padding: 1px 6px; border-radius: 10px; }
  .play, .progress { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); width: 48px; height: 48px; border-radius: 50%;
    background: rgba(0,0,0,.45); color: #fff; display: flex; align-items: center; justify-content: center; }
  .round { position: relative; display: block; width: 220px; height: 220px; border-radius: 50%; overflow: hidden; background: rgba(0,0,0,.3); }
  .round img, .round video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .round video { opacity: 0; }
  .round video.show { opacity: 1; }
  .round .badge { top: auto; bottom: 14px; left: 50%; transform: translateX(-50%); }
  .ring { position: absolute; inset: 0; width: 100%; height: 100%; transform: rotate(-90deg); fill: none; stroke: rgba(255,255,255,.85); stroke-width: 3; pointer-events: none; }
  .voice { display: flex; align-items: center; gap: 10px; min-width: 220px; padding: 2px 0; }
  .vbtn { width: 44px; height: 44px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  :global(.out) .vbtn { background: #fff; color: var(--bubble-out); }
  .vmain { display: flex; flex-direction: column; gap: 3px; flex: 1; min-width: 0; }
  .wave { display: flex; align-items: center; gap: 2px; height: 24px; cursor: pointer; }
  .bar { width: 3px; border-radius: 2px; background: rgba(255,255,255,.35); flex-shrink: 0; }
  .bar.done { background: var(--accent-light); }
  :global(.out) .bar.done { background: #fff; }
  .vtime { font-size: 12px; color: var(--text-muted); }
  :global(.out) .vtime { color: var(--text-bubble-muted); }
  .file { display: flex; align-items: center; gap: 12px; text-align: left; padding: 4px 0; color: inherit; text-decoration: none; }
  .file-icon { width: 46px; height: 46px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  :global(.out) .file-icon { background: #fff; color: var(--bubble-out); }
  .file-meta { display: flex; flex-direction: column; min-width: 0; }
  .file-name { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 260px; }
  .file-size { font-size: 13px; color: var(--text-muted); }
  :global(.out) .file-size { color: var(--text-bubble-muted); }
  .caption { white-space: pre-wrap; margin-top: 4px; }
  .spinner { width: 20px; height: 20px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: spin .8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg) } }
</style>
