// Message text formatting, Telegram-style markup kept in the text itself
// (so it travels inside the encrypted message as is):
//   **жирный**  __курсив__  ~~зачёркнутый~~  `код`  ```блок кода```
//   ||спойлер||  [текст](https://ссылка)
// Plus automatic links and @mentions. Rendering never uses HTML from the
// text: it produces tokens the component turns into elements.

export type Tok =
  | { t: 'text'; v: string }
  | { t: 'b' | 'i' | 's' | 'spoiler'; c: Tok[] }
  | { t: 'code'; v: string }
  | { t: 'pre'; v: string }
  | { t: 'link'; v: string; href: string }
  | { t: 'mention'; v: string }

const MARKS: [string, 'b' | 'i' | 's' | 'spoiler'][] = [['**', 'b'], ['__', 'i'], ['~~', 's'], ['||', 'spoiler']]
const URL_RE = /\b(?:https?:\/\/|www\.)[^\s<>"']+[^\s<>"'.,;:!?)\]}»]/giu
const MENTION_RE = /(^|[^\p{L}\p{N}_.@])@([a-zA-Z0-9_.-]{3,32})/gu

/** Parses the text into tokens (bounded nesting, never throws). */
export function parse(text: string, depth = 0): Tok[] {
  const out: Tok[] = []
  let buf = ''
  const flush = () => { if (buf) { out.push(...inline(buf)); buf = '' } }
  let i = 0
  while (i < text.length) {
    // ```code block```
    if (text.startsWith('```', i)) {
      const end = text.indexOf('```', i + 3)
      if (end > i + 3) {
        flush()
        out.push({ t: 'pre', v: text.slice(i + 3, end).replace(/^\n/, '') })
        i = end + 3
        continue
      }
    }
    // `code`
    if (text[i] === '`') {
      const end = text.indexOf('`', i + 1)
      if (end > i + 1 && !text.slice(i + 1, end).includes('\n')) {
        flush()
        out.push({ t: 'code', v: text.slice(i + 1, end) })
        i = end + 1
        continue
      }
    }
    // [label](url)
    if (text[i] === '[') {
      const m = /^\[([^\]\n]{1,200})\]\((https?:\/\/[^\s)]+)\)/.exec(text.slice(i))
      if (m) {
        flush()
        out.push({ t: 'link', v: m[1], href: m[2] })
        i += m[0].length
        continue
      }
    }
    if (depth < 4) {
      let matched = false
      for (const [mk, kind] of MARKS) {
        if (text.startsWith(mk, i)) {
          const end = text.indexOf(mk, i + mk.length)
          // Not "**" alone, not spanning an empty or blank content.
          if (end > i + mk.length && text.slice(i + mk.length, end).trim()) {
            flush()
            out.push({ t: kind, c: parse(text.slice(i + mk.length, end), depth + 1) })
            i = end + mk.length
            matched = true
            break
          }
        }
      }
      if (matched) continue
    }
    buf += text[i]
    i++
  }
  flush()
  return out
}

/** Links and @mentions inside plain text. */
function inline(s: string): Tok[] {
  const out: Tok[] = []
  const re = new RegExp(`${URL_RE.source}|${MENTION_RE.source}`, 'giu')
  let last = 0
  for (const m of s.matchAll(re)) {
    let start = m.index!
    let tok: Tok
    if (m[0].startsWith('http') || m[0].toLowerCase().startsWith('www.')) {
      const href = m[0].toLowerCase().startsWith('www.') ? 'https://' + m[0] : m[0]
      tok = { t: 'link', v: m[0], href }
    } else {
      start += m[1].length // the char before "@"
      tok = { t: 'mention', v: '@' + m[2] }
    }
    if (start > last) out.push({ t: 'text', v: s.slice(last, start) })
    out.push(tok)
    last = m.index! + m[0].length
  }
  if (last < s.length) out.push({ t: 'text', v: s.slice(last) })
  return out
}

/** The text without markup (chat list previews, notifications, copy). */
export function plain(text: string): string {
  const walk = (ts: Tok[]): string => ts.map((t) =>
    t.t === 'text' || t.t === 'code' || t.t === 'pre' || t.t === 'mention' ? t.v
    : t.t === 'link' ? t.v
    : t.t === 'spoiler' ? '▒▒▒' : walk(t.c)).join('')
  return walk(parse(text))
}

/** The first web link of a text (for the preview card). */
export function firstLink(text: string): string {
  for (const t of flat(parse(text))) if (t.t === 'link') return t.href
  return ''
}

function flat(ts: Tok[]): Tok[] {
  return ts.flatMap((t) => ('c' in t ? [t, ...flat(t.c)] : [t]))
}

/** Wraps the selection of a text field in markup (or removes it). */
export function wrap(el: HTMLTextAreaElement, text: string, mark: string, close = mark): { text: string; start: number; end: number } {
  const s = el.selectionStart ?? 0, e = el.selectionEnd ?? 0
  const sel = text.slice(s, e)
  // Already wrapped: unwrap.
  if (text.slice(s - mark.length, s) === mark && text.slice(e, e + close.length) === close) {
    return { text: text.slice(0, s - mark.length) + sel + text.slice(e + close.length), start: s - mark.length, end: e - mark.length }
  }
  return { text: text.slice(0, s) + mark + sel + close + text.slice(e), start: s + mark.length, end: e + mark.length }
}
