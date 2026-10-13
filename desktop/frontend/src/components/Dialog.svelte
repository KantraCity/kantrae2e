<script lang="ts">
  // The app's own confirm / prompt box. The system ones (window.confirm,
  // prompt) are never shown by the iPhone webview: they answered "no" at
  // once, so blocking a user, removing a member… silently did nothing.
  import { app } from '../lib/app.svelte'
  import { fade, scale } from 'svelte/transition'

  const d = $derived(app.dialog!)
  let value = $state(app.dialog?.value ?? '')
  let input: HTMLInputElement | undefined = $state()
  $effect(() => { if (input) { input.focus(); input.select() } })

  function done(ok: boolean) {
    const cur = app.dialog
    app.dialog = null
    cur?.resolve(ok ? (cur.kind === 'prompt' ? value : true) : cur.kind === 'prompt' ? null : false)
  }
  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.stopPropagation(); done(false) }
    else if (e.key === 'Enter' && d.kind === 'prompt') { e.preventDefault(); done(true) }
  }
</script>

<svelte:window onkeydown={key} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={() => done(false)} transition:fade={{ duration: 120 }}></div>
<div class="box" role="dialog" aria-modal="true" transition:scale={{ start: 0.94, duration: 140 }}>
  <p>{d.text}</p>
  {#if d.kind === 'prompt'}<input class="field" bind:this={input} bind:value />{/if}
  <div class="actions">
    <button class="btn flat" onclick={() => done(false)}>Отмена</button>
    <button class="btn flat" class:danger={d.danger} onclick={() => done(true)}>{d.ok ?? 'OK'}</button>
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0, 0, 0, .5); z-index: 400; }
  .box { position: fixed; left: 50%; top: 50%; translate: -50% -50%; z-index: 401; width: min(360px, calc(100vw - 32px));
    background: var(--bg-modal); border-radius: 14px; box-shadow: var(--shadow); padding: 20px 20px 10px; }
  p { margin: 0 0 14px; line-height: 1.4; white-space: pre-wrap; }
  .field { margin-bottom: 8px; }
  .actions { display: flex; justify-content: flex-end; gap: 4px; }
  .danger { color: var(--danger); }
</style>
