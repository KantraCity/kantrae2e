<script lang="ts">
  // Formatted message text (lib/format.ts): bold, italic, strikethrough,
  // code, spoilers (hidden until tapped), links and @mentions.
  import { parse, type Tok } from '../lib/format'
  import { app } from '../lib/app.svelte'
  import RichText from './RichText.svelte'

  let { text, toks }: { text?: string; toks?: Tok[] } = $props()
  const list = $derived(toks ?? parse(text ?? ''))

  let revealed = $state<Record<number, boolean>>({})

  async function openLink(e: MouseEvent, href: string) {
    // Invite links join the group inside the app.
    if (/\/join\/[^#\s]+#\S+/.test(href)) {
      e.preventDefault()
      app.joinByLink(href)
    }
  }
  function mention(e: MouseEvent, v: string) {
    e.stopPropagation()
    app.openDirect(v.slice(1))
  }
</script>

{#each list as t, i}{#if t.t === 'text'}{t.v}{:else if t.t === 'b'}<b><RichText toks={t.c} /></b>{:else if t.t === 'i'}<i><RichText toks={t.c} /></i>{:else if t.t === 's'}<s><RichText toks={t.c} /></s>{:else if t.t === 'code'}<code class="code">{t.v}</code>{:else if t.t === 'pre'}<pre class="pre">{t.v}</pre>{:else if t.t === 'link'}<a href={t.href} target="_blank" rel="noopener noreferrer" onclick={(e) => openLink(e, t.href)}>{t.v}</a>{:else if t.t === 'mention'}<button class="mention" onclick={(e) => mention(e, t.v)}>{t.v}</button>{:else if t.t === 'spoiler'}<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions --><span class="spoiler" class:shown={revealed[i]} onclick={(e) => { e.stopPropagation(); revealed[i] = true }}><RichText toks={t.c} /></span>{/if}{/each}

<style>
  .code { font-family: ui-monospace, "Cascadia Code", Consolas, monospace; font-size: .92em; background: rgba(0,0,0,.22); padding: 1px 5px; border-radius: 5px; }
  .pre { font-family: ui-monospace, "Cascadia Code", Consolas, monospace; font-size: .9em; background: rgba(0,0,0,.25); padding: 8px 10px; border-radius: 8px;
    margin: 4px 0; white-space: pre-wrap; overflow-x: auto; border-left: 3px solid var(--accent-light); }
  a { color: var(--accent-light); text-decoration: none; word-break: break-word; }
  a:hover { text-decoration: underline; }
  :global(.out) a { color: var(--out-accent); text-decoration: underline; }
  .mention { color: var(--accent-light); font: inherit; padding: 0; display: inline; }
  :global(.out) .mention { color: var(--out-accent); font-weight: 500; }
  .spoiler { background: var(--tint-3); color: transparent; border-radius: 4px; cursor: pointer; filter: blur(2px);
    transition: filter .25s, color .25s, background .25s; user-select: none; }
  .spoiler :global(*) { color: transparent !important; }
  .spoiler.shown { background: transparent; color: inherit; filter: none; user-select: inherit; -webkit-user-select: inherit; cursor: inherit; }
  .spoiler.shown :global(*) { color: inherit !important; }
</style>
