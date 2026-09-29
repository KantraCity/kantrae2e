<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import Modal from './Modal.svelte'

  let password = $state('')
  async function submit(e: SubmitEvent) {
    e.preventDefault()
    const ok = await app.try(() => Messenger.Relogin(password))
    if (ok !== undefined) {
      app.modal = null
      app.toast('Сессия обновлена')
    }
  }
</script>

<Modal title="Сессия истекла" onclose={() => (app.modal = null)}>
  <form onsubmit={submit}>
    <p class="muted">Введите пароль, чтобы продолжить на этом устройстве.</p>
    <!-- svelte-ignore a11y_autofocus -->
    <input class="field" type="password" placeholder="Пароль" bind:value={password} autofocus />
    <button class="btn block" disabled={password.length < 8}>Продолжить</button>
  </form>
</Modal>

<style>
  form { display: flex; flex-direction: column; gap: 12px; }
  p { margin: 0; }
</style>
