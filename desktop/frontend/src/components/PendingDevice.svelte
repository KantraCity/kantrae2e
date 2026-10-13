<script lang="ts">
  import { onMount } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { Messenger, errorText } from '../lib/api'
  import { plural } from '../lib/util'
  import Icon from './Icon.svelte'

  let mode = $state<'wait' | 'recover'>('wait')
  let phrase = $state('')
  let busy = $state(false)
  let error = $state('')

  const code = $derived(app.state?.linkCode ?? '')
  const words = $derived(phrase.trim().split(/\s+/).filter(Boolean).length)

  // Poll the state as a fallback for missed events.
  onMount(() => {
    const t = setInterval(async () => {
      const st = await Messenger.GetState().catch(() => null)
      if (st && !st.pending) await app.afterAuth()
    }, 5000)
    return () => clearInterval(t)
  })

  async function resend() {
    const ok = await app.try(() => Messenger.RequestLink())
    if (ok !== undefined) app.toast('Запрос отправлен на ваши устройства')
  }

  async function recover(e: SubmitEvent) {
    e.preventDefault()
    if (busy || words !== 12) return
    busy = true
    error = ''
    try {
      const r = await Messenger.Recover(phrase)
      phrase = ''
      if (r?.restored) app.toast(`Восстановлено ${r.restored} ${plural(r.restored, 'сообщение', 'сообщения', 'сообщений')}`)
      await app.afterAuth()
    } catch (err) {
      error = errorText(err)
    } finally {
      busy = false
    }
  }

  async function cancel() {
    if (!(await app.confirm('Отменить вход на этом устройстве?', { ok: 'Отменить вход', danger: true }))) return
    await app.try(() => Messenger.CancelPending())
    app.state = await Messenger.GetState()
  }
</script>

<div class="wrap">
  <div class="card">
    {#if mode === 'wait'}
      <div class="icon"><Icon name="device" size={44} /></div>
      <h1>Подтвердите вход</h1>
      <p class="muted">
        Откройте Kantra на другом своём устройстве — там появится запрос на вход.
        Проверьте, что код совпадает, и нажмите «Код совпадает».
      </p>
      <div class="code mono">{code}</div>
      <p class="muted small">
        После подтверждения это устройство получит ключ истории по зашифрованному каналу
        между вашими устройствами — сервер его не видит.
      </p>
      <div class="spinner"><span></span> Ожидание подтверждения…</div>
      <button class="btn flat" onclick={resend}>Отправить запрос ещё раз</button>
      <button class="btn flat" onclick={() => (mode = 'recover')}>Нет доступа к другим устройствам?</button>
      <button class="link muted" onclick={cancel}>Выйти</button>
    {:else}
      <div class="icon"><Icon name="key" size={44} /></div>
      <h1>Ключ восстановления</h1>
      <p class="muted">Введите 12 слов, которые вы сохранили в настройках на старом устройстве.</p>
      <form onsubmit={recover}>
        <!-- svelte-ignore a11y_autofocus -->
        <textarea class="field" rows="4" placeholder="слово1 слово2 … слово12" bind:value={phrase} autofocus
          autocomplete="off" autocapitalize="off" spellcheck="false"></textarea>
        <div class="muted small count">{words} / 12</div>
        {#if error}<div class="error-text">{error}</div>{/if}
        <button class="btn block" disabled={busy || words !== 12}>{busy ? 'Проверка…' : 'Восстановить'}</button>
      </form>
      <button class="btn flat" onclick={() => { mode = 'wait'; error = '' }}>Назад</button>
    {/if}
  </div>
</div>

<style>
  .wrap { height: 100%; display: flex; padding: 24px; overflow-y: auto; background: var(--bg-sidebar); }
  .card { margin: auto; max-width: 400px; width: 100%; display: flex; flex-direction: column; gap: 12px; text-align: center; align-items: center; }
  .icon { width: 96px; height: 96px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; margin-bottom: 4px; }
  h1 { margin: 0; font-size: 24px; font-weight: 600; }
  p { margin: 0; line-height: 1.45; }
  .small { font-size: 13px; }
  .code { font-size: 32px; letter-spacing: 3px; word-spacing: 14px; background: var(--bg-chat); border-radius: 14px; padding: 16px 22px; margin: 6px 0; }
  .spinner { display: flex; align-items: center; gap: 10px; color: var(--accent-light); margin: 4px 0; }
  .spinner span { width: 16px; height: 16px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: spin 0.8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  form { width: 100%; display: flex; flex-direction: column; gap: 10px; }
  textarea { resize: none; font-size: 16px; line-height: 1.5; }
  .count { text-align: right; }
  .link { font-size: 14px; margin-top: 6px; }
  .link:hover { color: var(--text); }
</style>
