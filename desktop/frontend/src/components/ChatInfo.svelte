<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, type Member } from '../lib/api'
  import { plural } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'
  import StatusBadge from './StatusBadge.svelte'

  let { chatId }: { chatId: string } = $props()
  const chat = $derived(app.chats.find((c) => c.id === chatId))
  let members = $state<Member[]>([])
  let invite = $state('')
  let busy = $state(false)

  // One row per user: worst status of its devices wins.
  const rank: Record<string, number> = { conflict: 0, seen: 1, verified: 2, self: 3 }
  const users = $derived.by(() => {
    const byUser = new Map<string, { username: string; userId: string; devices: number; status: string; me: boolean }>()
    for (const m of members) {
      const u = byUser.get(m.userId) ?? { username: m.username, userId: m.userId, devices: 0, status: m.status, me: false }
      u.devices++
      if (m.status === 'self') u.me = true
      else if (u.status === 'self' || rank[m.status] < rank[u.status]) u.status = m.status
      byUser.set(m.userId, u)
    }
    return [...byUser.values()].sort((a, b) => Number(b.me) - Number(a.me) || a.username.localeCompare(b.username))
  })

  async function load() {
    if (chat?.active) members = (await app.try(() => Messenger.Members(chatId))) ?? []
  }
  $effect(() => { load() })

  async function add(e: SubmitEvent) {
    e.preventDefault()
    const list = invite.split(/[\s,]+/).filter(Boolean)
    if (!list.length || busy) return
    busy = true
    const ok = await app.try(() => Messenger.Invite(chatId, list))
    busy = false
    if (ok !== undefined) {
      invite = ''
      app.toast('Участники добавлены')
      await load()
    }
  }

  async function remove(username: string) {
    if (!confirm(`Удалить ${username} из группы?`)) return
    const ok = await app.try(() => Messenger.Remove(chatId, username))
    if (ok !== undefined) await load()
  }
</script>

<Modal title="Информация о группе" onclose={() => (app.modal = null)}>
  {#if chat}
    <div class="head">
      <Avatar name={chat.name} id={chat.id} size={72} />
      <div>
        <div class="name">{chat.name}</div>
        <div class="muted">{chat.active ? `${users.length} ${plural(users.length, 'участник', 'участника', 'участников')}` : 'вы не участник'}</div>
      </div>
    </div>

    {#if chat.active}
      <form class="invite" onsubmit={add}>
        <input class="field" placeholder="Пригласить: имя пользователя" bind:value={invite} />
        <button class="btn" disabled={busy || !invite.trim()} aria-label="Пригласить"><Icon name="plus" size={18} /></button>
      </form>

      <div class="section muted">Участники · нажмите, чтобы сверить ключи</div>
      <div class="members">
        {#each users as u (u.userId)}
          <div class="member">
            <button class="who" onclick={() => (app.modal = { kind: 'verify', username: u.username })}>
              <Avatar name={u.username} id={u.userId} size={40} />
              <div class="who-text">
                <div class="uname">{u.username}{u.me ? ' (вы)' : ''}</div>
                <div class="muted small">{u.devices} {plural(u.devices, 'устройство', 'устройства', 'устройств')}</div>
              </div>
            </button>
            <StatusBadge status={u.me ? 'self' : u.status} />
            {#if !u.me}
              <button class="icon-btn" title="Удалить из группы" aria-label="Удалить" onclick={() => remove(u.username)}><Icon name="close" size={18} /></button>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  {/if}
</Modal>

<style>
  .head { display: flex; gap: 16px; align-items: center; margin: 4px 0 18px; }
  .name { font-size: 18px; font-weight: 600; }
  .invite { display: flex; gap: 8px; margin-bottom: 16px; }
  .invite .btn { padding: 0 14px; }
  .section { font-size: 13px; margin-bottom: 6px; }
  .members { display: flex; flex-direction: column; }
  .member { display: flex; align-items: center; gap: 8px; padding: 4px 0; }
  .who { flex: 1; display: flex; gap: 12px; align-items: center; text-align: left; padding: 6px; border-radius: 10px; min-width: 0; }
  .who:hover { background: var(--bg-hover); }
  .who-text { min-width: 0; }
  .uname { font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .small { font-size: 13px; }
</style>
