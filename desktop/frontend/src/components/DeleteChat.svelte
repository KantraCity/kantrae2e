<script lang="ts">
  // Telegram's "Delete chat" / "Clear history" / "Leave group" confirmation.
  import { app } from '../lib/app.svelte'
  import Avatar from './Avatar.svelte'
  import Modal from './Modal.svelte'

  let { chatId, action }: { chatId: string; action: 'delete' | 'clear' | 'leave' } = $props()
  const chat = $derived(app.chats.find((c) => c.id === chatId))
  const direct = $derived(chat?.kind === 'direct')
  let forAll = $state(false)

  const title = $derived(action === 'clear' ? 'Очистить историю' : action === 'leave' ? 'Покинуть группу' : 'Удалить чат')
  const text = $derived(
    action === 'clear' ? `Удалить все сообщения в чате «${chat?.name ?? ''}»?`
    : action === 'leave' ? `Вы уверены, что хотите удалить группу «${chat?.name ?? ''}» и выйти из неё?`
    : `Вы уверены, что хотите удалить чат с ${chat?.name ?? ''}?`)
  const button = $derived(action === 'clear' ? 'Очистить' : action === 'leave' ? 'Выйти' : 'Удалить')

  function go() {
    // Props come from app.modal, which is cleared now: copy them first.
    const id = chatId, act = action, all = direct && forAll && !!chat?.active
    app.modal = null
    app.chatAction(id, act, all)
  }
</script>

<Modal title={title} onclose={() => (app.modal = null)}>
  {#if chat}
    <div class="who">
      <Avatar name={chat.name} id={chat.peerId || chat.id} user={chat.peerId} photo={chat.peerAvatar} size={44} />
      <p>{text}</p>
    </div>
    {#if direct && action !== 'leave' && chat.active}
      <label class="check">
        <input type="checkbox" bind:checked={forAll} />
        Также удалить для {chat.name}
      </label>
    {/if}
    <p class="muted note">
      {#if action === 'leave'}История группы удалится на всех ваших устройствах.{:else if forAll}Сообщения удалятся у вас обоих на всех устройствах.{:else}Удалится на всех ваших устройствах; у собеседника всё останется.{/if}
    </p>
  {/if}
  <div class="actions">
    <button class="btn flat" onclick={() => (app.modal = null)}>Отмена</button>
    <button class="btn danger" onclick={go}>{button}</button>
  </div>
</Modal>

<style>
  .who { display: flex; gap: 14px; align-items: center; margin-bottom: 12px; }
  .who p { margin: 0; line-height: 1.4; }
  .check { display: flex; gap: 10px; align-items: center; cursor: pointer; margin: 4px 0 10px; }
  .note { font-size: 13px; margin: 0 0 14px; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
