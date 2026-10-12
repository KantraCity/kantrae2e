<script lang="ts">
  // A poll like Telegram's: tap an option to vote (several, if allowed);
  // after voting the results are shown with bars; tap again to change.
  import type { Message } from '../lib/api'
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import Icon from './Icon.svelte'

  let { m }: { m: Message } = $props()
  const p = $derived(m.poll!)
  let picked = $state<number[]>([])
  let busy = $state(false)
  const showResults = $derived(p.voted)
  const pct = (n: number) => (p.total ? Math.round((n * 100) / p.total) : 0)

  async function vote(options: number[]) {
    if (busy) return
    busy = true
    const ok = await app.try(() => Messenger.Vote(m.chatId, m.id, options))
    busy = false
    picked = []
    if (ok !== undefined) await app.refreshMessage(m.chatId, m.id)
  }
  function choose(i: number) {
    if (!p.multiple) { vote([i]); return }
    picked = picked.includes(i) ? picked.filter((x) => x !== i) : [...picked, i]
  }
</script>

<div class="poll">
  <div class="q">{p.question}</div>
  <div class="kind">{p.anonymous ? 'Анонимный опрос' : 'Опрос'}{p.multiple ? ' · несколько ответов' : ''}</div>
  {#each p.options as o, i}
    {#if showResults}
      <div class="res" class:mine={o.mine} title={o.voters?.join(', ') ?? ''}>
        <span class="pc">{pct(o.votes)}%</span>
        <div class="rmain">
          <span class="otext">{o.text}{#if o.mine} <Icon name="check" size={14} />{/if}</span>
          <span class="bar"><span style="width:{Math.max(2, pct(o.votes))}%"></span></span>
          {#if !p.anonymous && o.voters?.length}<span class="voters">{o.voters.join(', ')}</span>{/if}
        </div>
      </div>
    {:else}
      <button class="opt" disabled={busy} onclick={() => choose(i)}>
        <span class="radio" class:square={p.multiple} class:on={picked.includes(i)}></span>{o.text}
      </button>
    {/if}
  {/each}
  <div class="foot">
    <span class="total">{p.total ? `${p.total} проголосовал${p.total === 1 ? '' : 'и'}` : 'Пока нет голосов'}</span>
    {#if !showResults && p.multiple}
      <button class="act" disabled={!picked.length || busy} onclick={() => vote(picked)}>Голосовать</button>
    {:else if showResults}
      <button class="act" disabled={busy} onclick={() => vote([])}>Отменить голос</button>
    {/if}
  </div>
</div>

<style>
  .poll { min-width: 240px; max-width: 340px; display: flex; flex-direction: column; gap: 6px; padding: 2px 0; }
  .q { font-weight: 600; font-size: 15px; }
  .kind { font-size: 12px; opacity: .7; margin-bottom: 4px; }
  .opt { display: flex; align-items: center; gap: 10px; text-align: left; padding: 6px 4px; border-radius: 8px; font-size: 14px; }
  .opt:hover { background: var(--tint-1); }
  .radio { width: 18px; height: 18px; border-radius: 50%; border: 2px solid currentColor; opacity: .7; flex-shrink: 0; }
  .radio.square { border-radius: 5px; }
  .radio.on { background: var(--accent-light); border-color: var(--accent-light); opacity: 1; }
  .res { display: flex; gap: 10px; align-items: flex-start; font-size: 14px; }
  .pc { width: 38px; text-align: right; font-weight: 600; flex-shrink: 0; }
  .rmain { flex: 1; display: flex; flex-direction: column; gap: 4px; min-width: 0; }
  .otext { display: inline-flex; align-items: center; gap: 4px; }
  .bar { height: 4px; border-radius: 2px; background: var(--tint-2); overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--accent-light); border-radius: 2px; transition: width .4s; }
  :global(.out) .bar span { background: var(--out-accent); }
  .res.mine .pc { color: var(--accent-light); }
  :global(.out) .res.mine .pc { color: var(--out-accent); }
  .voters { font-size: 12px; opacity: .65; }
  .foot { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 4px; font-size: 12px; opacity: .85; }
  .act { color: var(--accent-light); font-weight: 600; font-size: 13px; padding: 4px 8px; border-radius: 8px; }
  :global(.out) .act { color: var(--out-accent); }
  .act:disabled { opacity: .5; }
</style>
