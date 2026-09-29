<script lang="ts">
  import Icon from './Icon.svelte'
  let { status }: { status: string } = $props()
  const labels: Record<string, string> = { verified: 'проверен', seen: 'не проверен', conflict: 'ключ изменился', self: 'это устройство' }
</script>

<span class="badge {status}">
  {#if status === 'verified'}<Icon name="check" size={13} />{:else if status === 'conflict'}<Icon name="warning" size={13} />{/if}
  {labels[status] ?? status}
</span>

<style>
  .badge { display: inline-flex; align-items: center; gap: 3px; font-size: 12px; padding: 2px 8px; border-radius: 10px; white-space: nowrap;
    background: rgba(112, 132, 153, 0.18); color: var(--text-muted); }
  .verified { background: rgba(77, 205, 94, 0.15); color: var(--success); }
  .conflict { background: rgba(236, 57, 66, 0.18); color: #ff6b73; }
  .self { background: rgba(82, 136, 193, 0.18); color: var(--accent-light); }
</style>
