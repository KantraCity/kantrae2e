<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { fly } from 'svelte/transition'
  import { flip } from 'svelte/animate'
  import Icon from './Icon.svelte'
</script>

<div class="toasts" aria-live="polite">
  {#each app.toasts as t (t.id)}
    <div class="toast {t.kind}" in:fly={{ y: -16, duration: 200 }} out:fly={{ y: -8, duration: 160 }} animate:flip={{ duration: 180 }}>
      {#if t.kind === 'security' || t.kind === 'warning'}<Icon name="warning" size={18} />{/if}
      <span>{t.text}</span>
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed; left: 50%; top: 64px; transform: translateX(-50%);
    z-index: 100; display: flex; flex-direction: column; gap: 8px; align-items: center;
    width: min(560px, calc(100vw - 32px)); pointer-events: none;
  }
  .toast {
    display: flex; gap: 10px; align-items: center;
    background: rgba(35, 46, 60, 0.97); color: var(--text);
    border-radius: 10px; padding: 11px 16px;
    box-shadow: var(--shadow); font-size: 14px; line-height: 1.35;
  }
  .toast.error { background: rgba(80, 30, 36, 0.97); }
  .toast.security { background: rgba(120, 24, 32, 0.97); font-weight: 500; }
  .toast.warning { background: rgba(92, 70, 20, 0.97); }
  @keyframes up { from { transform: translateY(10px); opacity: 0 } }
</style>
