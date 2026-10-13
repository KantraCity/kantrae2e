// Touch swipes (iPhone / Android), like Telegram's: back from a chat, reply
// to a message, close the viewer or the menu.
//
// `swipe` is a Svelte action. A touch becomes a swipe once it moves 10 px
// mostly along the wanted axis and direction; from then on the page does
// not scroll and `move` follows the finger, `end` gets the distance and the
// speed (px/ms) on release. Any other movement (scrolling) is left alone.

export type SwipeDir = 'right' | 'left' | 'down' | 'horizontal'

export type SwipeOpts = {
  dir: SwipeDir
  /** Follows the finger: dx (or dy for 'down'), already in the swipe's direction. */
  move?: (d: number) => void
  /** Released: distance and speed (positive = in the swipe's direction). */
  end?: (d: number, v: number) => void
  /** Touches starting inside these elements are ignored. */
  ignore?: string
  /** Off while false. */
  enabled?: boolean
}

const SLOP = 10

export function swipe(node: HTMLElement, opts: SwipeOpts) {
  let o = opts
  let x0 = 0, y0 = 0, state: 'idle' | 'pending' | 'on' | 'off' = 'idle'
  let last = { d: 0, t: 0 }, prev = { d: 0, t: 0 }

  const along = (dx: number, dy: number) => (o.dir === 'down' ? dy : o.dir === 'left' ? -dx : dx)

  function start(e: TouchEvent) {
    if (o.enabled === false || e.touches.length !== 1) {
      state = 'idle'
      return
    }
    const t = e.target as Element | null
    if (o.ignore && t?.closest?.(o.ignore)) {
      state = 'idle'
      return
    }
    x0 = e.touches[0].clientX
    y0 = e.touches[0].clientY
    state = 'pending'
    last = prev = { d: 0, t: e.timeStamp }
  }

  function moveH(e: TouchEvent) {
    if (state !== 'pending' && state !== 'on') return
    const dx = e.touches[0].clientX - x0, dy = e.touches[0].clientY - y0
    if (state === 'pending') {
      if (Math.abs(dx) < SLOP && Math.abs(dy) < SLOP) return
      const horizontal = Math.abs(dx) > Math.abs(dy) * 1.3
      const ok = o.dir === 'down' ? Math.abs(dy) > Math.abs(dx) * 1.3 && dy > 0
        : o.dir === 'horizontal' ? horizontal
        : horizontal && along(dx, dy) > 0
      state = ok ? 'on' : 'off'
      if (!ok) return
      // A long press must not fire under a swipe.
      node.dispatchEvent(new CustomEvent('swipestart', { bubbles: true }))
    }
    e.preventDefault()
    const d = o.dir === 'horizontal' ? dx : along(dx, dy)
    prev = last
    last = { d, t: e.timeStamp }
    o.move?.(o.dir === 'horizontal' ? d : Math.max(0, d))
  }

  function finish() {
    if (state === 'on') {
      const dt = Math.max(1, last.t - prev.t)
      o.end?.(last.d, (last.d - prev.d) / dt)
    }
    state = 'idle'
  }

  node.addEventListener('touchstart', start, { passive: true })
  node.addEventListener('touchmove', moveH, { passive: false })
  node.addEventListener('touchend', finish)
  node.addEventListener('touchcancel', finish)
  return {
    update(n: SwipeOpts) { o = n },
    destroy() {
      node.removeEventListener('touchstart', start)
      node.removeEventListener('touchmove', moveH)
      node.removeEventListener('touchend', finish)
      node.removeEventListener('touchcancel', finish)
    },
  }
}

/** A light tap of the vibration motor where there is one (Android). */
export function tick() {
  try { navigator.vibrate?.(12) } catch { /* not supported */ }
}
