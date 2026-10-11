<script lang="ts">
  import { app } from '../lib/app.svelte'
  import Modal from './Modal.svelte'
  import UserSearch from './UserSearch.svelte'

  async function pick(username: string) {
    app.modal = null
    await app.openDirect(username)
  }
</script>

<Modal title="Новый чат" onclose={() => (app.modal = null)}>
  <UserSearch onpick={(u) => pick(u.username)} placeholder="Кому написать? Введите ник" />
</Modal>
