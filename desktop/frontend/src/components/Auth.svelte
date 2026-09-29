<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, errorText } from '../lib/api'
  import { plural } from '../lib/util'

  let mode = $state<'login' | 'register'>('register')
  let server = $state(app.state?.serverUrl ?? 'https://localhost')
  let editServer = $state(false)
  let username = $state('')
  let password = $state('')
  let device = $state('')
  let seed = $state('')
  let busy = $state(false)
  let error = $state('')

  const valid = $derived(username.trim().length >= 3 && password.length >= 8 && server.trim() !== '')

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    if (!valid || busy) return
    busy = true
    error = ''
    try {
      if (mode === 'register') {
        app.seedPhrase = await Messenger.Register(server, username.trim(), password, device)
      } else {
        const r = await Messenger.Login(server, username.trim(), password, device, seed)
        if (r) {
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
  <form class="card" onsubmit={submit}>
    <img class="logo" src="/icon.svg" alt="" />
    <h1>Kantra</h1>
    <p class="muted sub">
      {mode === 'register' ? 'Создайте аккаунт. Сообщения шифруются на устройстве (MLS).' : 'Войдите, чтобы подключить это устройство к аккаунту.'}
    </p>

    <div class="tabs">
      <button type="button" class:active={mode === 'register'} onclick={() => (mode = 'register')}>Регистрация</button>
      <button type="button" class:active={mode === 'login'} onclick={() => (mode = 'login')}>Вход</button>
    </div>

    <input class="field" placeholder="Имя пользователя" autocomplete="username" bind:value={username} />
    <input class="field" type="password" placeholder="Пароль (от 8 символов)"
      autocomplete={mode === 'register' ? 'new-password' : 'current-password'} bind:value={password} />
    <input class="field" placeholder="Название устройства (необязательно)" bind:value={device} />
    {#if mode === 'login'}
      <textarea class="field seed" rows="3" placeholder="Seed-фраза из 24 слов — чтобы восстановить историю (необязательно)" bind:value={seed}></textarea>
    {/if}

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
  .auth { height: 100%; display: flex; align-items: center; justify-content: center; padding: 24px; overflow-y: auto; background: var(--bg-sidebar); }
  .card { width: 100%; max-width: 360px; display: flex; flex-direction: column; gap: 12px; text-align: center; }
  .logo { width: 120px; height: 120px; margin: 0 auto 8px; }
  h1 { margin: 0; font-size: 28px; font-weight: 600; }
  .sub { margin: 0 0 8px; line-height: 1.4; }
  .tabs { display: flex; background: var(--bg-input); border-radius: 10px; padding: 3px; margin-bottom: 4px; }
  .tabs button { flex: 1; padding: 8px; border-radius: 8px; color: var(--text-muted); font-weight: 500; }
  .tabs button.active { background: var(--bg-active); color: var(--text); }
  .seed { resize: none; }
  .server { font-size: 13px; text-align: center; }
  .server span { color: var(--accent-light); }
  .note { font-size: 12px; margin: 0; }
</style>
