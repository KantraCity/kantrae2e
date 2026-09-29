<script lang="ts">
  import { app } from '../lib/app.svelte'
  import Icon from './Icon.svelte'
</script>

<div class="toasts" aria-live="polite">
  {#each app.toasts as t (t.id)}
    <div class="toast {t.kind}">
      {#if t.kind === 'security' || t.kind === 'warning'}<Icon name="warning" size={18} />{/if}
      <span>{t.text}</span>
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed; left: 50%; bottom: 24px; transform: translateX(-50%);
    z-index: 100; display: flex; flex-direction: column; gap: 8px; align-items: center;
    width: min(560px, calc(100vw - 32px)); pointer-events: none;
  }
  .toast {
    display: flex; gap: 10px; align-items: center;
    background: rgba(35, 46, 60, 0.97); color: var(--text);
    border-radius: 10px; padding: 11px 16px;
    box-shadow: var(--shadow); font-size: 14px; line-height: 1.35;
    animation: up .2s ease-out;
  }
  .toast.error { background: rgba(80, 30, 36, 0.97); }
  .toast.security { background: rgba(120, 24, 32, 0.97); font-weight: 500; }
  .toast.warning { background: rgba(92, 70, 20, 0.97); }
  @keyframes up { from { transform: translateY(10px); opacity: 0 } }
</style>
