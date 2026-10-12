// Emoji for the picker (like Telegram's): categories, search in Russian and
// English, recently used, skin tones. The data (emoji-data.json, built by
// scripts/gen-emoji.mjs) is loaded on first use.

export type Emoji = { e: string; g: number; words: string; skins?: string[] }

export const GROUPS: { id: number; icon: string; name: string }[] = [
  { id: 0, icon: '😀', name: 'Смайлики' },
  { id: 1, icon: '👋', name: 'Люди' },
  { id: 3, icon: '🐻', name: 'Животные и природа' },
  { id: 4, icon: '🍔', name: 'Еда и напитки' },
  { id: 6, icon: '⚽', name: 'Занятия' },
  { id: 5, icon: '🚗', name: 'Путешествия' },
  { id: 7, icon: '💡', name: 'Предметы' },
  { id: 8, icon: '🔣', name: 'Символы' },
  { id: 9, icon: '🏁', name: 'Флаги' },
]

// Skin tones: 0 = default (yellow), 1..5 = light..dark.
export const TONES = ['', '🏻', '🏼', '🏽', '🏾', '🏿']

let all: Emoji[] | null = null
let loading: Promise<Emoji[]> | null = null

export function loadEmoji(): Promise<Emoji[]> {
  if (all) return Promise.resolve(all)
  loading ??= import('./emoji-data.json').then((m) => {
    const raw = (m.default ?? m) as unknown as [string, number, string, string[]?][]
    all = raw.map(([e, g, words, skins]) => ({ e, g, words, skins }))
    return all
  })
  return loading
}

function read<T>(key: string, fallback: T): T {
  try {
    const v = localStorage.getItem(key)
    return v ? (JSON.parse(v) as T) : fallback
  } catch {
    return fallback
  }
}
function write(key: string, v: unknown) {
  try { localStorage.setItem(key, JSON.stringify(v)) } catch { /* ignore */ }
}

const DEFAULT_RECENT = ['👍', '❤️', '😂', '🔥', '😍', '🙏', '😢', '😮', '🎉', '👌', '😁', '🤔']

class EmojiPrefs {
  /** Recently used, most used first (emoji -> count, newest wins ties). */
  recent = $state<string[]>(read('kantra.emoji.recent', DEFAULT_RECENT))
  /** Preferred skin tone per base emoji (Telegram remembers it per emoji). */
  tones = $state<Record<string, number>>(read('kantra.emoji.tones', {}))
  private counts: Record<string, number> = read('kantra.emoji.counts', {})

  use(e: string) {
    this.counts[e] = (this.counts[e] ?? 0) + 1
    const list = [e, ...this.recent.filter((x) => x !== e)]
    // Keep the frequently used ones, drop the rarest beyond 40.
    this.recent = list.slice(0, 40)
    write('kantra.emoji.recent', this.recent)
    write('kantra.emoji.counts', this.counts)
  }

  setTone(base: string, tone: number) {
    this.tones[base] = tone
    write('kantra.emoji.tones', this.tones)
  }

  /** The emoji as the user wants it (with the chosen skin tone). */
  withTone(em: Emoji): string {
    const t = this.tones[em.e]
    return t && em.skins?.[t - 1] ? em.skins[t - 1] : em.e
  }
}

export const emojiPrefs = new EmojiPrefs()

/** Search by Russian/English words. Ranking: the name itself, then a name
 * starting with the query, then any word starting with it, then the rest. */
export function searchEmoji(list: Emoji[], q: string): Emoji[] {
  const s = q.trim().toLowerCase()
  if (!s) return []
  const scored: [number, number, Emoji][] = []
  list.forEach((em, i) => {
    const at = em.words.indexOf(s)
    if (at < 0) return
    const label = em.words.split(' ')[0]
    // words = "<ru label words> <ru tags> <en label> <en tags>"; the RU label is the first phrase.
    let score = 3
    if (em.words.startsWith(s + ' ') || em.words === s) score = 0
    else if (at === 0) score = 1
    else if (em.words[at - 1] === ' ') score = 2
    if (label === s) score = 0
    scored.push([score, i, em])
  })
  scored.sort((x, y) => x[0] - y[0] || x[1] - y[1])
  return scored.slice(0, 120).map((x) => x[2])
}

// 1–3 emoji and nothing else: shown big, without a bubble (like Telegram).
const ONLY_EMOJI = /^(?:\p{Extended_Pictographic}|\p{Regional_Indicator}|\p{Emoji_Modifier}|‍|️|⃣|[#*0-9]️?⃣|\s)+$/u
export function bigEmojiCount(text: string): number {
  const t = text.trim()
  if (!t || t.length > 40 || !ONLY_EMOJI.test(t) || /^[#*0-9\s]+$/.test(t)) return 0
  const n = [...new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(t)].filter((x) => x.segment.trim()).length
  return n <= 3 ? n : 0
}
