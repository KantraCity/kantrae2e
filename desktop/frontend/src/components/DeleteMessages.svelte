<script lang="ts">
  import { app } from '../lib/app.svelte'
  import Modal from './Modal.svelte'

  let { chatId, ids, canAll }: { chatId: string; ids: string[]; canAll: boolean } = $props()
  const chat = $derived(app.chats.find((c) => c.id === chatId))
  let forAll = $state(true)
  let busy = $state(false)

  async function del() {
    busy = true
    // Props come from app.modal, which is cleared below: copy them first.
    const chat = chatId, list = [...ids], all = canAll && forAll
    app.modal = null
    await app.deleteMessages(chat, list, all)
    busy = false
  }
</script>

<Modal title={ids.length > 1 ? 'Удалить сообщения' : 'Удалить сообщение'} onclose={() => (app.modal = null)}>
  <p>Вы уверены, что хотите удалить {ids.length > 1 ? 'эти сообщения' : 'это сообщение'}?</p>
  {#if canAll}
    <label class="check">
      <input type="checkbox" bind:checked={forAll} />
      {chat?.kind === 'direct' ? `Также удалить для ${chat.name}` : 'Удалить у всех'}
    </label>
  {/if}
  <div class="actions">
    <button class="btn flat" onclick={() => (app.modal = null)}>Отмена</button>
    <button class="btn danger" disabled={busy} onclick={del}>Удалить</button>
  </div>
</Modal>

<style>
  p { margin: 0 0 12px; }
  .check { display: flex; gap: 10px; align-items: center; cursor: pointer; margin-bottom: 12px; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
