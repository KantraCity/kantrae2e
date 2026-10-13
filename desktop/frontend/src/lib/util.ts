const avatarColors = ['#e17076', '#faa774', '#a695e7', '#7bc862', '#6ec9cb', '#65aadd', '#ee7aae']

/** Height of the app's own window title bar (0 on phones and in the browser). */
export function titlebarHeight(): number {
  return parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--titlebar-h')) || 0
}

export function hash(s: string): number {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0
  return Math.abs(h)
}

export function avatarColor(key: string): string {
  return avatarColors[hash(key) % avatarColors.length]
}

// Name colors inside group chats (TG uses a lighter palette on dark).
const nameColors = ['#ff8e86', '#ffc16b', '#b69bff', '#8ee57a', '#71dfdf', '#72bdff', '#ff86c8']
export function nameColor(key: string): string {
  return nameColors[hash(key) % nameColors.length]
}

export function initials(name: string): string {
  const parts = name.trim().split(/[\s_.-]+/).filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[1][0]).toUpperCase()
}

// Date formatters are built once: toLocale*String with options creates a
// new Intl formatter on every call, which was the most expensive part of
// re-rendering the message list (every bubble shows its time).
const fmtHM = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' })
const fmtWeekday = new Intl.DateTimeFormat('ru-RU', { weekday: 'short' })
const fmtShortDate = new Intl.DateTimeFormat('ru-RU', { day: '2-digit', month: '2-digit', year: '2-digit' })
const fmtDayMonth = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' })
const fmtDayMonthYear = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' })
const hmCache = new Map<number, string>()

export function timeHM(ms: number): string {
  const minute = Math.floor(ms / 60000)
  let v = hmCache.get(minute)
  if (v === undefined) {
    v = fmtHM.format(ms)
    if (hmCache.size > 5000) hmCache.clear()
    hmCache.set(minute, v)
  }
  return v
}

export function chatListTime(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  const now = new Date()
  if (d.toDateString() === now.toDateString()) return timeHM(ms)
  const diff = (now.getTime() - ms) / 86400000
  if (diff < 7) return fmtWeekday.format(d)
  return fmtShortDate.format(d)
}

export function dayLabel(ms: number): string {
  const d = new Date(ms)
  const now = new Date()
  const yesterday = new Date(now.getTime() - 86400000)
  if (d.toDateString() === now.toDateString()) return 'Сегодня'
  if (d.toDateString() === yesterday.toDateString()) return 'Вчера'
  return (d.getFullYear() === now.getFullYear() ? fmtDayMonth : fmtDayMonthYear).format(d)
}

export function fileSize(n: number): string {
  if (n < 1024) return `${n} Б`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} КБ`
  return `${(n / 1024 / 1024).toFixed(1)} МБ`
}

export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10, m100 = n % 100
  if (m10 === 1 && m100 !== 11) return one
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few
  return many
}

export function readFileBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result).split(',', 2)[1] ?? '')
    r.onerror = () => reject(r.error)
    r.readAsDataURL(file)
  })
}

export function downloadBase64(name: string, mime: string, b64: string) {
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  const url = URL.createObjectURL(new Blob([bytes], { type: mime }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 10000)
}

/** Telegram's status line of a person: "в сети" / "был(а) в 14:20" … */
export function lastSeenText(p: { online: boolean; lastSeen: number; hidden: boolean } | undefined): string {
  if (!p) return ''
  if (p.online) return 'в сети'
  if (p.hidden || !p.lastSeen) return 'был(а) недавно'
  const d = new Date(p.lastSeen), now = new Date()
  const mins = Math.floor((now.getTime() - p.lastSeen) / 60000)
  if (mins < 1) return 'был(а) только что'
  if (mins < 60) return `был(а) ${mins} ${plural(mins, 'минуту', 'минуты', 'минут')} назад`
  const hm = timeHM(p.lastSeen)
  if (d.toDateString() === now.toDateString()) return `был(а) в ${hm}`
  const y = new Date(now); y.setDate(now.getDate() - 1)
  if (d.toDateString() === y.toDateString()) return `был(а) вчера в ${hm}`
  return `был(а) ${fmtDayMonth.format(d)}`
}
