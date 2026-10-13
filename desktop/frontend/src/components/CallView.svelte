<script lang="ts">
  // Calls: the incoming call screen, the call itself (participant tiles,
  // controls) and, when minimised, a bar on top of the app.
  import { fade, fly, scale } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import { app } from '../lib/app.svelte'
  import { calls, type Peer } from '../lib/call.svelte'
  import { plural } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  const chat = $derived(calls.call ? app.chats.find((c) => c.id === calls.call!.chatId) : null)
  const ringChat = $derived(calls.ringing ? app.chats.find((c) => c.id === calls.ringing!.chatId) : null)
  const peers = $derived(Object.values(calls.peers))
  const me = $derived(app.state?.account)

  // Timer since the call connected.
  let now = $state(Date.now())
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(t)
  })
  const elapsed = $derived.by(() => {
    const c = calls.call
    if (!c || !c.connectedAt) return ''
    const s = Math.max(0, Math.floor((now - c.connectedAt) / 1000))
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60
    return (h ? h + ':' + String(m).padStart(2, '0') : String(m)) + ':' + String(sec).padStart(2, '0')
  })
  const status = $derived.by(() => {
    if (!calls.call) return ''
    if (!peers.length) return calls.call.outgoing ? 'Вызов…' : 'Соединение…'
    if (peers.every((p) => p.state !== 'connected')) return 'Соединение…'
    return elapsed
  })

  // <video> elements get their streams through an action.
  function stream(node: HTMLMediaElement, s: MediaStream | null) {
    node.srcObject = s
    return { update(n: MediaStream | null) { if (node.srcObject !== n) node.srcObject = n } }
  }
  const hasVideo = (p: Peer) => p.cam && !!p.stream?.getVideoTracks().some((t) => t.readyState === 'live')
  const localVideo = $derived(calls.cam || calls.screen ? calls.localVideo : null)

  // Grid: 1 peer -> big; more -> columns.
  const cols = $derived(peers.length <= 1 ? 1 : peers.length <= 4 ? 2 : 3)
</script>

<!-- Remote audio (always, also while minimised). -->
{#each peers as p (p.key)}
  <audio autoplay use:stream={p.stream}></audio>
{/each}

{#if calls.ringing && !calls.call}
  {@const r = calls.ringing}
  <div class="incoming" transition:fade={{ duration: 200 }}>
    <div class="inc-card" in:scale={{ start: 0.9, duration: 260, easing: cubicOut }}>
      <div class="pulse">
        <Avatar name={ringChat?.kind === 'group' ? ringChat.name : r.name} id={ringChat?.kind === 'group' ? r.chatId : r.from}
          user={ringChat?.kind === 'group' ? undefined : r.from} photo={ringChat?.kind === 'group' ? undefined : ringChat?.peerAvatar} size={112} />
      </div>
      <div class="inc-name">{ringChat?.kind === 'group' ? ringChat.name : r.name}</div>
      <div class="inc-what">
        {r.video ? 'Входящий видеозвонок' : 'Входящий звонок'}{ringChat?.kind === 'group' ? ` · ${r.name}` : ''}
      </div>
      <div class="inc-actions">
        <button class="round-btn red" aria-label="Отклонить" onclick={() => calls.decline()}><Icon name="hangup" size={30} /></button>
        <button class="round-btn green" aria-label="Ответить" onclick={() => calls.accept()}><Icon name={r.video ? 'video' : 'phone'} size={28} /></button>
      </div>
      <div class="inc-labels"><span>Отклонить</span><span>Ответить</span></div>
    </div>
  </div>
{/if}

{#if calls.call && calls.minimized}
  <button class="bar" transition:fly={{ y: -40, duration: 200 }} onclick={() => (calls.minimized = false)}>
    <span class="dot"></span>
    <span class="bar-name">{chat?.name ?? 'Звонок'}</span>
    <span class="bar-time">{status}</span>
    <span class="bar-hint">Вернуться к звонку</span>
  </button>
{/if}

{#if calls.call && !calls.minimized}
  <div class="call" transition:fade={{ duration: 180 }}>
    <header>
      <button class="ghost" aria-label="Свернуть" title="Свернуть" onclick={() => (calls.minimized = true)}><Icon name="minimize" size={20} /></button>
      <div class="title">
        <div class="name">{chat?.name ?? 'Звонок'}</div>
        <div class="status">{status}{#if peers.length > 1} · {peers.length + 1} {plural(peers.length + 1, 'участник', 'участника', 'участников')}{/if}</div>
      </div>
      <span class="e2e" title="Звонок зашифрован между устройствами"><Icon name="lock" size={14} /> E2E</span>
    </header>

    <div class="stage" class:solo={peers.length === 0} style="--cols:{cols}">
      {#if peers.length === 0}
        <div class="waiting">
          <div class="pulse">
            <Avatar name={chat?.name ?? ''} id={chat?.peerId || chat?.id || ''} user={chat?.peerId} photo={chat?.peerAvatar} size={128} />
          </div>
          <div class="w-name">{chat?.name}</div>
          <div class="w-status">{status}</div>
        </div>
      {:else}
        {#each peers as p (p.key)}
          <div class="tile" class:speaking={p.speaking && p.mic} in:scale={{ start: 0.92, duration: 220 }}>
            {#if hasVideo(p)}
              <video autoplay playsinline muted use:stream={p.stream}></video>
            {:else}
              <div class="tile-avatar"><Avatar name={p.name} id={p.userId} user={p.userId} size={peers.length > 2 ? 72 : 112} /></div>
            {/if}
            <div class="tile-label">
              {#if !p.mic}<Icon name="micoff" size={14} />{/if}
              {p.name}
              {#if p.state === 'connecting'}<span class="muted"> · соединение…</span>{:else if p.state === 'failed'}<span class="bad"> · нет связи</span>{:else if p.route === 'relay'}<span class="muted" title="Прямое соединение не удалось, звонок идёт через сервер (TURN) — задержка чуть выше"> · через сервер</span>{/if}
            </div>
          </div>
        {/each}
      {/if}
      {#if localVideo}
        <div class="self" class:speaking={calls.selfSpeaking} transition:scale={{ start: 0.8, duration: 200 }}>
          <video autoplay playsinline muted class:mirror={!calls.screen} use:stream={localVideo}></video>
        </div>
      {/if}
    </div>

    <footer>
      <div class="ctl">
        <button class="round-btn" class:off={!calls.mic} aria-label={calls.mic ? 'Выключить микрофон' : 'Включить микрофон'} onclick={() => calls.toggleMic()}>
          <Icon name={calls.mic ? 'mic' : 'micoff'} size={24} />
        </button>
        <span>{calls.mic ? 'Микрофон' : 'Без звука'}</span>
      </div>
      <div class="ctl">
        <button class="round-btn" class:on={calls.cam} aria-label="Камера" onclick={() => calls.toggleCam()}>
          <Icon name={calls.cam ? 'video' : 'videooff'} size={24} />
        </button>
        <span>Камера</span>
      </div>
      {#if calls.canShare}
        <div class="ctl">
          <button class="round-btn" class:on={calls.screen} aria-label="Демонстрация экрана" onclick={() => calls.toggleScreen()}>
            <Icon name="screen" size={24} />
          </button>
          <span>Экран</span>
        </div>
      {/if}
      <div class="ctl">
        <button class="round-btn red" aria-label="Завершить" onclick={() => calls.hangup(calls.call?.outgoing && !peers.length)}>
          <Icon name="hangup" size={28} />
        </button>
        <span>Завершить</span>
      </div>
    </footer>
  </div>
{/if}

<style>
  /* ---- incoming ---- */
  .incoming { position: fixed; inset: var(--titlebar-h, 0px) 0 0; z-index: 250; display: flex; align-items: center; justify-content: center;
    background: radial-gradient(circle at 50% 30%, rgba(43, 82, 120, .95), rgba(14, 22, 33, .97) 70%); }
  .inc-card { display: flex; flex-direction: column; align-items: center; gap: 10px; color: #fff; text-align: center; padding: 24px; }
  .inc-name { font-size: 26px; font-weight: 600; margin-top: 14px; }
  .inc-what { color: rgba(255,255,255,.7); font-size: 15px; }
  .inc-actions { display: flex; gap: 72px; margin-top: 56px; }
  .inc-labels { display: flex; gap: 72px; font-size: 13px; color: rgba(255,255,255,.7); }
  .inc-labels span { width: 68px; text-align: center; }
  .inc-actions .green { animation: wiggle 1.2s ease-in-out infinite; }
  @keyframes wiggle { 0%, 60%, 100% { transform: rotate(0) } 70% { transform: rotate(-12deg) } 80% { transform: rotate(12deg) } 90% { transform: rotate(-6deg) } }

  .pulse { position: relative; border-radius: 50%; }
  .pulse::before, .pulse::after { content: ''; position: absolute; inset: 0; border-radius: 50%; border: 2px solid rgba(106, 179, 243, .55);
    animation: ripple 2.4s ease-out infinite; }
  .pulse::after { animation-delay: 1.2s; }
  @keyframes ripple { from { transform: scale(1); opacity: 1 } to { transform: scale(1.7); opacity: 0 } }

  /* ---- buttons ---- */
  .round-btn { width: 64px; height: 64px; border-radius: 50%; display: flex; align-items: center; justify-content: center; color: #fff;
    background: rgba(255,255,255,.14); backdrop-filter: blur(10px); -webkit-backdrop-filter: blur(10px);
    transition: transform .12s, background .15s, filter .15s; }
  .round-btn:hover { filter: brightness(1.15); }
  .round-btn:active { transform: scale(.92); }
  .round-btn.on { background: #fff; color: #17212b; }
  .round-btn.off { background: rgba(255,255,255,.9); color: #17212b; }
  .round-btn.red { background: #e53935; }
  .round-btn.green { background: #3fbf5f; }

  /* ---- the call ---- */
  .call { position: fixed; inset: var(--titlebar-h, 0px) 0 0; z-index: 240; display: flex; flex-direction: column; color: #fff;
    background: radial-gradient(circle at 50% 0%, #22384f, #0b121a 75%); }
  .call header { display: flex; align-items: center; gap: 12px; padding: 14px 16px; }
  .ghost { width: 40px; height: 40px; border-radius: 50%; display: flex; align-items: center; justify-content: center; color: rgba(255,255,255,.8); }
  .ghost:hover { background: rgba(255,255,255,.1); }
  .title { flex: 1; min-width: 0; text-align: center; }
  .title .name { font-weight: 600; font-size: 16px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .title .status { font-size: 13px; color: rgba(255,255,255,.65); font-variant-numeric: tabular-nums; }
  .e2e { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; color: rgba(255,255,255,.6); padding: 4px 8px;
    border-radius: 10px; background: rgba(255,255,255,.08); }

  .stage { position: relative; flex: 1; min-height: 0; display: grid; grid-template-columns: repeat(var(--cols), 1fr); gap: 10px; padding: 6px 16px; }
  .stage.solo { display: flex; align-items: center; justify-content: center; }
  .waiting { display: flex; flex-direction: column; align-items: center; gap: 10px; }
  .w-name { font-size: 24px; font-weight: 600; margin-top: 16px; }
  .w-status { color: rgba(255,255,255,.65); }
  .tile { position: relative; min-height: 0; border-radius: 18px; overflow: hidden; background: rgba(255,255,255,.05);
    display: flex; align-items: center; justify-content: center; box-shadow: inset 0 0 0 2px transparent; transition: box-shadow .15s; }
  .tile.speaking { box-shadow: inset 0 0 0 3px #4dcd5e; }
  .tile video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .tile-avatar { border-radius: 50%; }
  .tile-label { position: absolute; left: 10px; bottom: 10px; display: inline-flex; align-items: center; gap: 5px; font-size: 13px;
    padding: 3px 10px; border-radius: 12px; background: rgba(0,0,0,.45); backdrop-filter: blur(6px); -webkit-backdrop-filter: blur(6px); }
  .tile-label .muted { color: rgba(255,255,255,.6); }
  .tile-label .bad { color: #ff8a80; }
  .self { position: absolute; right: 24px; bottom: 16px; width: min(220px, 30vw); aspect-ratio: 4 / 3; border-radius: 14px; overflow: hidden;
    box-shadow: 0 6px 24px rgba(0,0,0,.5); background: #000; outline: 2px solid transparent; transition: outline-color .15s; z-index: 2; }
  .self.speaking { outline-color: #4dcd5e; }
  .self video { width: 100%; height: 100%; object-fit: cover; }
  .mirror { transform: scaleX(-1); }

  .call footer { display: flex; justify-content: center; gap: 28px; padding: 18px 16px calc(24px + env(safe-area-inset-bottom)); }
  .ctl { display: flex; flex-direction: column; align-items: center; gap: 6px; font-size: 12px; color: rgba(255,255,255,.7); }

  /* ---- minimised bar ---- */
  .bar { position: fixed; top: var(--titlebar-h, 0px); left: 50%; transform: translateX(-50%); z-index: 230; margin-top: 6px; display: flex; align-items: center; gap: 10px;
    padding: 7px 16px; border-radius: 18px; color: #fff; font-size: 13px; background: linear-gradient(90deg, #3fbf5f, #2fa84f);
    box-shadow: 0 4px 16px rgba(0,0,0,.35); }
  .bar .dot { width: 8px; height: 8px; border-radius: 50%; background: #fff; animation: blink 1.4s infinite; }
  .bar-name { font-weight: 600; }
  .bar-time { font-variant-numeric: tabular-nums; opacity: .9; }
  .bar-hint { opacity: .8; }
  @keyframes blink { 50% { opacity: .3 } }

  @media (max-width: 720px) {
    .stage { grid-template-columns: repeat(min(var(--cols), 2), 1fr); padding: 6px 8px; }
    .self { right: 12px; bottom: 8px; width: 34vw; aspect-ratio: 3 / 4; }
    .call footer { gap: 18px; }
    .round-btn { width: 58px; height: 58px; }
    .bar-hint { display: none; }
  }
</style>
