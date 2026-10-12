<script lang="ts">
  // Set / change / turn off the passcode: current code (if set), then the
  // new one twice.
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import Modal from './Modal.svelte'

  let { mode }: { mode: 'set' | 'change' | 'off' } = $props()
  // svelte-ignore state_referenced_locally
  let step = $state<'current' | 'new' | 'repeat'>(mode === 'set' ? 'new' : 'current')
  let current = ''
  let first = ''
  let value = $state('')
  let error = $state('')
  let input: HTMLInputElement | undefined = $state()

  // svelte-ignore state_referenced_locally
  const title = mode === 'set' ? 'Код-пароль' : mode === 'change' ? 'Изменить код-пароль' : 'Отключить код-пароль'
  const prompt = $derived(step === 'current' ? 'Введите текущий код-пароль' : step === 'new' ? 'Придумайте код-пароль из 4 цифр' : 'Повторите код-пароль')

  $effect(() => { void step; input?.focus() })

  async function onInput() {
    value = value.replace(/\D/g, '').slice(0, 4)
    error = ''
    if (value.length < 4) return
    const v = value
    value = ''
    if (step === 'current') {
      current = v
      if (mode === 'off') {
        if ((await app.try(() => Messenger.SetPasscode(current, ''))) !== undefined) {
          await app.loadPasscode()
          app.modal = { kind: 'settings' }
          app.toast('Код-пароль отключён')
        }
        return
      }
      step = 'new'
    } else if (step === 'new') {
      first = v
      step = 'repeat'
    } else {
      if (v !== first) {
        error = 'Коды не совпадают — попробуйте ещё раз'
        step = 'new'
        return
      }
      const done = mode === 'set' ? 'Код-пароль включён' : 'Код-пароль изменён' // props go with the modal
      if ((await app.try(() => Messenger.SetPasscode(current, v))) !== undefined) {
        await app.loadPasscode()
        app.modal = { kind: 'settings' }
        app.toast(done)
      } else {
        step = mode === 'set' ? 'new' : 'current'
      }
    }
  }
</script>

<Modal {title} onclose={() => (app.modal = { kind: 'settings' })} width={360}>
  <p class="prompt">{prompt}</p>
  <div class="entry">
    <div class="dots">{#each [0, 1, 2, 3] as i}<span class:on={i < value.length}></span>{/each}</div>
    <input bind:this={input} class="code" type="password" inputmode="numeric" autocomplete="off" maxlength="4" aria-label={prompt}
      bind:value oninput={onInput} />
  </div>
  {#if error}<p class="err">{error}</p>{/if}
  {#if mode === 'set' && step === 'new'}
    <p class="muted small">Код запрашивается при запуске приложения и после автоблокировки. Его нельзя восстановить — если забудете, выйдите из аккаунта и войдите снова.</p>
  {/if}
</Modal>

<style>
  .prompt { text-align: center; margin: 4px 0 12px; font-size: 15px; }
  .entry { position: relative; }
  .code { position: absolute; inset: 0; width: 100%; opacity: 0; cursor: pointer; }
  .dots { display: flex; justify-content: center; gap: 18px; margin: 6px 0 14px; }
  .dots span { width: 14px; height: 14px; border-radius: 50%; border: 2px solid var(--text-muted); }
  .dots span.on { background: var(--accent-light); border-color: var(--accent-light); }
  .err { color: var(--danger); text-align: center; font-size: 14px; margin: 0 0 8px; }
  .small { font-size: 13px; }
</style>
