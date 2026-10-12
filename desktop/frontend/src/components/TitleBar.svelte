<script lang="ts">
  // The window's title bar, drawn by the app: dark like the rest of the UI.
  // Windows: our own minimise / maximise / close buttons (frameless window).
  // macOS: the native traffic lights sit over the left end of the bar.
  // Dragging the bar moves the window; double-click maximises.
  import { Window } from '@wailsio/runtime'
  import { app } from '../lib/app.svelte'

  let { style }: { style: 'custom' | 'mac' } = $props()
  let maximised = $state(false)

  async function sync() {
    maximised = await Window.IsMaximised().catch(() => false)
  }
  $effect(() => {
    sync()
    const onResize = () => sync()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  })

  const title = $derived(app.current?.name ? `${app.current.name} — Kantra` : 'Kantra')
  async function toggle() {
    await Window.ToggleMaximise().catch(() => {})
    sync()
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="titlebar {style}" ondblclick={toggle}>
  {#if style === 'custom'}<img class="logo" src="/icon.svg" alt="" />{/if}
  <span class="title">{title}</span>
  {#if style === 'custom'}
    <div class="buttons" ondblclick={(e) => e.stopPropagation()}>
      <button aria-label="Свернуть" title="Свернуть" onclick={() => Window.Minimise()}>
        <svg viewBox="0 0 10 10"><path d="M0 5h10" /></svg>
      </button>
      <button aria-label={maximised ? 'Восстановить' : 'Развернуть'} title={maximised ? 'Восстановить' : 'Развернуть'} onclick={toggle}>
        {#if maximised}
          <svg viewBox="0 0 10 10"><path d="M2.5 2.5V.5h7v7h-2M.5 2.5h7v7h-7z" /></svg>
        {:else}
          <svg viewBox="0 0 10 10"><path d="M.5.5h9v9h-9z" /></svg>
        {/if}
      </button>
      <button class="close" aria-label="Закрыть" title="Закрыть" onclick={() => Window.Close()}>
        <svg viewBox="0 0 10 10"><path d="M0 0l10 10M10 0L0 10" /></svg>
      </button>
    </div>
  {/if}
</div>

<style>
  .titlebar {
    --wails-draggable: drag;
    flex-shrink: 0; height: 32px; display: flex; align-items: center; gap: 8px;
    background: var(--bg-titlebar); color: var(--text-muted); font-size: 12px; user-select: none; -webkit-user-select: none;
    position: relative; z-index: 300;
  }
  .titlebar.mac { height: 28px; justify-content: center; padding: 0 80px; }
  .logo { width: 16px; height: 16px; margin-left: 12px; }
  .title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; min-width: 0; }
  .mac .title { flex: 0 1 auto; text-align: center; }
  .buttons { --wails-draggable: no-drag; display: flex; height: 100%; margin-left: auto; }
  .buttons button { width: 46px; height: 100%; display: flex; align-items: center; justify-content: center; color: var(--text);
    transition: background .12s, color .12s; }
  .buttons button:hover { background: rgba(255, 255, 255, .08); }
  .buttons button:active { background: rgba(255, 255, 255, .14); }
  .buttons .close:hover { background: #e81123; color: #fff; }
  .buttons .close:active { background: #f1707a; }
  svg { width: 10px; height: 10px; fill: none; stroke: currentColor; stroke-width: 1; shape-rendering: crispEdges; }
  .close svg { shape-rendering: geometricPrecision; }
</style>
