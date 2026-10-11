<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, type Member, type UserProfile } from '../lib/api'
  import { plural } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'
  import StatusBadge from './StatusBadge.svelte'

  let { chatId }: { chatId: string } = $props()
  const chat = $derived(app.chats.find((c) => c.id === chatId))
  let members = $state<Member[]>([])
  let profile = $state<UserProfile | null>(null)
  $effect(() => {
    if (chat?.kind === 'direct' && chat.peerId) Messenger.UserProfile(chat.peerId).then((p) => {
      profile = p
      // The name/photo may have changed: update the list and the header too.
      if (p && (p.name !== chat.name || (p.avatarId ?? '') !== (chat.peerAvatar ?? ''))) app.refreshChats()
    }).catch(() => {})
  })
  let invite = $state('')
  let busy = $state(false)

  // One row per user: worst status of its devices wins.
  const rank: Record<string, number> = { conflict: 0, seen: 1, verified: 2, self: 3 }
  const users = $derived.by(() => {
    const byUser = new Map<string, { username: string; name: string; avatarId?: string; userId: string; devices: number; status: string; me: boolean }>()
    for (const m of members) {
      const u = byUser.get(m.userId) ?? { username: m.username, name: m.name, avatarId: m.avatarId, userId: m.userId, devices: 0, status: m.status, me: false }
      u.devices++
      if (m.status === 'self') u.me = true
      else if (u.status === 'self' || rank[m.status] < rank[u.status]) u.status = m.status
      byUser.set(m.userId, u)
    }
    return [...byUser.values()].sort((a, b) => Number(b.me) - Number(a.me) || a.name.localeCompare(b.name))
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

<Modal title={chat?.kind === 'direct' ? 'Информация' : 'Информация о группе'} onclose={() => (app.modal = null)}>
  {#if chat && chat.kind === 'direct'}
    {@const peer = users.find((u) => !u.me)}
    <div class="head">
      <Avatar name={chat.name} id={chat.peerId || chat.id} user={chat.peerId} photo={profile?.avatarId ?? chat.peerAvatar} size={96} />
      <div>
        <div class="name">{profile?.name ?? chat.name}</div>
        <div class="muted">личный чат · сквозное шифрование</div>
      </div>
    </div>
    <div class="info-rows">
      <div class="info-row"><div>@{profile?.username ?? chat.peer}</div><div class="muted small">Имя пользователя</div></div>
      {#if profile?.bio}<div class="info-row"><div class="bio">{profile.bio}</div><div class="muted small">О себе</div></div>{/if}
    </div>
    <button class="row-btn" onclick={() => (app.modal = { kind: 'verify', username: chat.peer! })}>
      <Icon name="shield" size={20} />
      <span class="grow">Ключи шифрования</span>
      {#if peer}<StatusBadge status={peer.status} />{/if}
    </button>
    <p class="muted small hint">Сверьте отпечатки с собеседником лично или по звонку, чтобы исключить подмену ключей.</p>
  {:else if chat}
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
              <Avatar name={u.name} id={u.userId} user={u.userId} photo={u.avatarId} size={40} />
              <div class="who-text">
                <div class="uname">{u.name}{u.me ? ' (вы)' : ''}</div>
                <div class="muted small">@{u.username} · {u.devices} {plural(u.devices, 'устройство', 'устройства', 'устройств')}</div>
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
  .row-btn { width: 100%; display: flex; align-items: center; gap: 14px; padding: 12px 10px; border-radius: 10px; text-align: left; color: var(--accent-light); }
  .row-btn:hover { background: var(--bg-hover); }
  .grow { flex: 1; color: var(--text); }
  .hint { margin: 6px 10px 0; }
  .info-rows { display: flex; flex-direction: column; gap: 2px; margin-bottom: 10px; }
  .info-row { padding: 8px 10px; border-radius: 10px; }
  .info-row:hover { background: var(--bg-hover); }
  .bio { white-space: pre-wrap; word-break: break-word; }
</style>
