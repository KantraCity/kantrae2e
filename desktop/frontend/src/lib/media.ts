// Media preparation and recording in the UI, the way Telegram does it:
// photos are re-encoded (max 2560 px JPEG), photos and videos get a tiny
// inline preview, voice messages a waveform, video messages are square.
import type { MediaUpload } from './api'

export const MAX_FILE = 64 * 1024 * 1024

export function blobToBase64(b: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    // "data:<type>;base64,<data>" — the type itself may contain commas
    // (video/webm;codecs=vp9,opus).
    r.onload = () => {
      const u = String(r.result)
      const i = u.indexOf(';base64,')
      resolve(i >= 0 ? u.slice(i + 8) : '')
    }
    r.onerror = () => reject(r.error)
    r.readAsDataURL(b)
  })
}

function canvasBlob(c: HTMLCanvasElement, type: string, q: number): Promise<Blob> {
  return new Promise((resolve, reject) => c.toBlob((b) => (b ? resolve(b) : reject(new Error('encode failed'))), type, q))
}

/** A ~64 px JPEG preview (a few KB), shown blurred until the file loads. */
async function thumb(src: CanvasImageSource, w: number, h: number, square = false): Promise<string> {
  const c = document.createElement('canvas')
  let sx = 0, sy = 0, sw = w, sh = h
  if (square) {
    const side = Math.min(w, h)
    sx = (w - side) / 2; sy = (h - side) / 2; sw = sh = side
  }
  const scale = 64 / Math.max(sw, sh)
  c.width = Math.max(1, Math.round(sw * scale))
  c.height = Math.max(1, Math.round(sh * scale))
  c.getContext('2d')!.drawImage(src, sx, sy, sw, sh, 0, 0, c.width, c.height)
  return blobToBase64(await canvasBlob(c, 'image/jpeg', 0.6))
}

/** Loads a video's metadata and a frame for the preview. */
function videoInfo(url: string): Promise<{ v: HTMLVideoElement; w: number; h: number; dur: number }> {
  return new Promise((resolve, reject) => {
    const v = document.createElement('video')
    v.muted = true
    v.preload = 'auto'
    v.playsInline = true
    v.src = url
    v.onloadeddata = () => {
      v.currentTime = Math.min(0.1, v.duration || 0)
    }
    v.onseeked = () => resolve({ v, w: v.videoWidth, h: v.videoHeight, dur: isFinite(v.duration) ? v.duration : 0 })
    v.onerror = () => reject(new Error('video'))
    setTimeout(() => reject(new Error('video timeout')), 8000)
  })
}

export type Prepared = { upload: MediaUpload; localUrl: string; blob: Blob }

/** Prepares a chosen file. compress: send images/videos "as photo/video". */
export async function prepareFile(file: File, compress: boolean): Promise<Prepared> {
  const base: MediaUpload = { name: file.name, base64: '', mime: file.type, kind: 'file', w: 0, h: 0, dur: 0, thumb: '', wave: [], caption: '' }
  let blob: Blob = file
  if (compress && file.type.startsWith('image/') && file.type !== 'image/gif') {
    try {
      const bmp = await createImageBitmap(file)
      const scale = Math.min(1, 2560 / Math.max(bmp.width, bmp.height))
      const c = document.createElement('canvas')
      c.width = Math.round(bmp.width * scale)
      c.height = Math.round(bmp.height * scale)
      c.getContext('2d')!.drawImage(bmp, 0, 0, c.width, c.height)
      blob = await canvasBlob(c, 'image/jpeg', 0.87)
      if (blob.size > file.size && scale === 1 && file.type === 'image/jpeg') blob = file
      Object.assign(base, {
        kind: 'photo', mime: blob.type || 'image/jpeg', w: c.width, h: c.height,
        name: file.name.replace(/\.[^.]+$/, '') + '.jpg', thumb: await thumb(c, c.width, c.height),
      })
    } catch { /* not decodable: send as a file */ }
  } else if (compress && file.type === 'image/gif') {
    try {
      const bmp = await createImageBitmap(file)
      Object.assign(base, { kind: 'photo', w: bmp.width, h: bmp.height, thumb: await thumb(bmp, bmp.width, bmp.height) })
    } catch { /* file */ }
  } else if (compress && file.type.startsWith('video/')) {
    const url = URL.createObjectURL(file)
    try {
      const { v, w, h, dur } = await videoInfo(url)
      Object.assign(base, { kind: 'video', w, h, dur, thumb: w ? await thumb(v, w, h) : '' })
    } catch {
      Object.assign(base, { kind: 'video' }) // the WebView cannot decode it: no preview
    } finally {
      URL.revokeObjectURL(url)
    }
  }
  base.base64 = await blobToBase64(blob)
  return { upload: base, localUrl: URL.createObjectURL(blob), blob }
}

function pickMime(cands: string[]): string {
  if (typeof MediaRecorder === 'undefined') throw new Error('Запись не поддерживается в этом окружении')
  return cands.find((m) => MediaRecorder.isTypeSupported(m)) ?? ''
}

export function micError(e: unknown): string {
  const n = (e as { name?: string })?.name
  if (n === 'NotAllowedError' || n === 'SecurityError') return 'Нет доступа к микрофону/камере — разрешите его в настройках'
  if (n === 'NotFoundError') return 'Микрофон или камера не найдены'
  return e instanceof Error ? e.message : 'Не удалось начать запись'
}

/** Voice message recorder (Opus) with a live level meter and waveform. */
export class VoiceRecorder {
  levels: number[] = []
  onLevel: ((v: number) => void) | null = null
  started = 0
  private rec!: MediaRecorder
  private stream!: MediaStream
  private chunks: Blob[] = []
  private ac?: AudioContext
  private timer?: ReturnType<typeof setInterval>

  async start() {
    this.stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true } })
    const mime = pickMime(['audio/webm;codecs=opus', 'audio/ogg;codecs=opus', 'audio/mp4', 'audio/webm'])
    this.rec = new MediaRecorder(this.stream, mime ? { mimeType: mime, audioBitsPerSecond: 32000 } : undefined)
    this.rec.ondataavailable = (e) => e.data.size && this.chunks.push(e.data)
    this.rec.start(250)
    this.started = Date.now()
    try {
      this.ac = new AudioContext()
      const an = this.ac.createAnalyser()
      an.fftSize = 512
      this.ac.createMediaStreamSource(this.stream).connect(an)
      const buf = new Uint8Array(an.fftSize)
      this.timer = setInterval(() => {
        an.getByteTimeDomainData(buf)
        let sum = 0
        for (const x of buf) sum += (x - 128) * (x - 128)
        const v = Math.min(1, Math.sqrt(sum / buf.length) / 40)
        this.levels.push(v)
        this.onLevel?.(v)
      }, 50)
    } catch { /* no analyser: flat waveform */ }
  }

  private release() {
    clearInterval(this.timer)
    this.ac?.close().catch(() => {})
    this.stream?.getTracks().forEach((t) => t.stop())
  }

  cancel() {
    if (this.rec?.state !== 'inactive') this.rec?.stop()
    this.release()
  }

  async stop(): Promise<Prepared | null> {
    const dur = (Date.now() - this.started) / 1000
    await new Promise<void>((r) => { this.rec.onstop = () => r(); this.rec.stop() })
    this.release()
    if (dur < 0.7) return null // a tap, not a message
    const type = this.rec.mimeType || 'audio/webm'
    const blob = new Blob(this.chunks, { type })
    const ext = type.includes('ogg') ? 'ogg' : type.includes('mp4') ? 'm4a' : 'webm'
    // 48 bars, like Telegram's voice waveform.
    const bars = 48, wave: number[] = []
    const L = this.levels.length || 1
    for (let i = 0; i < bars; i++) {
      const seg = this.levels.slice(Math.floor((i * L) / bars), Math.max(Math.floor(((i + 1) * L) / bars), Math.floor((i * L) / bars) + 1))
      wave.push(Math.round(255 * Math.min(1, Math.max(0.05, Math.max(0, ...seg)))))
    }
    return {
      upload: { name: `voice.${ext}`, base64: await blobToBase64(blob), mime: type.split(';')[0], kind: 'voice', w: 0, h: 0, dur, thumb: '', wave, caption: '' },
      localUrl: URL.createObjectURL(blob), blob,
    }
  }
}

/** Round video message recorder (square, front camera, up to 60 s). */
export class RoundRecorder {
  stream!: MediaStream
  started = 0
  private rec!: MediaRecorder
  private chunks: Blob[] = []

  async start() {
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: true, noiseSuppression: true },
      video: { facingMode: 'user', width: { ideal: 480 }, height: { ideal: 480 }, aspectRatio: 1 },
    })
    const mime = pickMime(['video/webm;codecs=vp9,opus', 'video/webm;codecs=vp8,opus', 'video/webm', 'video/mp4'])
    this.rec = new MediaRecorder(this.stream, mime ? { mimeType: mime, videoBitsPerSecond: 1_000_000 } : undefined)
    this.rec.ondataavailable = (e) => e.data.size && this.chunks.push(e.data)
    this.rec.start(250)
    this.started = Date.now()
  }

  cancel() {
    if (this.rec?.state !== 'inactive') this.rec?.stop()
    this.stream?.getTracks().forEach((t) => t.stop())
  }

  async stop(preview: HTMLVideoElement | undefined): Promise<Prepared | null> {
    const dur = (Date.now() - this.started) / 1000
    // The preview frame (centre square) is the thumbnail.
    let th = ''
    if (preview && preview.videoWidth) {
      try { th = await thumb(preview, preview.videoWidth, preview.videoHeight, true) } catch { /* none */ }
    }
    const w = preview?.videoWidth ?? 0, h = preview?.videoHeight ?? 0
    await new Promise<void>((r) => { this.rec.onstop = () => r(); this.rec.stop() })
    this.stream.getTracks().forEach((t) => t.stop())
    if (dur < 1) return null
    const type = this.rec.mimeType || 'video/webm'
    const blob = new Blob(this.chunks, { type })
    return {
      upload: { name: `round.${type.includes('mp4') ? 'mp4' : 'webm'}`, base64: await blobToBase64(blob), mime: type.split(';')[0],
        kind: 'round', w: Math.min(w, h), h: Math.min(w, h), dur, thumb: th, wave: [], caption: '' },
      localUrl: URL.createObjectURL(blob), blob,
    }
  }
}

export function duration(sec: number): string {
  const s = Math.max(0, Math.round(sec))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

// Permission is asked once with a separate tap (the system dialog would
// otherwise interrupt a press-and-hold and the recording would go nowhere).
const permKey = (kind: 'voice' | 'round') => 'kantra.perm.' + kind
export function hasRecordPermission(kind: 'voice' | 'round'): boolean {
  try { return localStorage.getItem(permKey(kind)) === '1' } catch { return false }
}
export function rememberRecordPermission(kind: 'voice' | 'round') {
  try { localStorage.setItem(permKey(kind), '1') } catch { /* ignore */ }
}
export async function requestRecordPermission(kind: 'voice' | 'round'): Promise<void> {
  const s = await navigator.mediaDevices.getUserMedia(kind === 'voice' ? { audio: true } : { audio: true, video: { facingMode: 'user' } })
  s.getTracks().forEach((t) => t.stop())
  rememberRecordPermission(kind)
}
