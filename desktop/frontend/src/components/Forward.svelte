<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import Avatar from './Avatar.svelte'
  import Modal from './Modal.svelte'

  let { chatId, ids }: { chatId: string; ids: string[] } = $props()
  let q = $state('')
  let busy = $state(false)
  const targets = $derived(app.chats.filter((c) => c.active && c.name.toLowerCase().includes(q.trim().toLowerCase())))

  async function forward(to: string) {
    if (busy) return
    busy = true
    const from = chatId, list = [...ids]
    const ok = await app.try(() => Messenger.Forward(from, list, [to]))
    busy = false
    if (ok !== undefined) {
      app.modal = null
      await app.refreshChats()
      await app.openChat(to)
    }
  }
</script>

<Modal title="Переслать" onclose={() => (app.modal = null)}>
  <!-- svelte-ignore a11y_autofocus -->
  <input class="field" placeholder="Поиск чата" bind:value={q} autofocus />
  <div class="list">
    {#each targets as c (c.id)}
      <button class="item" disabled={busy} onclick={() => forward(c.id)}>
        <Avatar name={c.name} id={c.peerId || c.id} user={c.peerId} photo={c.peerAvatar} size={42} />
        <span>{c.name}</span>
      </button>
    {:else}
      <p class="muted">Нет чатов</p>
    {/each}
  </div>
</Modal>

<style>
  .list { display: flex; flex-direction: column; margin-top: 8px; max-height: 50vh; overflow-y: auto; }
  .item { display: flex; align-items: center; gap: 12px; padding: 7px 8px; border-radius: 10px; text-align: left; font-weight: 500; }
  .item:hover { background: var(--bg-hover); }
</style>
