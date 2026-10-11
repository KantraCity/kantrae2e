<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, errorText } from '../lib/api'
  import { plural } from '../lib/util'

  let mode = $state<'login' | 'register'>('register')
  let server = $state(app.state?.serverUrl ?? 'https://localhost')
  let editServer = $state(false)
  let firstName = $state('')
  let lastName = $state('')
  let username = $state('')
  let password = $state('')
  let device = $state('')
  let busy = $state(false)
  let error = $state('')

  const valid = $derived(username.trim().length >= 3 && password.length >= 8 && server.trim() !== ''
    && (mode === 'login' || firstName.trim() !== ''))
  // Adding another account: a way back to the current ones.
  const adding = $derived((app.state?.accounts ?? 0) > 1)

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    if (!valid || busy) return
    busy = true
    error = ''
    try {
      if (mode === 'register') {
        await Messenger.Register(server, username.trim().replace(/^@/, ''), password, `${firstName.trim()} ${lastName.trim()}`.trim(), device)
      } else {
        const r = await Messenger.Login(server, username.trim().replace(/^@/, ''), password, device)
        if (r && !r.pending) {
          const parts = []
          if (r.restored) parts.push(`восстановлено ${r.restored} ${plural(r.restored, 'сообщение', 'сообщения', 'сообщений')}`)
          if (r.joined) parts.push(`${r.joined} ${plural(r.joined, 'чат подключён', 'чата подключено', 'чатов подключено')}`)
          if (r.requested) parts.push(`запрошено добавление в ${r.requested}`)
          if (parts.length) app.toast(parts.join(', '))
        }
      }
      await app.afterAuth()
    } catch (err) {
      error = errorText(err)
    } finally {
      busy = false
    }
  }
</script>

<div class="auth">
  {#if adding}
    <button class="back muted" onclick={() => app.try(() => Messenger.CancelAddAccount())}>← Назад к аккаунтам</button>
  {/if}
  <form class="card" onsubmit={submit}>
    <img class="logo" src="/icon.svg" alt="" />
    <h1>Kantra</h1>
    <p class="muted sub">
      {mode === 'register' ? 'Создайте аккаунт. Сообщения шифруются на устройстве (MLS).' : 'Войдите — затем подтвердите вход на другом своём устройстве.'}
    </p>

    <div class="tabs">
      <button type="button" class:active={mode === 'register'} onclick={() => (mode = 'register')}>Регистрация</button>
      <button type="button" class:active={mode === 'login'} onclick={() => (mode = 'login')}>Вход</button>
    </div>

    {#if mode === 'register'}
      <div class="row2">
        <input class="field" placeholder="Имя" autocomplete="given-name" bind:value={firstName} />
        <input class="field" placeholder="Фамилия (необязательно)" autocomplete="family-name" bind:value={lastName} />
      </div>
    {/if}
    <input class="field" placeholder={mode === 'register' ? 'Имя пользователя (@username)' : 'Имя пользователя'} autocomplete="username" bind:value={username} />
    <input class="field" type="password" placeholder="Пароль (от 8 символов)"
      autocomplete={mode === 'register' ? 'new-password' : 'current-password'} bind:value={password} />
    <input class="field" placeholder="Название устройства (необязательно)" bind:value={device} />

    {#if editServer}
      <input class="field" placeholder="Адрес сервера" bind:value={server} />
    {:else}
      <button type="button" class="server muted" onclick={() => (editServer = true)}>Сервер: {server} · <span>изменить</span></button>
    {/if}

    {#if error}<div class="error-text">{error}</div>{/if}
    <button class="btn block" disabled={!valid || busy}>
      {busy ? 'Подождите…' : mode === 'register' ? 'Создать аккаунт' : 'Войти'}
    </button>
    {#if app.state?.webMode}
      <p class="muted note">Веб-режим: ключи хранятся в процессе, который вы запустили на этом компьютере.</p>
    {/if}
  </form>
</div>

<style>
  .auth { position: relative; height: 100%; display: flex; align-items: center; justify-content: center; padding: 24px; overflow-y: auto; background: var(--bg-sidebar); }
  .card { width: 100%; max-width: 360px; display: flex; flex-direction: column; gap: 12px; text-align: center; }
  .logo { width: 120px; height: 120px; margin: 0 auto 8px; }
  h1 { margin: 0; font-size: 28px; font-weight: 600; }
  .sub { margin: 0 0 8px; line-height: 1.4; }
  .tabs { display: flex; background: var(--bg-input); border-radius: 10px; padding: 3px; margin-bottom: 4px; }
  .tabs button { flex: 1; padding: 8px; border-radius: 8px; color: var(--text-muted); font-weight: 500; }
  .tabs button.active { background: var(--bg-active); color: var(--text); }
  .server { font-size: 13px; text-align: center; }
  .row2 { display: flex; gap: 8px; }
  .row2 .field { flex: 1; min-width: 0; }
  .back { position: absolute; top: 14px; left: 16px; font-size: 14px; }
  .back:hover { color: var(--text); }
  .server span { color: var(--accent-light); }
  .note { font-size: 12px; margin: 0; }
</style>
