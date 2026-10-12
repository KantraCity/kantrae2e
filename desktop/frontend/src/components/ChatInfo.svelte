<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, type Member, type UserProfile } from '../lib/api'
  import { lastSeenText, plural } from '../lib/util'
  import { blobToBase64 } from '../lib/media'
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

  // ---- direct chat: presence and block ----
  const blocked = $derived(!!chat?.peerId && app.blocked.includes(chat.peerId))
  $effect(() => {
    if (chat?.kind === 'direct' && chat.peerId) app.refreshPresence([chat.peerId])
  })
  async function toggleBlock() {
    if (!chat?.peerId) return
    if (!blocked && !confirm(`Заблокировать ${chat.name}? Он(а) не сможет писать и звонить вам.`)) return
    if (await app.setBlocked(chat.peerId, !blocked)) app.toast(blocked ? 'Пользователь заблокирован' : 'Пользователь разблокирован')
  }

  // ---- group: name, description, photo, invite link ----
  let editing = $state(false)
  let editName = $state('')
  let editAbout = $state('')
  function startEdit() {
    editName = chat?.name ?? ''
    editAbout = chat?.about ?? ''
    editing = true
  }
  async function saveInfo(e: SubmitEvent) {
    e.preventDefault()
    if (!editName.trim()) return
    const ok = await app.try(() => Messenger.SetGroupInfo(chatId, editName.trim(), editAbout.trim()))
    if (ok !== undefined) {
      editing = false
      await app.refreshChats()
    }
  }
  let photoInput: HTMLInputElement | undefined = $state()
  async function setPhoto(files: FileList | null) {
    const f = files?.[0]
    if (photoInput) photoInput.value = ''
    if (!f) return
    try {
      // Square, 640 px, JPEG — like Telegram's group photos.
      const bmp = await createImageBitmap(f)
      const side = Math.min(bmp.width, bmp.height)
      const c = document.createElement('canvas')
      c.width = c.height = Math.min(640, side)
      c.getContext('2d')!.drawImage(bmp, (bmp.width - side) / 2, (bmp.height - side) / 2, side, side, 0, 0, c.width, c.height)
      const blob = await new Promise<Blob>((r) => c.toBlob((b) => r(b!), 'image/jpeg', 0.88))
      const b64 = await blobToBase64(blob)
      const ok = await app.try(() => Messenger.SetGroupPhoto(chatId, b64))
      if (ok !== undefined) await app.refreshChats()
    } catch {
      app.toast('Не удалось открыть картинку', 'error')
    }
  }
  let link = $state('')
  async function showLink(reset = false) {
    if (reset && !confirm('Старая ссылка перестанет работать. Создать новую?')) return
    const l = await app.try(() => Messenger.InviteLink(chatId, reset))
    if (l) {
      link = l
      await app.refreshChats()
    }
  }
  async function copyLink() {
    try {
      await navigator.clipboard.writeText(link)
      app.toast('Ссылка скопирована')
    } catch { app.toast('Не удалось скопировать', 'error') }
  }
  async function revokeLink() {
    if (!confirm('Отключить ссылку-приглашение?')) return
    const ok = await app.try(() => Messenger.RevokeInviteLink(chatId))
    if (ok !== undefined) {
      link = ''
      await app.refreshChats()
    }
  }
  $effect(() => { if (chat?.hasInvite && !link) Messenger.InviteLink(chatId, false).then((l) => (link = l)).catch(() => {}) })

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
        <div class="muted" class:online={app.presence[chat.peerId ?? '']?.online}>{lastSeenText(app.presence[chat.peerId ?? '']) || 'личный чат'}</div>
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
    <button class="row-btn" class:red={!blocked} onclick={toggleBlock}>
      <Icon name="block" size={20} /><span>{blocked ? 'Разблокировать' : 'Заблокировать'}</span>
    </button>
  {:else if chat}
    <div class="head">
      <button class="photo-btn" disabled={!chat.active} title="Изменить фото группы" onclick={() => photoInput?.click()}>
        <Avatar name={chat.name} id={chat.id} src={chat.photo} size={72} />
        {#if chat.active}<span class="photo-cam"><Icon name="camera" size={16} /></span>{/if}
      </button>
      <input type="file" hidden accept="image/*" bind:this={photoInput} onchange={(e) => setPhoto((e.currentTarget as HTMLInputElement).files)} />
      <div class="grow-head">
        <div class="name">{chat.name}</div>
        <div class="muted">{chat.active ? `${users.length} ${plural(users.length, 'участник', 'участника', 'участников')}` : 'вы не участник'}</div>
      </div>
      {#if chat.active && !editing}<button class="icon-btn" title="Изменить" aria-label="Изменить" onclick={startEdit}><Icon name="pencil" size={20} /></button>{/if}
    </div>
    {#if editing}
      <form class="edit" onsubmit={saveInfo}>
        <input class="field" placeholder="Название группы" bind:value={editName} maxlength="128" />
        <textarea class="field" placeholder="Описание (необязательно)" bind:value={editAbout} maxlength="255" rows="2"></textarea>
        <div class="edit-actions">
          <button type="button" class="btn flat" onclick={() => (editing = false)}>Отмена</button>
          <button class="btn" disabled={!editName.trim()}>Сохранить</button>
        </div>
      </form>
    {:else if chat.about}
      <div class="info-rows"><div class="info-row"><div class="bio">{chat.about}</div><div class="muted small">Описание</div></div></div>
    {/if}

    {#if chat.active}
      <div class="link-box">
        <div class="section muted">Ссылка-приглашение</div>
        {#if link}
          <div class="link-row">
            <code class="link">{link}</code>
            <button class="icon-btn" title="Копировать" aria-label="Копировать" onclick={copyLink}><Icon name="copy" size={18} /></button>
          </div>
          <div class="link-actions">
            <button class="btn flat" onclick={() => showLink(true)}>Новая ссылка</button>
            <button class="btn flat danger-text" onclick={revokeLink}>Отключить</button>
          </div>
        {:else}
          <button class="row-btn" onclick={() => showLink()}><Icon name="link" size={20} /><span class="grow">Создать ссылку-приглашение</span></button>
        {/if}
      </div>
    {/if}

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
  {#if chat}
    {@const isMuted = (chat.mutedUntil ?? 0) > Date.now()}
    <label class="row-btn plain notif">
      <Icon name={isMuted ? 'bell-off' : 'bell'} size={20} />
      <span class="grow">Уведомления{#if isMuted}<small class="muted"> · {chat.mutedUntil! >= 253402300799000 ? 'выключены' : 'до ' + new Date(chat.mutedUntil!).toLocaleString('ru', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })}</small>{/if}</span>
      <input type="checkbox" class="switch" checked={!isMuted} onchange={(e) => app.muteChat(chat, (e.currentTarget as HTMLInputElement).checked ? 0 : -1)} />
    </label>
    <div class="danger-zone">
      <button class="row-btn plain" onclick={() => (app.modal = { kind: 'deleteChat', chatId: chat.id, action: 'clear' })}>
        <Icon name="broom" size={20} /><span>Очистить историю</span>
      </button>
      {#if chat.kind === 'group'}
        <button class="row-btn red" onclick={() => (app.modal = { kind: 'deleteChat', chatId: chat.id, action: 'leave' })}>
          <Icon name="leave" size={20} /><span>Покинуть группу</span>
        </button>
      {:else}
        <button class="row-btn red" onclick={() => (app.modal = { kind: 'deleteChat', chatId: chat.id, action: 'delete' })}>
          <Icon name="trash" size={20} /><span>Удалить чат</span>
        </button>
      {/if}
    </div>
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
  .danger-zone { border-top: 1px solid var(--border); margin-top: 14px; padding-top: 8px; display: flex; flex-direction: column; }
  .row-btn.plain { color: var(--text-muted); }
  .notif { cursor: pointer; }
  .notif .grow { flex: 1; }
  .switch { appearance: none; width: 36px; height: 20px; border-radius: 10px; background: #46525e; position: relative; cursor: pointer; transition: background .15s; flex-shrink: 0; }
  .switch::after { content: ''; position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: 50%; background: #fff; transition: transform .15s; }
  .switch:checked { background: var(--accent); }
  .switch:checked::after { transform: translateX(16px); }
  .row-btn.plain span { color: var(--text); }
  .row-btn.red { color: #e17076; }
  .grow { flex: 1; color: var(--text); }
  .hint { margin: 6px 10px 0; }
  .info-rows { display: flex; flex-direction: column; gap: 2px; margin-bottom: 10px; }
  .info-row { padding: 8px 10px; border-radius: 10px; }
  .info-row:hover { background: var(--bg-hover); }
  .bio { white-space: pre-wrap; word-break: break-word; }
  .photo-btn { position: relative; border-radius: 50%; }
  .photo-cam { position: absolute; right: -2px; bottom: -2px; width: 26px; height: 26px; border-radius: 50%; background: var(--accent);
    color: #fff; display: flex; align-items: center; justify-content: center; border: 2px solid var(--bg-modal); }
  .grow-head { flex: 1; min-width: 0; }
  .edit { display: flex; flex-direction: column; gap: 8px; margin-bottom: 14px; }
  .edit textarea { resize: none; }
  .edit-actions { display: flex; justify-content: flex-end; gap: 8px; }
  .link-box { margin-bottom: 14px; }
  .link-row { display: flex; align-items: center; gap: 6px; background: var(--bg-input); border-radius: 10px; padding: 6px 6px 6px 12px; }
  .link { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; color: var(--accent-light); }
  .link-actions { display: flex; gap: 8px; margin-top: 6px; }
  .danger-text { color: #e17076; }
  .row-btn.red { color: #e17076; }
  .row-btn.red span { color: #e17076; }
  .online { color: var(--accent-light) !important; }
</style>
