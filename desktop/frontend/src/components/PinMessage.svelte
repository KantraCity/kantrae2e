<script lang="ts">
  // Pin / unpin a message: for everybody (any member may, as chosen) or only for me.
  import type { Message } from '../lib/api'
  import { app } from '../lib/app.svelte'
  import Modal from './Modal.svelte'

  let { m }: { m: Message } = $props()
  const chat = $derived(app.chats.find((c) => c.id === m.chatId))
  // svelte-ignore state_referenced_locally
  const unpin = m.pinned
  let forAll = $state(true)

  function go() {
    const msg = m, all = forAll && !!chat?.active
    app.modal = null
    app.pin(msg, !unpin, all)
  }
</script>

<Modal title={unpin ? 'Открепить сообщение' : 'Закрепить сообщение'} onclose={() => (app.modal = null)}>
  <p>{unpin ? 'Открепить это сообщение?' : 'Закрепить это сообщение вверху чата?'}</p>
  {#if chat?.active}
    <label class="check">
      <input type="checkbox" bind:checked={forAll} />
      {chat.kind === 'direct' ? `Также ${unpin ? 'открепить' : 'закрепить'} для ${chat.name}` : `${unpin ? 'Открепить' : 'Закрепить'} для всех участников`}
    </label>
  {/if}
  <div class="actions">
    <button class="btn flat" onclick={() => (app.modal = null)}>Отмена</button>
    <button class="btn" onclick={go}>{unpin ? 'Открепить' : 'Закрепить'}</button>
  </div>
</Modal>

<style>
  p { margin: 0 0 12px; }
  .check { display: flex; gap: 10px; align-items: center; cursor: pointer; margin-bottom: 12px; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
