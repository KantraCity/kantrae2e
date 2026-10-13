// Calls, 1:1 and group: a WebRTC mesh (every participant device connected
// to every other). Signaling goes through Messenger.SendSignal — an MLS
// message in the chat — so the server can neither read nor forge it; media
// is DTLS-SRTP between the devices (relayed by the server's TURN only when
// no direct path exists).
//
// Signals (JSON, "t" = type):
//   invite {call, video, mic, cam}  the caller rings the chat (and is in it)
//   join   {call, mic, cam, offer?: {to, sdp}}  a device entered the call;
//                              answering a 1:1 call it brings its offer to
//                              the caller along (prepared while ringing)
//   hello  {call, to}          "please send me an offer" (see below)
//   offer  {call, to, sdp, restart?} / answer {call, to, sdp}
//   ice    {call, to, cands}   candidates found after the SDP went out
//   restart {call, to}         "please restart ICE" (my network changed)
//   leave  {call}              a device left; end {call}: the caller cancelled
//   decline {call}             declined (1:1: the caller stops)
//   media  {call, mic, cam}    mute / camera state for the others' tiles
//   alive  {call}              group calls: "still going" (join banner)
// "to" is "<userId>/<deviceId>". Every pair is connected by an offer from the
// device with the smaller id, so two devices never offer to each other —
// except the answering device of a 1:1 call, whose offer travels with its
// join (the caller then never offers to it).
//
// Speed: candidates are trickled (the SDP leaves after ~0.3 s instead of a
// complete gathering), the answering device prepares its connection while
// ringing, and a broken path is repaired by an ICE restart (keeping the
// DTLS session) — at once when the network changes.
import { logClient } from './diag'
import { Messenger, onEvent, type CallSignal } from './api'
import { app } from './app.svelte'

/** Opus with in-band forward error correction: a lost packet is rebuilt
 * from the next one instead of a click or a gap (WebRTC usually offers it;
 * this makes sure). */
function opusFec(d: RTCSessionDescriptionInit): RTCSessionDescriptionInit {
  const sdp = d.sdp
  if (!sdp) return d
  const pt = /a=rtpmap:(\d+) opus\/48000/i.exec(sdp)?.[1]
  if (!pt) return d
  const fmtp = new RegExp(`a=fmtp:${pt} ([^\r\n]*)`)
  const m = fmtp.exec(sdp)
  let out = sdp
  if (!m) out = sdp.replace(new RegExp(`(a=rtpmap:${pt} opus/48000[^\r\n]*\r?\n)`, 'i'), `$1a=fmtp:${pt} minptime=10;useinbandfec=1\r\n`)
  else if (!/useinbandfec=1/.test(m[1])) out = sdp.replace(fmtp, `a=fmtp:${pt} ${m[1]};useinbandfec=1`)
  return { type: d.type, sdp: out }
}

export type Peer = {
  key: string
  userId: string
  name: string
  stream: MediaStream | null
  state: 'connecting' | 'connected' | 'failed'
  mic: boolean
  cam: boolean
  speaking: boolean
  /** How the media flows once connected: straight between the devices, or
   * through the server's TURN relay (a bit more delay). */
  route?: 'direct' | 'relay'
}

type Ringing = { call: string; chatId: string; from: string; fromKey: string; name: string; video: boolean; at: number }

const RING_MS = 45_000
/** The SDP waits this long for the first candidates; the rest trickle. */
const FIRST_CANDIDATES_MS = 300
/** Candidates are sent in batches (each signal is one MLS message). */
const TRICKLE_BATCH_MS = 120
/** TURN credentials last 12 h on the server: refresh well before. */
const ICE_TTL_MS = 60 * 60_000
const ALIVE_MS = 30_000

/** Timing log for tests (window.__callDebug = true). */
function dbg(...a: unknown[]) {
  if ((window as unknown as { __callDebug?: boolean }).__callDebug) console.log('[call]', Date.now(), ...a)
}

function rid(): string {
  return Math.random().toString(36).slice(2) + Date.now().toString(36)
}

class CallStore {
  // ---- state the UI renders ----
  /** The call this device is in (or calling out). */
  call = $state<{ id: string; chatId: string; video: boolean; outgoing: boolean; startedAt: number; connectedAt: number } | null>(null)
  /** An incoming call ringing here. */
  ringing = $state<Ringing | null>(null)
  peers = $state<Record<string, Peer>>({})
  local = $state<MediaStream | null>(null)
  mic = $state(true)
  cam = $state(false)
  screen = $state(false)
  minimized = $state(false)
  /** Group calls going on without us: chat id -> call id (join banner). */
  active = $state<Record<string, { call: string; video: boolean; at: number }>>({})

  private pcs = new Map<string, RTCPeerConnection>()
  private ice: RTCIceServer[] | null = null
  private iceAt = 0
  /** A connection prepared while an incoming call rings (1:1). */
  private warm: { call: string; key: string; pc: RTCPeerConnection } | null = null
  private outbox = new Map<string, { cands: RTCIceCandidateInit[]; timer: ReturnType<typeof setTimeout> }>()
  private iceRestarts = new Map<string, number>()
  private ringTimer: ReturnType<typeof setTimeout> | undefined
  private tone: { stop: () => void } | null = null
  private screenTrack: MediaStreamTrack | null = null
  private camTrack: MediaStreamTrack | null = null
  /** Front or back camera; whether the device has another one. */
  facing = $state<'user' | 'environment'>('user')
  canFlip = $state(false)
  private meters = new Map<string, () => void>()
  private aliveTimer: ReturnType<typeof setInterval> | undefined
  /** Candidates that arrived before the remote description was set. */
  private pendingIce = new Map<string, RTCIceCandidateInit[]>()
  /** Connections whose SDP is out: later candidates are trickled. */
  private trickle = new WeakSet<RTCPeerConnection>()
  private restarts = new Map<string, number>()

  constructor() {
    // Group calls nobody announced for a while are over.
    setInterval(() => {
      const now = Date.now()
      for (const [k, v] of Object.entries(this.active)) if (now - v.at > ALIVE_MS * 2.5) delete this.active[k]
    }, 10_000)
    // Wi-Fi <-> mobile: repair the media paths at once instead of waiting
    // for the connections to notice.
    const changed = () => this.networkChanged()
    window.addEventListener('online', changed)
    ;(navigator as Navigator & { connection?: EventTarget }).connection?.addEventListener('change', changed)
  }

  private async ensureIce(): Promise<RTCIceServer[]> {
    if (!this.ice || Date.now() - this.iceAt > ICE_TTL_MS) {
      const s = await Messenger.IceServers().then((l) => l ?? []).catch(() => null)
      if (s) {
        this.ice = s.map((x) => ({ urls: x.urls ?? [], username: x.username || undefined, credential: x.credential || undefined }))
        this.iceAt = Date.now()
      }
    }
    return this.ice ?? []
  }

  get myKey(): string {
    const a = app.state?.account
    return a ? `${a.userId}/${a.deviceId}` : ''
  }

  // ---- signaling ----
  private send(chatId: string, body: Record<string, unknown>) {
    dbg('send', body.t)
    return Messenger.SendSignal(chatId, JSON.stringify({ v: 1, ...body })).then(() => dbg('sent', body.t)).catch((e) => console.warn('signal', e))
  }

  /** A signal from the Go side ("signal" UI event, or PendingCall). */
  async onSignal(s: CallSignal) {
    let b: any
    try { b = JSON.parse(s.body) } catch { return }
    const from = `${s.userId}/${s.deviceId}`
    const age = Date.now() / 1000 - s.at
    const mine = s.userId === app.state?.account?.userId
    const inThis = this.call && this.call.id === b.call
    dbg('recv', b.t, 'age', Date.now() - s.at * 1000)
    switch (b.t) {
      case 'invite':
        if (age > RING_MS / 1000) return
        if (inThis) {
          // The caller's other device, or a re-sent invite: the caller is
          // in the call like after a join.
          await this.onJoin(s.chatId, from, s.userId, s.name, b)
          return
        }
        if (this.call) {
          if (!inThis && !mine) this.send(s.chatId, { t: 'busy', call: b.call, to: from })
          return
        }
        if (mine) {
          // Another device of ours is calling: show the join banner only.
          this.active[s.chatId] = { call: b.call, video: !!b.video, at: Date.now() }
          return
        }
        this.active[s.chatId] = { call: b.call, video: !!b.video, at: Date.now() }
        this.startRinging({ call: b.call, chatId: s.chatId, from: s.userId, fromKey: from, name: s.name, video: !!b.video, at: Date.now() })
        return
      case 'join':
        this.active[s.chatId] = { call: b.call, video: this.active[s.chatId]?.video ?? false, at: Date.now() }
        if (this.ringing?.call === b.call && mine) this.stopRinging() // answered on another device of ours
        if (!inThis || from === this.myKey) return
        await this.onJoin(s.chatId, from, s.userId, s.name, b)
        return
      case 'restart':
        // Only the device with the smaller id restarts (see restartIce).
        if (inThis && b.to === this.myKey && this.myKey < from && this.pcs.has(from)) this.restartIce(from)
        return
      case 'alive':
        if (!inThis) this.active[s.chatId] = { call: b.call, video: this.active[s.chatId]?.video ?? false, at: Date.now() }
        return
      case 'hello':
        if (inThis && b.to === this.myKey) {
          this.addPeer(from, s.userId, s.name)
          this.offer(from)
        }
        return
      case 'offer':
        if (inThis && b.to === this.myKey) await this.answer(from, s.userId, s.name, b.sdp, !!b.restart)
        return
      case 'answer':
        if (inThis && b.to === this.myKey) {
          const pc = this.pcs.get(from)
          if (pc && pc.signalingState === 'have-local-offer') {
            await pc.setRemoteDescription({ type: 'answer', sdp: b.sdp }).catch((e) => console.warn(e))
            this.flushIce(from, pc)
          }
        }
        return
      case 'ice':
        if (inThis && b.to === this.myKey) {
          const list: RTCIceCandidateInit[] = b.cands ?? (b.cand ? [b.cand] : [])
          const pc = this.pcs.get(from)
          for (const c of list) {
            if (pc && pc.remoteDescription) pc.addIceCandidate(c).catch(() => {})
            else this.pendingIce.set(from, [...(this.pendingIce.get(from) ?? []), c])
          }
        }
        return
      case 'media':
        if (inThis && this.peers[from]) {
          this.peers[from].mic = !!b.mic
          this.peers[from].cam = !!b.cam
        }
        return
      case 'leave':
        if (inThis) this.dropPeer(from)
        if (this.ringing && this.ringing.call === b.call && this.ringing.fromKey === from) this.stopRinging('Звонок отменён')
        {
          const c = this.call as { id: string; chatId: string; outgoing: boolean; connectedAt: number } | null
          if (c && c.id === b.call && c.outgoing && !c.connectedAt && app.chats.find((x) => x.id === c.chatId)?.kind === 'direct') this.hangup(false)
        }
        this.expireActive()
        return
      case 'end':
        if (this.ringing?.call === b.call) this.stopRinging('Звонок отменён')
        delete this.active[s.chatId]
        return
      case 'decline':
      case 'busy':
        if (this.ringing?.call === b.call && mine) this.stopRinging() // declined on another device of ours
        if (inThis && this.call!.outgoing && !mine && (b.t === 'decline' || b.to === this.myKey)) {
          const direct = app.chats.find((c) => c.id === this.call!.chatId)?.kind === 'direct'
          if (direct && !Object.keys(this.peers).length) {
            app.toast(b.t === 'busy' ? `${s.name} разговаривает` : `${s.name} отклонил(а) звонок`, 'info')
            this.hangup(true)
          }
        }
        return
    }
  }

  /** Another device entered the call (join, or the caller's invite). */
  private async onJoin(chatId: string, from: string, userId: string, name: string, b: any) {
    if (!this.call) return
    if (this.call.outgoing && !this.call.connectedAt) this.call.connectedAt = Date.now()
    this.addPeer(from, userId, name)
    if (typeof b.mic === 'boolean') {
      this.peers[from].mic = b.mic
      this.peers[from].cam = !!b.cam
    }
    if (b.offer?.to === this.myKey && b.offer.sdp) {
      // Prepared while our call rang there: answer right away.
      await this.answer(from, userId, name, b.offer.sdp, false)
      return
    }
    if (this.myKey < from) this.offer(from)
    else this.send(chatId, { t: 'hello', call: b.call, to: from })
    this.sendMedia()
  }

  // ---- ringing ----
  private startRinging(r: Ringing) {
    this.ringing = r
    this.tone?.stop()
    this.tone = ringtone('incoming')
    clearTimeout(this.ringTimer)
    this.ringTimer = setTimeout(() => this.stopRinging('Пропущенный звонок'), RING_MS)
    navigator.vibrate?.([400, 300, 400])
    this.prepare(r)
  }

  /** While a 1:1 call rings: ICE servers, the connection to the caller and
   * its offer with gathered candidates are ready before "answer" (the
   * microphone is not touched until then). */
  private async prepare(r: Ringing) {
    if (app.chats.find((c) => c.id === r.chatId)?.kind !== 'direct') return
    await this.ensureIce()
    if (this.ringing?.call !== r.call) return
    this.dropWarm()
    const pc = new RTCPeerConnection({ iceServers: this.ice ?? [] })
    pc.addTransceiver('audio', { direction: 'sendrecv' })
    pc.addTransceiver('video', { direction: 'sendrecv' })
    this.warm = { call: r.call, key: r.fromKey, pc }
    await pc.setLocalDescription(opusFec(await pc.createOffer()))
  }

  private dropWarm() {
    this.warm?.pc.close()
    this.warm = null
  }

  private stopRinging(note?: string) {
    if (note && this.ringing) app.toast(`${note}: ${this.ringing.name}`, 'info')
    this.dropWarm() // accept() takes it before
    this.ringing = null
    this.tone?.stop()
    this.tone = null
    clearTimeout(this.ringTimer)
  }

  async accept() {
    const r = this.ringing
    if (!r) return
    dbg('accept')
    const warm = this.warm?.call === r.call ? this.warm : null
    this.warm = null
    this.stopRinging()
    if (!(await this.enter(r.chatId, r.call, r.video, false, !warm))) {
      warm?.pc.close()
      return
    }
    dbg('entered, warm:', !!warm, warm?.pc.iceGatheringState)
    if (!warm || !this.call) return
    // One message: "I'm in" + the offer to the caller.
    this.addPeer(warm.key, r.from, r.name)
    const pc = warm.pc
    this.pcs.set(warm.key, pc)
    this.wire(warm.key, pc)
    this.attach(pc)
    this.trickle.add(pc)
    await firstCandidates(pc)
    if (!this.call || this.pcs.get(warm.key) !== pc) return
    this.send(this.call.chatId, { t: 'join', call: this.call.id, mic: this.mic, cam: this.cam,
      offer: { to: warm.key, sdp: pc.localDescription?.sdp } })
  }

  decline() {
    const r = this.ringing
    if (!r) return
    this.stopRinging()
    this.dropWarm()
    this.send(r.chatId, { t: 'decline', call: r.call })
  }

  // ---- starting / joining ----
  /** Calls a chat (1:1 or group). */
  async start(chatId: string, video: boolean) {
    if (this.call) { this.minimized = false; return }
    const id = rid()
    if (!(await this.enter(chatId, id, video, true, false))) return
    // The invite also says "I'm in" (no separate join ahead of it).
    this.send(chatId, { t: 'invite', call: id, video, mic: this.mic, cam: this.cam })
    this.tone = ringtone('outgoing')
    clearTimeout(this.ringTimer)
    this.ringTimer = setTimeout(() => {
      if (this.call?.id === id && !Object.keys(this.peers).length) {
        app.toast('Никто не ответил', 'info')
        this.hangup(true)
      }
    }, RING_MS)
  }

  /** Joins a group call that is already going on. */
  async join(chatId: string) {
    const a = this.active[chatId]
    if (!a || this.call) return
    await this.enter(chatId, a.call, a.video, false, true)
  }

  private async enter(chatId: string, id: string, video: boolean, outgoing: boolean, announce: boolean): Promise<boolean> {
    try {
      this.local = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
        video: video ? { facingMode: 'user', width: { ideal: 1280 }, height: { ideal: 720 } } : false,
      })
    } catch (e) {
      app.toast('Нет доступа к микрофону' + (video ? ' или камере' : '') + ' — разрешите его в настройках', 'error', 6000)
      return false
    }
    this.camTrack = this.local.getVideoTracks()[0] ?? null
    this.facing = 'user'
    if (this.camTrack) this.checkFlip()
    this.mic = true
    this.cam = !!this.camTrack
    this.screen = false
    this.minimized = false
    this.peers = {}
    dbg('media ready')
    this.call = { id, chatId, video, outgoing, startedAt: Date.now(), connectedAt: 0 }
    await this.ensureIce()
    dbg('ice ready')
    delete this.active[chatId]
    this.meter(this.myKey, this.local)
    if (announce) this.send(chatId, { t: 'join', call: id, mic: this.mic, cam: this.cam })
    clearInterval(this.aliveTimer)
    if (app.chats.find((c) => c.id === chatId)?.kind === 'group') {
      this.aliveTimer = setInterval(() => this.call && this.send(this.call.chatId, { t: 'alive', call: this.call.id }), ALIVE_MS)
    }
    return true
  }

  // ---- peer connections ----
  private addPeer(key: string, userId: string, name: string) {
    if (!this.peers[key]) {
      this.peers[key] = { key, userId, name, stream: null, state: 'connecting', mic: true, cam: false, speaking: false }
      for (const pc of this.pcs.values()) this.tune(pc) // the upload is shared by one more
    }
    if (this.tone && this.call?.outgoing) { this.tone.stop(); this.tone = null }
  }

  private newPC(key: string): RTCPeerConnection {
    this.pcs.get(key)?.close()
    const pc = new RTCPeerConnection({ iceServers: this.ice ?? [] })
    this.pcs.set(key, pc)
    this.wire(key, pc)
    return pc
  }

  /** Event handlers of a peer connection. */
  private wire(key: string, pc: RTCPeerConnection) {
    pc.ontrack = (e) => {
      const p = this.peers[key]
      if (!p) return
      const stream = p.stream ?? new MediaStream()
      if (!stream.getTracks().includes(e.track)) stream.addTrack(e.track)
      p.stream = stream
      this.peers[key] = { ...p, stream }
      if (e.track.kind === 'audio') this.meter(key, stream)
    }
    pc.onicecandidate = (e) => {
      if (e.candidate && this.trickle.has(pc) && this.call && this.pcs.get(key) === pc) this.queueCandidate(key, e.candidate.toJSON())
    }
    let lost: ReturnType<typeof setTimeout> | undefined
    pc.onconnectionstatechange = () => {
      const p = this.peers[key]
      if (!p || this.pcs.get(key) !== pc) return
      clearTimeout(lost)
      dbg('pc', key.slice(0, 6), pc.connectionState)
      if (pc.connectionState === 'connected') {
        p.state = 'connected'
        this.restarts.delete(key)
        this.iceRestarts.delete(key)
        this.tune(pc)
        this.route(key, pc)
        if (this.call && !this.call.connectedAt) this.call.connectedAt = Date.now()
      } else if (pc.connectionState === 'failed') {
        this.repair(key)
      } else if (pc.connectionState === 'disconnected') {
        // Often a short loss: give ICE a moment to recover on its own.
        p.state = 'connecting'
        lost = setTimeout(() => pc.connectionState === 'disconnected' && this.repair(key), 2000)
      }
    }
  }

  /** Finds out whether the selected ICE pair goes through the TURN relay
   * (shown on the tile; logged to the diagnostics report). */
  private async route(key: string, pc: RTCPeerConnection) {
    const stats = await pc.getStats().catch(() => null)
    if (!stats || this.pcs.get(key) !== pc) return
    let pair: any = null
    stats.forEach((r: any) => {
      if (r.type === 'transport' && r.selectedCandidatePairId) pair = stats.get(r.selectedCandidatePairId)
    })
    if (!pair) stats.forEach((r: any) => { if (r.type === 'candidate-pair' && r.nominated && r.state === 'succeeded') pair = r })
    if (!pair) return
    const local = stats.get(pair.localCandidateId), remote = stats.get(pair.remoteCandidateId)
    const relay = local?.candidateType === 'relay' || remote?.candidateType === 'relay'
    const p = this.peers[key]
    if (p) this.peers[key] = { ...p, route: relay ? 'relay' : 'direct' }
    logClient(`call: ${relay ? 'relayed via TURN' : 'direct'} (${local?.candidateType}/${local?.protocol} ↔ ${remote?.candidateType}), rtt ${Math.round((pair.currentRoundTripTime ?? 0) * 1000)} ms`)
  }

  /** Candidates leave in small batches (each signal is an MLS message). */
  private queueCandidate(key: string, c: RTCIceCandidateInit) {
    const box = this.outbox.get(key)
    if (box) {
      box.cands.push(c)
      return
    }
    const timer = setTimeout(() => {
      const b = this.outbox.get(key)
      this.outbox.delete(key)
      if (b && this.call) this.send(this.call.chatId, { t: 'ice', call: this.call.id, to: key, cands: b.cands })
    }, TRICKLE_BATCH_MS)
    this.outbox.set(key, { cands: [c], timer })
  }

  /** A broken path: ICE restart (keeps the DTLS session, fast); after two
   * that did not help, the connection is rebuilt. */
  private repair(key: string) {
    const n = (this.iceRestarts.get(key) ?? 0) + 1
    this.iceRestarts.set(key, n)
    if (n > 2) {
      this.iceRestarts.delete(key)
      this.reconnect(key)
      return
    }
    this.restartIce(key)
  }

  /** ICE restart: the device with the smaller id sends a new offer; the
   * other one asks it to. */
  private async restartIce(key: string) {
    const pc = this.pcs.get(key)
    if (!pc || !this.call) return
    if (this.myKey > key) {
      this.send(this.call.chatId, { t: 'restart', call: this.call.id, to: key })
      return
    }
    if (pc.signalingState !== 'stable') return
    if (this.peers[key]) this.peers[key].state = 'connecting'
    await this.ensureIce()
    try { pc.setConfiguration({ ...pc.getConfiguration(), iceServers: this.ice ?? [] }) } catch { /* not supported */ }
    await pc.setLocalDescription(opusFec(await pc.createOffer({ iceRestart: true })))
    await firstCandidates(pc)
    if (!this.call || this.pcs.get(key) !== pc) return
    this.send(this.call.chatId, { t: 'offer', call: this.call.id, to: key, sdp: pc.localDescription?.sdp, restart: true })
  }

  /** The device's network changed: restart ICE on every connection now. */
  networkChanged() {
    if (!this.call) return
    for (const key of this.pcs.keys()) {
      this.iceRestarts.delete(key)
      this.restartIce(key)
    }
  }

  /** Rebuilds a connection that failed (the offering side starts over). */
  private reconnect(key: string) {
    const p = this.peers[key]
    if (!p || !this.call) return
    const n = (this.restarts.get(key) ?? 0) + 1
    this.restarts.set(key, n)
    if (n > 3) {
      p.state = 'failed'
      return
    }
    p.state = 'connecting'
    if (this.myKey < key) setTimeout(() => this.call && this.offer(key), 300 * n)
  }

  private flushIce(key: string, pc: RTCPeerConnection) {
    const list = this.pendingIce.get(key)
    this.pendingIce.delete(key)
    for (const c of list ?? []) pc.addIceCandidate(c).catch(() => {})
  }

  /** Our tracks on the audio/video transceivers (camera can be switched
   * on later without renegotiation: replaceTrack on the video sender). */
  private attach(pc: RTCPeerConnection) {
    const audio = this.local?.getAudioTracks()[0] ?? null
    const video = this.screenTrack ?? (this.cam ? this.camTrack : null)
    for (const t of pc.getTransceivers()) {
      const kind = t.receiver.track?.kind
      if (kind === 'audio') { t.direction = 'sendrecv'; t.sender.replaceTrack(audio) }
      if (kind === 'video') { t.direction = 'sendrecv'; t.sender.replaceTrack(video) }
    }
    this.tune(pc)
  }

  /** Voice first: audio gets the higher network priority; video shares the
   * upload with every other participant (a mesh sends one copy each). */
  private tune(pc: RTCPeerConnection) {
    const peers = Math.max(1, Object.keys(this.peers).length)
    const videoMax = peers <= 1 ? 1_500_000 : Math.max(250_000, Math.floor(1_800_000 / peers))
    for (const t of pc.getTransceivers()) {
      const kind = t.receiver.track?.kind
      const params = t.sender.getParameters()
      if (!params.encodings?.length) continue
      for (const e of params.encodings) {
        if (kind === 'audio') {
          e.priority = 'high'
          e.networkPriority = 'high'
        } else if (kind === 'video') {
          e.maxBitrate = videoMax
          e.priority = 'low'
        }
      }
      if (kind === 'video') (params as RTCRtpSendParameters & { degradationPreference?: string }).degradationPreference = 'balanced'
      t.sender.setParameters(params).catch(() => {})
    }
  }

  private async offer(key: string) {
    if (!this.call) return
    const pc = this.newPC(key)
    pc.addTransceiver('audio', { direction: 'sendrecv' })
    pc.addTransceiver('video', { direction: 'sendrecv' })
    this.attach(pc)
    this.pendingIce.delete(key)
    await pc.setLocalDescription(opusFec(await pc.createOffer()))
    this.trickle.add(pc)
    await firstCandidates(pc)
    if (!this.call || this.pcs.get(key) !== pc) return
    this.send(this.call.chatId, { t: 'offer', call: this.call.id, to: key, sdp: pc.localDescription?.sdp })
  }

  private async answer(key: string, userId: string, name: string, sdp: string, restart: boolean) {
    if (!this.call) return
    this.addPeer(key, userId, name)
    // An ICE restart renegotiates the existing connection.
    const existing = this.pcs.get(key)
    const pc = restart && existing && existing.connectionState !== 'closed' ? existing : this.newPC(key)
    await pc.setRemoteDescription({ type: 'offer', sdp })
    this.flushIce(key, pc)
    this.attach(pc)
    await pc.setLocalDescription(opusFec(await pc.createAnswer()))
    this.trickle.add(pc)
    await firstCandidates(pc)
    if (!this.call || this.pcs.get(key) !== pc) return
    this.trickle.add(pc)
    this.send(this.call.chatId, { t: 'answer', call: this.call.id, to: key, sdp: pc.localDescription?.sdp })
    if (!restart) this.sendMedia()
  }

  private dropPeer(key: string) {
    this.pcs.get(key)?.close()
    this.pcs.delete(key)
    this.meters.get(key)?.()
    this.meters.delete(key)
    delete this.peers[key]
    for (const pc of this.pcs.values()) this.tune(pc)
    // 1:1: the other side hung up.
    if (this.call && !Object.keys(this.peers).length && this.call.connectedAt) {
      const direct = app.chats.find((c) => c.id === this.call!.chatId)?.kind === 'direct'
      if (direct) this.hangup(false)
    }
  }

  // ---- controls ----
  private sendMedia() {
    if (this.call) this.send(this.call.chatId, { t: 'media', call: this.call.id, mic: this.mic, cam: this.cam || this.screen })
  }

  toggleMic() {
    this.mic = !this.mic
    this.local?.getAudioTracks().forEach((t) => (t.enabled = this.mic))
    this.sendMedia()
  }

  async toggleCam() {
    if (!this.call) return
    if (!this.cam) {
      if (!this.camTrack || this.camTrack.readyState === 'ended') {
        try {
          const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: this.facing, width: { ideal: 1280 }, height: { ideal: 720 } } })
          this.camTrack = s.getVideoTracks()[0]
          this.checkFlip()
          this.local?.addTrack(this.camTrack)
          this.local = new MediaStream(this.local?.getTracks() ?? [])
        } catch {
          app.toast('Нет доступа к камере', 'error')
          return
        }
      }
      this.cam = true
    } else {
      this.cam = false
      this.camTrack?.stop()
      if (this.camTrack) this.local?.removeTrack(this.camTrack)
      this.camTrack = null
      this.local = new MediaStream(this.local?.getTracks() ?? [])
    }
    this.replaceVideo()
    this.sendMedia()
  }

  get canShare(): boolean {
    return !!navigator.mediaDevices && 'getDisplayMedia' in navigator.mediaDevices && !/Android|iPhone|iPad/i.test(navigator.userAgent)
  }

  async toggleScreen() {
    if (!this.call) return
    if (this.screenTrack) {
      this.screenTrack.stop()
      this.screenTrack = null
      this.screen = false
    } else {
      try {
        const s = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false })
        this.screenTrack = s.getVideoTracks()[0]
        this.screenTrack.onended = () => { if (this.screen) this.toggleScreen() }
        this.screen = true
      } catch {
        return
      }
    }
    this.replaceVideo()
    this.sendMedia()
  }

  /** Switches between the front and the back camera during a call. */
  async flipCamera() {
    if (!this.call || !this.cam) return
    const next = this.facing === 'user' ? 'environment' : 'user'
    let track: MediaStreamTrack
    try {
      // The old camera is released first: phones open only one at a time.
      const old = this.camTrack
      old?.stop()
      const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: next }, width: { ideal: 1280 }, height: { ideal: 720 } } })
      track = s.getVideoTracks()[0]
      if (old) this.local?.removeTrack(old)
    } catch {
      app.toast('Не удалось переключить камеру', 'error')
      // Get the previous one back.
      try {
        const s = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: this.facing } } })
        track = s.getVideoTracks()[0]
      } catch {
        return
      }
    }
    if (this.camTrack) this.local?.removeTrack(this.camTrack)
    this.camTrack = track
    this.local?.addTrack(track)
    this.local = new MediaStream(this.local?.getTracks() ?? [])
    this.facing = (track.getSettings().facingMode as 'user' | 'environment' | undefined) ?? next
    this.replaceVideo()
  }

  private async checkFlip() {
    const ds = await navigator.mediaDevices.enumerateDevices().catch(() => [])
    this.canFlip = ds.filter((d) => d.kind === 'videoinput').length > 1
  }

  /** What we show of ourselves: the screen while sharing, else the camera. */
  get localVideo(): MediaStream | null {
    const t = this.screenTrack ?? (this.cam ? this.camTrack : null)
    return t ? new MediaStream([t]) : null
  }

  private replaceVideo() {
    const t = this.screenTrack ?? (this.cam ? this.camTrack : null)
    for (const pc of this.pcs.values()) {
      for (const tr of pc.getTransceivers()) {
        if (tr.receiver.track?.kind === 'video') tr.sender.replaceTrack(t).catch(() => {})
      }
    }
  }

  /** Leaves the call. cancel: we were the caller and nobody answered. */
  hangup(cancel = false) {
    const c = this.call
    if (!c) return
    this.send(c.chatId, { t: cancel ? 'end' : 'leave', call: c.id })
    clearInterval(this.aliveTimer)
    // Others are still talking in the group: offer to come back.
    if (!cancel && Object.keys(this.peers).length && app.chats.find((x) => x.id === c.chatId)?.kind === 'group') {
      this.active[c.chatId] = { call: c.id, video: c.video, at: Date.now() }
    }
    for (const pc of this.pcs.values()) pc.close()
    this.pcs.clear()
    this.pendingIce.clear()
    this.restarts.clear()
    this.iceRestarts.clear()
    for (const b of this.outbox.values()) clearTimeout(b.timer)
    this.outbox.clear()
    this.dropWarm()
    for (const stop of this.meters.values()) stop()
    this.meters.clear()
    this.local?.getTracks().forEach((t) => t.stop())
    this.screenTrack?.stop()
    this.screenTrack = null
    this.camTrack = null
    this.local = null
    this.peers = {}
    this.call = null
    this.tone?.stop()
    this.tone = null
    clearTimeout(this.ringTimer)
    if (c.connectedAt) ringtone('end')
  }

  private expireActive() {
    const now = Date.now()
    for (const [k, v] of Object.entries(this.active)) if (now - v.at > 10 * 60_000) delete this.active[k]
  }

  /** Who is talking: a level meter per stream (the tile glows). */
  private meter(key: string, stream: MediaStream) {
    this.meters.get(key)?.()
    try {
      const ac = new AudioContext()
      const an = ac.createAnalyser()
      an.fftSize = 512
      ac.createMediaStreamSource(stream).connect(an)
      const buf = new Uint8Array(an.fftSize)
      const timer = setInterval(() => {
        an.getByteTimeDomainData(buf)
        let sum = 0
        for (const x of buf) sum += (x - 128) * (x - 128)
        const speaking = Math.sqrt(sum / buf.length) > 6
        if (key === this.myKey) this.selfSpeaking = speaking && this.mic
        else if (this.peers[key] && this.peers[key].speaking !== speaking) this.peers[key].speaking = speaking
      }, 150)
      this.meters.set(key, () => { clearInterval(timer); ac.close().catch(() => {}) })
    } catch { /* no Web Audio */ }
  }
  selfSpeaking = $state(false)
}

export type CallProbe = { url: string; ok: boolean; ip: string; err: string }
export type CallCheck = { probes: CallProbe[]; relay: boolean; relayIp: string; error: string }

/** Settings → "Проверить звонки": tries every STUN/TURN address of the
 * server on its own (STUN: is the server reachable; TURN: can it relay —
 * then calls work from any network). Errors of a working address are not
 * shown: browsers report one per network interface (IPv6, VPN, WSL…). */
export async function checkCalls(): Promise<CallCheck> {
  const res: CallCheck = { probes: [], relay: false, relayIp: '', error: '' }
  const list = (await Messenger.IceServers().catch(() => null)) ?? []
  if (!list.length) {
    res.error = 'Сервер не выдал адреса для звонков — обновите сервер (deploy/init-env.sh --upgrade)'
    return res
  }
  const probes = list.flatMap((x) => (x.urls ?? []).map((url) => ({ url, username: x.username || undefined, credential: x.credential || undefined })))
  res.probes = await Promise.all(probes.map(async (p): Promise<CallProbe> => {
    const turn = p.url.startsWith('turn')
    const out: CallProbe = { url: p.url, ok: false, ip: '', err: '' }
    const codes = new Set<number>()
    const pc = new RTCPeerConnection({ iceServers: [{ urls: p.url, username: p.username, credential: p.credential }], iceTransportPolicy: turn ? 'relay' : 'all' })
    pc.createDataChannel('check')
    pc.onicecandidate = (e) => {
      const c = e.candidate
      if (c && c.type === (turn ? 'relay' : 'srflx')) { out.ok = true; out.ip = c.address ?? '' }
    }
    pc.onicecandidateerror = (e: Event) => codes.add((e as RTCPeerConnectionIceErrorEvent).errorCode)
    await pc.setLocalDescription(opusFec(await pc.createOffer()))
    const t0 = Date.now()
    while (!out.ok && pc.iceGatheringState !== 'complete' && Date.now() - t0 < 7000) await new Promise((r) => setTimeout(r, 200))
    pc.close()
    if (!out.ok) {
      out.err = codes.has(401) ? 'неверный пароль TURN (перезапустите сервер: up -d)'
        : codes.has(701) || !codes.size ? 'нет ответа (порт закрыт или UDP режется по пути)'
        : 'ошибка ' + [...codes].join(', ')
    }
    return out
  }))
  const relay = res.probes.find((p) => p.ok && p.url.startsWith('turn'))
  res.relay = !!relay
  res.relayIp = relay?.ip ?? ''
  return res
}

/** Waits briefly for the first candidates (host ones come at once), so the
 * SDP usually carries a usable path; the others are trickled. */
function firstCandidates(pc: RTCPeerConnection): Promise<void> {
  if (pc.iceGatheringState === 'complete') return Promise.resolve()
  return new Promise((resolve) => {
    const done = () => { pc.removeEventListener('icegatheringstatechange', check); clearTimeout(t); resolve() }
    const check = () => pc.iceGatheringState === 'complete' && done()
    const t = setTimeout(done, FIRST_CANDIDATES_MS)
    pc.addEventListener('icegatheringstatechange', check)
  })
}

/** Synthesized tones (no audio files): incoming ring, outgoing beeps, hang-up. */
function ringtone(kind: 'incoming' | 'outgoing' | 'end'): { stop: () => void } {
  let ac: AudioContext
  try { ac = new AudioContext() } catch { return { stop() {} } }
  const gain = ac.createGain()
  gain.gain.value = kind === 'incoming' ? 0.12 : 0.07
  gain.connect(ac.destination)
  const beep = (at: number, freq: number, dur: number) => {
    const o = ac.createOscillator()
    o.type = 'sine'
    o.frequency.value = freq
    const g = ac.createGain()
    g.gain.setValueAtTime(0, at)
    g.gain.linearRampToValueAtTime(1, at + 0.02)
    g.gain.setValueAtTime(1, at + dur - 0.03)
    g.gain.linearRampToValueAtTime(0, at + dur)
    o.connect(g).connect(gain)
    o.start(at)
    o.stop(at + dur)
  }
  let timer: ReturnType<typeof setInterval> | undefined
  const play = () => {
    const t = ac.currentTime + 0.05
    if (kind === 'incoming') { beep(t, 880, 0.18); beep(t + 0.22, 660, 0.18); beep(t + 0.44, 880, 0.18); beep(t + 0.66, 660, 0.18) }
    else if (kind === 'outgoing') beep(t, 440, 1.0)
    else { beep(t, 480, 0.15); beep(t + 0.2, 360, 0.25) }
  }
  play()
  if (kind !== 'end') timer = setInterval(play, kind === 'incoming' ? 2200 : 3000)
  else setTimeout(() => ac.close().catch(() => {}), 800)
  return { stop() { clearInterval(timer); ac.close().catch(() => {}) } }
}

export const calls = new CallStore()
if ((window as unknown as { __callDebug?: boolean }).__callDebug) (window as unknown as { __calls: CallStore }).__calls = calls

onEvent((e) => {
  if (e.type !== 'signal' || !e.detail) return
  try { calls.onSignal(JSON.parse(e.detail) as CallSignal) } catch { /* malformed */ }
})

/** After start / account switch: a call may be ringing already (the app was
 * opened from the "incoming call" notification). */
export async function checkPendingCall() {
  const s = await Messenger.PendingCall().catch(() => null)
  if (s) calls.onSignal(s)
}
