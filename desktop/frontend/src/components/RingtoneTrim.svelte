<script lang="ts">
  // Pick the piece of a song to ring with: the whole track as a waveform,
  // a window to drag (or its edges), listen, save. Only the piece (≤ 30 s)
  // is kept on the device.
  import { onDestroy } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { MAX_RING_SECONDS, setCustomRingtone, trimToWav } from '../lib/ringtones'
  import { duration } from '../lib/media'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'

  let { file }: { file: File } = $props()
  const back = () => (app.modal = { kind: 'settings' })

  let buf = $state<AudioBuffer | null>(null)
  let error = $state('')
  let peaks = $state<number[]>([])
  let start = $state(0)
  let end = $state(0)
  let saving = $state(false)
  const total = $derived(buf?.duration ?? 0)
  const BARS = 120

  let ac: AudioContext | null = null
  ;(async () => {
    try {
      ac = new AudioContext()
      const b = await ac.decodeAudioData(await file.arrayBuffer())
      const ch = b.getChannelData(0)
      const step = Math.max(1, Math.floor(ch.length / BARS))
      const ps: number[] = []
      for (let i = 0; i < BARS; i++) {
        let m = 0
        for (let j = i * step; j < Math.min(ch.length, (i + 1) * step); j += 16) m = Math.max(m, Math.abs(ch[j]))
        ps.push(m)
      }
      const top = Math.max(0.01, ...ps)
      peaks = ps.map((p) => p / top)
      buf = b
      end = Math.min(b.duration, MAX_RING_SECONDS)
    } catch {
      error = 'Не удалось прочитать файл — выберите mp3, m4a или wav'
    }
  })()

  // ---- dragging the window or its edges ----
  let track: HTMLDivElement | undefined = $state()
  let drag: { what: 'start' | 'end' | 'move'; x0: number; s0: number; e0: number } | null = null
  const toSec = (dx: number) => (track ? (dx / track.clientWidth) * total : 0)
  function down(e: PointerEvent, what: 'start' | 'end' | 'move') {
    e.stopPropagation()
    ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    drag = { what, x0: e.clientX, s0: start, e0: end }
  }
  function move(e: PointerEvent) {
    if (!drag) return
    const d = toSec(e.clientX - drag.x0)
    const len = drag.e0 - drag.s0
    if (drag.what === 'move') {
      start = Math.max(0, Math.min(total - len, drag.s0 + d))
      end = start + len
    } else if (drag.what === 'start') {
      start = Math.max(0, Math.max(drag.e0 - MAX_RING_SECONDS, Math.min(drag.e0 - 1, drag.s0 + d)))
    } else {
      end = Math.min(total, Math.min(drag.s0 + MAX_RING_SECONDS, Math.max(drag.s0 + 1, drag.e0 + d)))
    }
  }
  function up() {
    if (drag) { drag = null; play() }
  }
  // A tap on the waveform moves the window there.
  function jumpTo(e: PointerEvent) {
    if (!track || !total) return
    const r = track.getBoundingClientRect()
    const len = end - start
    const at = ((e.clientX - r.left) / r.width) * total - len / 2
    start = Math.max(0, Math.min(total - len, at))
    end = start + len
    play()
  }

  // ---- listening ----
  let src: AudioBufferSourceNode | null = null
  let playing = $state(false)
  let pos = $state(0)
  let raf = 0
  function stop() {
    try { src?.stop() } catch { /* already */ }
    src = null
    playing = false
    cancelAnimationFrame(raf)
  }
  function play() {
    if (!ac || !buf) return
    stop()
    ac.resume()
    src = ac.createBufferSource()
    src.buffer = buf
    src.connect(ac.destination)
    const t0 = ac.currentTime
    src.start(0, start, end - start)
    playing = true
    const s = src
    s.onended = () => { if (src === s) stop() }
    const tickPos = () => { pos = start + (ac!.currentTime - t0); raf = requestAnimationFrame(tickPos) }
    tickPos()
  }

  async function save() {
    if (!buf) return
    stop()
    saving = true
    try {
      const url = await trimToWav(buf, start, end)
      setCustomRingtone(url, file.name.replace(/\.[^.]+$/, ''))
      app.toast('Мелодия звонка сохранена')
      back()
    } catch (e) {
      app.toast(e instanceof Error ? e.message : String(e), 'error')
    } finally {
      saving = false
    }
  }

  onDestroy(() => { stop(); ac?.close().catch(() => {}) })
  const pct = (t: number) => (total ? (t / total) * 100 : 0)
</script>

<Modal title="Мелодия звонка" onclose={back} width={480}>
  <p class="name">{file.name}</p>
  {#if error}
    <p class="error-text">{error}</p>
  {:else if !buf}
    <p class="muted">Читаем файл…</p>
  {:else}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="track" bind:this={track} onpointerdown={jumpTo} onpointermove={move} onpointerup={up} onpointercancel={up}>
      <div class="bars">
        {#each peaks as p, i}
          {@const t = (i / peaks.length) * total}
          <span class:in={t >= start && t < end} style="height:{8 + p * 92}%"></span>
        {/each}
      </div>
      <div class="win" style="left:{pct(start)}%;width:{pct(end) - pct(start)}%" onpointerdown={(e) => down(e, 'move')}>
        <span class="edge l" onpointerdown={(e) => down(e, 'start')}></span>
        <span class="edge r" onpointerdown={(e) => down(e, 'end')}></span>
      </div>
      {#if playing}<div class="pos" style="left:{pct(pos)}%"></div>{/if}
    </div>
    <div class="times muted">
      <span>{duration(start)}</span>
      <span>отрезок {duration(end - start)} (до {MAX_RING_SECONDS} с)</span>
      <span>{duration(end)}</span>
    </div>
    <p class="muted small">Перетащите рамку или её края; нажмите на волну, чтобы перенести рамку туда.</p>
    <div class="actions">
      <button class="btn flat" onclick={() => (playing ? stop() : play())}>
        <Icon name={playing ? 'pause' : 'play'} size={18} /> {playing ? 'Стоп' : 'Прослушать'}
      </button>
      <button class="btn" onclick={save} disabled={saving}>{saving ? 'Сохраняем…' : 'Сохранить'}</button>
    </div>
  {/if}
</Modal>

<style>
  .name { margin: 0 0 10px; font-weight: 500; word-break: break-word; }
  .track { position: relative; height: 96px; border-radius: 10px; background: var(--tint-1); touch-action: none; user-select: none; cursor: pointer; }
  .bars { position: absolute; inset: 8px 4px; display: flex; align-items: center; gap: 1px; pointer-events: none; }
  .bars span { flex: 1; border-radius: 2px; background: var(--tint-3); }
  .bars span.in { background: var(--accent-light); }
  .win { position: absolute; top: 0; bottom: 0; border: 2px solid var(--accent); border-radius: 10px; background: rgba(82, 136, 193, .14); cursor: grab; }
  .edge { position: absolute; top: 0; bottom: 0; width: 22px; cursor: ew-resize; }
  .edge::after { content: ''; position: absolute; top: 50%; left: 50%; width: 4px; height: 28px; margin: -14px 0 0 -2px; border-radius: 2px; background: var(--accent); }
  .edge.l { left: -12px; }
  .edge.r { right: -12px; }
  .pos { position: absolute; top: 4px; bottom: 4px; width: 2px; background: #fff; pointer-events: none; }
  .times { display: flex; justify-content: space-between; font-size: 12px; margin: 6px 2px 8px; font-variant-numeric: tabular-nums; }
  .small { font-size: 13px; }
  .actions { display: flex; justify-content: space-between; align-items: center; margin-top: 8px; }
  .actions .btn.flat { display: inline-flex; gap: 6px; }
</style>
