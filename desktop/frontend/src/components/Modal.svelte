<script lang="ts">
  import type { Snippet } from 'svelte'
  import Icon from './Icon.svelte'

  let { title, onclose, children, width = 420 }: {
    title: string
    onclose: () => void
    children: Snippet
    width?: number
  } = $props()

  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') onclose()
  }
</script>

<svelte:window onkeydown={key} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={(e) => e.target === e.currentTarget && onclose()}>
  <div class="modal" style="max-width:{width}px" role="dialog" aria-label={title}>
    <header>
      <h2>{title}</h2>
      <button class="icon-btn" onclick={onclose} aria-label="Закрыть"><Icon name="close" size={20} /></button>
    </header>
    <div class="body">{@render children()}</div>
  </div>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; z-index: 50;
    background: rgba(0, 0, 0, 0.55);
    display: flex; align-items: center; justify-content: center;
    padding: 16px;
    animation: fade .15s ease-out;
  }
  .modal {
    width: 100%;
    max-height: calc(100vh - 32px);
    display: flex; flex-direction: column;
    background: var(--bg-modal);
    border-radius: 14px;
    box-shadow: var(--shadow);
    animation: pop .18s ease-out;
  }
  header { display: flex; align-items: center; justify-content: space-between; padding: 14px 12px 6px 22px; }
  h2 { font-size: 18px; font-weight: 600; margin: 0; }
  .body { padding: 6px 22px 20px; overflow-y: auto; }
  @keyframes fade { from { opacity: 0 } }
  @keyframes pop { from { transform: scale(.96); opacity: 0 } }
</style>
