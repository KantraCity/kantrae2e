// Incoming call melodies (Settings → Звонки): a few synthesized ones (no
// audio files to ship), silence, or the person's own file. The choice and
// the file are kept on this device.

export type RingtoneId = 'classic' | 'melody' | 'bells' | 'marimba' | 'pulse' | 'silent' | 'custom'

export const RINGTONES: { id: RingtoneId; name: string }[] = [
  { id: 'classic', name: 'Классический' },
  { id: 'melody', name: 'Мелодия' },
  { id: 'bells', name: 'Колокольчики' },
  { id: 'marimba', name: 'Маримба' },
  { id: 'pulse', name: 'Тихий' },
  { id: 'silent', name: 'Без звука' },
]

const KEY = 'kantra.ringtone'
const FILE_KEY = 'kantra.ringtone.file'
const NAME_KEY = 'kantra.ringtone.name'
export const MAX_RINGTONE_FILE = 2 << 20

function get(k: string): string | null {
  try { return localStorage.getItem(k) } catch { return null }
}
function set(k: string, v: string | null) {
  try { v === null ? localStorage.removeItem(k) : localStorage.setItem(k, v) } catch { /* full or blocked */ }
}

export function ringtoneChoice(): RingtoneId {
  const v = get(KEY) as RingtoneId | null
  if (v === 'custom' && !get(FILE_KEY)) return 'classic'
  return v && (v === 'custom' || RINGTONES.some((r) => r.id === v)) ? v : 'classic'
}
export function setRingtone(id: RingtoneId) { set(KEY, id) }
export function customRingtoneName(): string { return get(NAME_KEY) ?? '' }

/** Keeps the person's own melody (as a data: URL). */
export async function setCustomRingtone(file: File): Promise<void> {
  if (file.size > MAX_RINGTONE_FILE) throw new Error('Файл больше 2 МБ — выберите покороче')
  if (!file.type.startsWith('audio/')) throw new Error('Это не аудиофайл')
  const url = await new Promise<string>((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result))
    r.onerror = () => reject(r.error)
    r.readAsDataURL(file)
  })
  set(FILE_KEY, url)
  if (get(FILE_KEY) !== url) throw new Error('Не хватает места для файла')
  set(NAME_KEY, file.name)
  set(KEY, 'custom')
}

type Note = [at: number, freq: number, dur: number, kind?: OscillatorType, decay?: boolean]

// One cycle of each melody (seconds) and the pause until it repeats.
const PATTERNS: Record<Exclude<RingtoneId, 'silent' | 'custom'>, { notes: Note[]; every: number; volume: number }> = {
  classic: { notes: [[0, 880, 0.18], [0.22, 660, 0.18], [0.44, 880, 0.18], [0.66, 660, 0.18]], every: 2.2, volume: 0.12 },
  melody: { notes: [[0, 659, 0.16], [0.18, 784, 0.16], [0.36, 988, 0.16], [0.54, 1319, 0.3], [0.9, 988, 0.16], [1.08, 1319, 0.36]], every: 2.6, volume: 0.1 },
  bells: { notes: [[0, 1568, 0.9, 'sine', true], [0.3, 1319, 0.9, 'sine', true], [0.6, 1047, 1.2, 'sine', true]], every: 2.4, volume: 0.14 },
  marimba: { notes: [[0, 523, 0.35, 'triangle', true], [0.2, 659, 0.35, 'triangle', true], [0.4, 784, 0.35, 'triangle', true], [0.6, 659, 0.35, 'triangle', true], [0.8, 523, 0.5, 'triangle', true]], every: 2.2, volume: 0.22 },
  pulse: { notes: [[0, 523, 0.5, 'sine', true], [0.6, 523, 0.5, 'sine', true]], every: 3, volume: 0.07 },
}

/** Plays the incoming-call melody until stop(); `id` overrides the choice
 * (Settings preview). Phones also vibrate where they can. */
export function playRingtone(id: RingtoneId = ringtoneChoice(), opts: { once?: boolean } = {}): { stop: () => void } {
  const vibrate = !opts.once && 'vibrate' in navigator
  let vib: ReturnType<typeof setInterval> | undefined
  if (vibrate) {
    const buzz = () => { try { navigator.vibrate([400, 300, 400]) } catch { /* not allowed */ } }
    buzz()
    vib = setInterval(buzz, 2200)
  }
  const stopVib = () => { clearInterval(vib); if (vibrate) try { navigator.vibrate(0) } catch { /* ignore */ } }

  if (id === 'silent') return { stop: stopVib }

  if (id === 'custom') {
    const url = get(FILE_KEY)
    if (!url) return playRingtone('classic', opts)
    const a = new Audio(url)
    a.loop = !opts.once
    a.play().catch(() => {})
    let t: ReturnType<typeof setTimeout> | undefined
    if (opts.once) t = setTimeout(() => a.pause(), 6000)
    return { stop() { clearTimeout(t); a.pause(); a.src = ''; stopVib() } }
  }

  let ac: AudioContext
  try { ac = new AudioContext() } catch { return { stop: stopVib } }
  const p = PATTERNS[id]
  const out = ac.createGain()
  out.gain.value = p.volume
  out.connect(ac.destination)
  const note = (at: number, freq: number, dur: number, kind: OscillatorType = 'sine', decay = false) => {
    const o = ac.createOscillator()
    o.type = kind
    o.frequency.value = freq
    const g = ac.createGain()
    g.gain.setValueAtTime(0, at)
    g.gain.linearRampToValueAtTime(1, at + 0.015)
    if (decay) g.gain.exponentialRampToValueAtTime(0.001, at + dur)
    else { g.gain.setValueAtTime(1, at + dur - 0.03); g.gain.linearRampToValueAtTime(0, at + dur) }
    o.connect(g).connect(out)
    o.start(at)
    o.stop(at + dur + 0.02)
  }
  const cycle = () => {
    const t = ac.currentTime + 0.05
    for (const [at, f, d, k, dec] of p.notes) note(t + at, f, d, k, dec)
  }
  cycle()
  const timer = opts.once ? undefined : setInterval(cycle, p.every * 1000)
  const close = opts.once ? setTimeout(() => ac.close().catch(() => {}), p.every * 1000) : undefined
  return { stop() { clearInterval(timer); clearTimeout(close); ac.close().catch(() => {}); stopVib() } }
}
