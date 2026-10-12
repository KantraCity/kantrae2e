<script lang="ts">
  // Telegram-like emoji keyboard: category tabs, search (Russian/English),
  // recently used, skin tones (long press / right click), and a GIF tab.
  // mode "compose" sits under/above the message field; mode "reaction" is
  // the full reaction picker of the message menu.
  import { onMount, tick } from 'svelte'
  import { GROUPS, TONES, emojiPrefs, loadEmoji, searchEmoji, type Emoji } from '../lib/emoji.svelte'
  import GifGrid from './GifGrid.svelte'
  import Icon from './Icon.svelte'

  let { onpick, onclose, mode = 'compose', ongif }: {
    onpick: (e: string) => void
    onclose?: () => void
    mode?: 'compose' | 'reaction'
    ongif?: () => void
  } = $props()

  let all = $state<Emoji[]>([])
  let tab = $state<'emoji' | 'sticker' | 'gif'>('emoji')
  let query = $state('')
  let scroller: HTMLDivElement | undefined = $state()
  let active = $state(-1) // -1 = recent
  let skinFor = $state<{ em: Emoji; x: number; y: number } | null>(null)

  onMount(() => { loadEmoji().then((l) => (all = l)) })

  const byGroup = $derived(GROUPS.map((g) => ({ ...g, items: all.filter((e) => e.g === g.id) })))
  const found = $derived(query.trim() ? searchEmoji(all, query) : [])
  const recent = $derived(emojiPrefs.recent)
  const findEmoji = (s: string) => all.find((e) => e.e === s || e.skins?.includes(s))

  function pick(s: string) {
    emojiPrefs.use(s)
    onpick(s)
  }
  function pickEmoji(em: Emoji) {
    pick(emojiPrefs.withTone(em))
  }

  // Skin tone popup.
  let pressTimer: ReturnType<typeof setTimeout> | undefined
  let longPressed = false
  function pressStart(e: PointerEvent, em: Emoji) {
    longPressed = false
    if (!em.skins || e.pointerType === 'mouse') return
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    pressTimer = setTimeout(() => { longPressed = true; navigator.vibrate?.(12); skinFor = { em, x: r.left + r.width / 2, y: r.top } }, 380)
  }
  function pressEnd() { clearTimeout(pressTimer) }
  function click(em: Emoji) {
    if (longPressed) { longPressed = false; return }
    pickEmoji(em)
  }
  function context(e: MouseEvent, em: Emoji) {
    if (!em.skins) return
    e.preventDefault()
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    skinFor = { em, x: r.left + r.width / 2, y: r.top }
  }
  function chooseTone(i: number) {
    if (!skinFor) return
    const em = skinFor.em
    emojiPrefs.setTone(em.e, i)
    skinFor = null
    pick(i ? em.skins![i - 1] : em.e)
  }

  // Category tabs follow the scroll; a tap scrolls to the category.
  function onScroll() {
    if (!scroller) return
    const top = scroller.scrollTop + 8
    let cur = -1
    for (const s of scroller.querySelectorAll<HTMLElement>('[data-group]')) {
      if (s.offsetTop <= top) cur = Number(s.dataset.group)
    }
    active = cur
  }
  async function jump(id: number) {
    query = ''
    tab = 'emoji'
    await tick()
    const el = scroller?.querySelector<HTMLElement>(`[data-group="${id}"]`)
    if (el && scroller) scroller.scrollTo({ top: id === -1 ? 0 : el.offsetTop - 4, behavior: 'smooth' })
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && (skinFor ? (skinFor = null) : onclose?.())} />

<div class="panel" class:reaction={mode === 'reaction'}>
  {#if tab === 'emoji'}
    <div class="search">
      <Icon name="search" size={16} />
      <input placeholder="Поиск эмодзи" bind:value={query} autocomplete="off" spellcheck="false" />
      {#if query}<button class="clear" aria-label="Очистить" onclick={() => (query = '')}><Icon name="close" size={14} /></button>{/if}
    </div>
    <div class="body" bind:this={scroller} onscroll={onScroll}>
      {#if query.trim()}
        <div class="grid">
          {#each found as em (em.e)}
            <button class="em" title={em.words.split(' ')[0]} onclick={() => click(em)} oncontextmenu={(e) => context(e, em)}
              onpointerdown={(e) => pressStart(e, em)} onpointerup={pressEnd} onpointerleave={pressEnd}>{emojiPrefs.withTone(em)}</button>
          {:else}
            <div class="empty muted">{all.length ? 'Ничего не найдено' : 'Загрузка…'}</div>
          {/each}
        </div>
      {:else}
        <section data-group="-1">
          <div class="cat">Недавние</div>
          <div class="grid">
            {#each recent as s (s)}
              {@const em = findEmoji(s)}
              <button class="em" onclick={() => pick(s)} oncontextmenu={(e) => em && context(e, em)}>{s}</button>
            {/each}
          </div>
        </section>
        {#each byGroup as g (g.id)}
          <section data-group={g.id}>
            <div class="cat">{g.name}</div>
            <div class="grid">
              {#each g.items as em (em.e)}
                <button class="em" onclick={() => click(em)} oncontextmenu={(e) => context(e, em)}
                  onpointerdown={(e) => pressStart(e, em)} onpointerup={pressEnd} onpointerleave={pressEnd}>{emojiPrefs.withTone(em)}</button>
              {/each}
            </div>
          </section>
        {/each}
      {/if}
    </div>
    <nav class="tabs">
      <button class:on={active === -1 && !query} aria-label="Недавние" onclick={() => jump(-1)}><Icon name="clock" size={20} /></button>
      {#each GROUPS as g (g.id)}
        <button class:on={active === g.id && !query} aria-label={g.name} title={g.name} onclick={() => jump(g.id)}>{g.icon}</button>
      {/each}
    </nav>
  {:else}
    {#key tab}<GifGrid kind={tab} onpick={() => ongif?.()} />{/key}
  {/if}
  {#if mode === 'compose'}
    <div class="switch">
      <button class:on={tab === 'emoji'} onclick={() => (tab = 'emoji')}>Эмодзи</button>
      <button class:on={tab === 'sticker'} onclick={() => (tab = 'sticker')}>Стикеры</button>
      <button class:on={tab === 'gif'} onclick={() => (tab = 'gif')}>GIF</button>
    </div>
  {/if}
</div>

{#if skinFor}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="skin-backdrop" onclick={() => (skinFor = null)}></div>
  <div class="skins" style="left:{Math.max(150, Math.min(skinFor.x, innerWidth - 150))}px;top:{skinFor.y}px">
    {#each TONES as _t, i}
      <button class="em" class:cur={(emojiPrefs.tones[skinFor.em.e] ?? 0) === i} onclick={() => chooseTone(i)}>{i ? skinFor.em.skins![i - 1] : skinFor.em.e}</button>
    {/each}
  </div>
{/if}

<style>
  .panel { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--bg-menu); }
  .search { display: flex; align-items: center; gap: 8px; margin: 8px 10px 4px; padding: 0 10px; height: 34px; border-radius: 17px;
    background: var(--bg-input); color: var(--text-muted); flex-shrink: 0; }
  .search input { flex: 1; min-width: 0; border: 0; outline: 0; background: transparent; }
  .clear { color: var(--text-muted); display: inline-flex; }
  .body { flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 0 6px 6px; }
  section { content-visibility: auto; contain-intrinsic-size: auto 300px; }
  .cat { position: sticky; top: 0; z-index: 1; padding: 8px 6px 4px; font-size: 13px; font-weight: 600; color: var(--text-muted);
    background: var(--bg-menu); }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(40px, 1fr)); }
  .em { height: 40px; font-size: 26px; line-height: 1; border-radius: 10px; display: flex; align-items: center; justify-content: center;
    font-family: "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", "Twemoji Mozilla", sans-serif;
    transition: background .1s, transform .1s; -webkit-user-select: none; user-select: none; -webkit-touch-callout: none; }
  .em:hover { background: var(--bg-hover); }
  .em:active { transform: scale(.88); }
  .empty { grid-column: 1 / -1; text-align: center; padding: 24px; }
  .tabs { display: flex; justify-content: space-around; border-top: 1px solid var(--border); flex-shrink: 0; padding: 2px 4px; }
  .tabs button { flex: 1; height: 36px; font-size: 18px; border-radius: 8px; color: var(--text-muted); display: flex; align-items: center; justify-content: center;
    filter: grayscale(1) opacity(.6); transition: filter .15s, background .15s; }
  .tabs button.on, .tabs button:hover { filter: none; background: var(--bg-hover); }
  .switch { display: flex; justify-content: center; gap: 6px; padding: 4px 0 6px; flex-shrink: 0; }
  .switch button { padding: 4px 14px; border-radius: 14px; font-size: 14px; font-weight: 500; color: var(--text-muted); }
  .switch button.on { background: var(--bg-hover); color: var(--accent-light); }
  .skin-backdrop { position: fixed; inset: 0; z-index: 300; }
  .skins { position: fixed; z-index: 301; transform: translate(-50%, calc(-100% - 6px)); display: flex; gap: 2px; padding: 4px;
    background: var(--bg-menu); border-radius: 14px; box-shadow: var(--shadow); }
  .skins .em { width: 42px; }
  .skins .em.cur { background: var(--bg-active); }
</style>
