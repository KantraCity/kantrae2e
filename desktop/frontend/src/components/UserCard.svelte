<script lang="ts">
  // A person's profile, opened from their avatar or name in a group chat.
  import { app } from '../lib/app.svelte'
  import { Messenger, type UserProfile } from '../lib/api'
  import { lastSeenText } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'

  let { userId, name, photo }: { userId: string; name: string; photo?: string } = $props()
  let profile = $state<UserProfile | null>(null)
  const me = $derived(app.state?.account?.userId === userId)
  $effect(() => {
    Messenger.UserProfile(userId).then((p) => (profile = p)).catch(() => {})
    app.refreshPresence([userId])
  })

  async function write() {
    if (!profile?.username) return
    app.modal = null
    await app.openDirect(profile.username)
  }
</script>

<Modal title="Профиль" onclose={() => (app.modal = null)}>
  <div class="head">
    <Avatar name={profile?.name ?? name} id={userId} user={userId} photo={profile?.avatarId ?? photo} size={96} />
    <div>
      <div class="name">{profile?.name ?? name}</div>
      <div class="muted" class:online={app.presence[userId]?.online}>{me ? 'это вы' : lastSeenText(app.presence[userId])}</div>
    </div>
  </div>
  <div class="info-rows">
    {#if profile?.username}<div class="info-row"><div>@{profile.username}</div><div class="muted small">Имя пользователя</div></div>{/if}
    {#if profile?.bio}<div class="info-row"><div class="bio">{profile.bio}</div><div class="muted small">О себе</div></div>{/if}
  </div>
  {#if !me && profile?.username}
    <button class="row-btn" onclick={write}><Icon name="send" size={20} /><span class="grow">Написать сообщение</span></button>
    <button class="row-btn" onclick={() => (app.modal = { kind: 'verify', username: profile!.username })}>
      <Icon name="shield" size={20} /><span class="grow">Ключи шифрования</span>
    </button>
  {/if}
</Modal>

<style>
  .head { display: flex; align-items: center; gap: 18px; padding: 6px 0 14px; }
  .name { font-size: 20px; font-weight: 600; word-break: break-word; }
  .online { color: var(--accent-light); }
  .info-rows { display: flex; flex-direction: column; gap: 12px; padding: 4px 0 12px; }
  .bio { white-space: pre-wrap; word-break: break-word; }
  .small { font-size: 13px; }
  .row-btn { display: flex; align-items: center; gap: 14px; width: 100%; padding: 12px 4px; border-radius: 10px; color: var(--text); }
  .row-btn:hover { background: var(--bg-hover); }
  .grow { flex: 1; text-align: left; }
</style>
