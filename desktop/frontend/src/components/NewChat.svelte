<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import Modal from './Modal.svelte'

  let name = $state('')
  let members = $state('')
  let busy = $state(false)

  async function create(e: SubmitEvent) {
    e.preventDefault()
    if (!name.trim() || busy) return
    busy = true
    const users = members.split(/[\s,]+/).filter(Boolean)
    const id = await app.try(() => Messenger.CreateChat(name, users))
    busy = false
    if (id) {
      app.modal = null
      await app.refreshChats()
      await app.openChat(id)
    }
  }
</script>

<Modal title="Новая группа" onclose={() => (app.modal = null)}>
  <form onsubmit={create}>
    <!-- svelte-ignore a11y_autofocus -->
    <input class="field" placeholder="Название группы" bind:value={name} autofocus />
    <input class="field" placeholder="Участники: имена через пробел или запятую" bind:value={members} />
    <p class="muted hint">Будут добавлены все устройства этих пользователей. Позже можно пригласить ещё.</p>
    <div class="actions">
      <button type="button" class="btn flat" onclick={() => (app.modal = null)}>Отмена</button>
      <button class="btn" disabled={!name.trim() || busy}>{busy ? 'Создание…' : 'Создать'}</button>
    </div>
  </form>
</Modal>

<style>
  form { display: flex; flex-direction: column; gap: 12px; }
  .hint { margin: 0; font-size: 13px; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
