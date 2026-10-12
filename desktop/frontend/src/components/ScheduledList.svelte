<script lang="ts">
  // Scheduled messages of a chat: send now or cancel.
  import { app } from '../lib/app.svelte'
  import { Messenger, type ScheduledView } from '../lib/api'
  import { plain } from '../lib/format'
  import Modal from './Modal.svelte'
  import Icon from './Icon.svelte'

  let { chatId }: { chatId: string } = $props()
  let list = $state<ScheduledView[] | null>(null)

  $effect(() => {
    void app.scheduledTick
    Messenger.Scheduled(chatId).then((l) => (list = l ?? [])).catch(() => (list = []))
  })
  $effect(() => { if (list && !list.length) app.modal = null })

  async function now(s: ScheduledView) {
    if ((await app.try(() => Messenger.SendScheduledNow(s.id))) !== undefined) app.scheduledTick++
  }
  async function cancel(s: ScheduledView) {
    if ((await app.try(() => Messenger.CancelScheduled(s.id))) !== undefined) app.scheduledTick++
  }
</script>

<Modal title="Отложенные сообщения" onclose={() => (app.modal = null)}>
  {#if !list}<p class="muted">Загрузка…</p>{/if}
  {#each list ?? [] as s (s.id)}
    <div class="item">
      <div class="when">
        <Icon name="clock" size={14} />
        {new Date(s.at).toLocaleString('ru', { day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' })}
        {#if s.silent}<span class="muted">· без звука</span>{/if}
      </div>
      <div class="text">{plain(s.text)}</div>
      <div class="btns">
        <button class="link" onclick={() => now(s)}>Отправить сейчас</button>
        <button class="link danger" onclick={() => cancel(s)}>Удалить</button>
      </div>
    </div>
  {/each}
</Modal>

<style>
  .item { padding: 10px 0; border-bottom: 1px solid var(--border); }
  .item:last-child { border-bottom: 0; }
  .when { display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--accent-light); }
  .text { margin: 4px 0 6px; white-space: pre-wrap; word-break: break-word; max-height: 120px; overflow: hidden; }
  .btns { display: flex; gap: 16px; }
  .link { color: var(--accent-light); font-size: 14px; font-weight: 500; }
  .danger { color: #e17076; }
</style>
