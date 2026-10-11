<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import { plural } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'
  import UserSearch from './UserSearch.svelte'

  // Like Telegram: first pick members, then name the group.
  let step = $state<'members' | 'name'>('members')
  let members = $state<{ username: string; userId: string }[]>([])
  let name = $state('')
  let busy = $state(false)

  function toggle(u: { username: string; userId: string }) {
    members = members.some((m) => m.userId === u.userId) ? members.filter((m) => m.userId !== u.userId) : [...members, u]
  }

  async function create(e: SubmitEvent) {
    e.preventDefault()
    if (!name.trim() || busy) return
    busy = true
    const id = await app.try(() => Messenger.CreateGroup(name, members.map((m) => m.username)))
    busy = false
    if (id) {
      app.modal = null
      await app.refreshChats()
      await app.openChat(id)
    }
  }
</script>

<Modal title={step === 'members' ? 'Добавить участников' : 'Новая группа'} onclose={() => (app.modal = null)}>
  {#if step === 'members'}
    {#if members.length}
      <div class="chips">
        {#each members as m (m.userId)}
          <button class="chip" onclick={() => toggle(m)} title="Убрать">
            <Avatar name={m.username} id={m.userId} size={24} />{m.username}<Icon name="close" size={14} />
          </button>
        {/each}
      </div>
    {/if}
    <UserSearch onpick={toggle} selected={members.map((m) => m.username)} placeholder="Кого добавить?" />
    <div class="actions">
      <span class="muted small">{members.length ? `${members.length} ${plural(members.length, 'участник', 'участника', 'участников')}` : 'можно добавить позже'}</span>
      <button class="btn" onclick={() => (step = 'name')}>Далее</button>
    </div>
  {:else}
    <form onsubmit={create}>
      <div class="name-row">
        <Avatar name={name || '?'} id={name} size={64} />
        <!-- svelte-ignore a11y_autofocus -->
        <input class="field" placeholder="Название группы" bind:value={name} autofocus />
      </div>
      <p class="muted small">Участники: {members.length ? members.map((m) => m.username).join(', ') : 'только вы'}</p>
      <div class="actions">
        <button type="button" class="btn flat" onclick={() => (step = 'members')}>Назад</button>
        <button class="btn" disabled={!name.trim() || busy}>{busy ? 'Создание…' : 'Создать'}</button>
      </div>
    </form>
  {/if}
</Modal>

<style>
  form { display: flex; flex-direction: column; gap: 12px; }
  .chips { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 10px; }
  .chip { display: inline-flex; align-items: center; gap: 6px; background: var(--bg-active); border-radius: 16px; padding: 2px 10px 2px 2px; font-size: 14px; }
  .name-row { display: flex; gap: 14px; align-items: center; }
  .name-row .field { flex: 1; }
  .small { font-size: 13px; margin: 0; }
  .actions { display: flex; justify-content: space-between; align-items: center; gap: 8px; margin-top: 10px; }
  form .actions { justify-content: flex-end; }
</style>
