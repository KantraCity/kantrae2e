<script lang="ts">
  // Like Telegram's "Edit profile": photo, first/last name, bio, @username.
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import { photos } from '../lib/photos.svelte'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'

  const BIO_MAX = 140
  const me = app.me
  const parts = (me?.name ?? '').split(' ')
  let first = $state(parts[0] ?? '')
  let last = $state(parts.slice(1).join(' '))
  let bio = $state(me?.bio ?? '')
  let username = $state(me?.username ?? '')
  let busy = $state(false)
  let fileInput: HTMLInputElement | undefined = $state()

  const usernameValid = $derived(/^[a-zA-Z0-9_.-]{3,32}$/.test(username.replace(/^@/, '')))

  // Square crop, 512 px JPEG: what Telegram does with profile photos.
  async function toAvatar(file: File): Promise<string> {
    const bmp = await createImageBitmap(file)
    const side = Math.min(bmp.width, bmp.height)
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = Math.min(512, side)
    canvas.getContext('2d')!.drawImage(bmp, (bmp.width - side) / 2, (bmp.height - side) / 2, side, side, 0, 0, canvas.width, canvas.height)
    return canvas.toDataURL('image/jpeg', 0.88)
  }

  async function pickPhoto(files: FileList | null) {
    const f = files?.[0]
    if (!f) return
    busy = true
    try {
      const dataUrl = await toAvatar(f)
      const p = await app.try(() => Messenger.SetAvatar(dataUrl.split(',')[1]))
      if (p) {
        if (p.avatarId) photos.put(p.userId, p.avatarId, dataUrl)
        app.me = p
      }
    } catch {
      app.toast('Не удалось прочитать изображение', 'error')
    } finally {
      busy = false
      if (fileInput) fileInput.value = ''
    }
  }

  async function removePhoto() {
    const p = await app.try(() => Messenger.SetAvatar(''))
    if (p) app.me = p
  }

  async function save(e: SubmitEvent) {
    e.preventDefault()
    if (busy || !first.trim()) return
    busy = true
    let p = await app.try(() => Messenger.UpdateProfile(`${first.trim()} ${last.trim()}`.trim(), bio))
    const u = username.replace(/^@/, '').trim()
    if (p && u !== p.username) p = await app.try(() => Messenger.SetUsername(u))
    busy = false
    if (p) {
      app.me = p
      if (app.state?.account) app.state.account.username = p.username
      app.toast('Профиль сохранён')
      app.modal = null
    }
  }
</script>

<Modal title="Мой профиль" onclose={() => (app.modal = null)} width={440}>
  <form onsubmit={save}>
    <div class="photo">
      <button type="button" class="photo-btn" onclick={() => fileInput?.click()} aria-label="Сменить фото">
        <Avatar name={`${first} ${last}`.trim() || '?'} id={me?.userId} user={app.me?.userId} photo={app.me?.avatarId} size={110} />
        <span class="cam"><Icon name="camera" size={26} /></span>
      </button>
      <input type="file" accept="image/*" hidden bind:this={fileInput} onchange={(e) => pickPhoto((e.currentTarget as HTMLInputElement).files)} />
      {#if app.me?.avatarId}<button type="button" class="link" onclick={removePhoto}>Удалить фото</button>{/if}
    </div>

    <input class="field" placeholder="Имя (обязательно)" bind:value={first} maxlength="64" />
    <input class="field" placeholder="Фамилия (необязательно)" bind:value={last} maxlength="64" />

    <div class="bio">
      <textarea class="field" rows="2" placeholder="О себе" bind:value={bio} maxlength={BIO_MAX}></textarea>
      <span class="count muted">{BIO_MAX - bio.length}</span>
    </div>
    <p class="muted hint">Любые подробности, например: возраст, род занятий или город.</p>

    <label class="label muted" for="un">Имя пользователя</label>
    <div class="un" class:bad={!usernameValid}>
      <span>@</span><input id="un" bind:value={username} autocomplete="off" autocapitalize="off" spellcheck="false" />
    </div>
    <p class="muted hint">По этому имени вас можно найти в поиске и написать вам. Латиница, цифры, «_», «.», «-»; от 3 символов.</p>

    <button class="btn block" disabled={busy || !first.trim() || !usernameValid}>{busy ? 'Сохранение…' : 'Сохранить'}</button>
  </form>
</Modal>

<style>
  form { display: flex; flex-direction: column; gap: 10px; }
  .photo { display: flex; flex-direction: column; align-items: center; gap: 6px; margin-bottom: 6px; }
  .photo-btn { position: relative; border-radius: 50%; }
  .cam { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; color: #fff; background: rgba(0, 0, 0, .35);
    border-radius: 50%; opacity: 0; transition: opacity .15s; }
  .photo-btn:hover .cam { opacity: 1; }
  .link { color: var(--accent-light); font-size: 14px; }
  .bio { position: relative; }
  .bio textarea { width: 100%; resize: none; padding-right: 44px; }
  .count { position: absolute; right: 12px; top: 12px; font-size: 13px; }
  .hint { margin: -4px 4px 4px; font-size: 13px; line-height: 1.4; }
  .label { font-size: 13px; margin: 4px 4px -4px; }
  .un { display: flex; align-items: center; gap: 2px; background: var(--bg-input); border-radius: 10px; padding: 0 12px; border: 1px solid transparent; }
  .un.bad { border-color: #e17076; }
  .un span { color: var(--text-muted); }
  .un input { flex: 1; background: transparent; border: 0; outline: 0; padding: 11px 0; color: var(--text); font-size: 15px; }
</style>
