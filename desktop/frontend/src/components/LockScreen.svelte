<script lang="ts">
  // The passcode lock screen: four dots and a keypad (also the keyboard).
  import { onMount } from 'svelte'
  import { fade } from 'svelte/transition'
  import { app } from '../lib/app.svelte'
  import { Messenger, errorText } from '../lib/api'
  import Icon from './Icon.svelte'

  let code = $state('')
  let shake = $state(false)
  let busy = $state(false)
  let error = $state('')

  async function press(d: string) {
    if (busy || code.length >= 4) return
    error = ''
    code += d
    navigator.vibrate?.(6)
    if (code.length === 4) await check()
  }
  function back() { code = code.slice(0, -1) }
  async function check() {
    busy = true
    try {
      if (await Messenger.Unlock(code)) {
        app.locked = false
        return
      }
      error = 'Неверный код-пароль'
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = false
    }
    shake = true
    navigator.vibrate?.([30, 40, 30])
    setTimeout(() => { shake = false; code = '' }, 450)
  }
  function key(e: KeyboardEvent) {
    if (/^[0-9]$/.test(e.key)) press(e.key)
    else if (e.key === 'Backspace') back()
    else return
    e.preventDefault()
  }
  onMount(() => (document.activeElement as HTMLElement | null)?.blur())
</script>

<svelte:window onkeydown={key} />

<div class="lock" out:fade={{ duration: 180 }}>
  <div class="logo"><Icon name="lock" size={34} /></div>
  <div class="title">Введите код-пароль</div>
  <div class="dots" class:shake>
    {#each [0, 1, 2, 3] as i}<span class:on={i < code.length}></span>{/each}
  </div>
  <div class="err">{error}</div>
  <div class="pad">
    {#each ['1', '2', '3', '4', '5', '6', '7', '8', '9'] as d}
      <button onclick={() => press(d)}>{d}</button>
    {/each}
    <span></span>
    <button onclick={() => press('0')}>0</button>
    <button class="bk" aria-label="Стереть" onclick={back} disabled={!code}><Icon name="back" size={24} /></button>
  </div>
</div>

<style>
  .lock { position: absolute; inset: var(--titlebar-h, 0px) 0 0; z-index: 1000; background: var(--bg-sidebar); display: flex; flex-direction: column; align-items: center;
    justify-content: center; gap: 14px; padding: 24px; user-select: none; -webkit-user-select: none; }
  .logo { width: 72px; height: 72px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; }
  .title { font-size: 18px; font-weight: 600; }
  .dots { display: flex; gap: 18px; margin: 8px 0 0; }
  .dots span { width: 14px; height: 14px; border-radius: 50%; border: 2px solid var(--text-muted); transition: background .12s, border-color .12s, transform .12s; }
  .dots span.on { background: var(--accent-light); border-color: var(--accent-light); transform: scale(1.1); }
  .dots.shake { animation: shake .4s; }
  .dots.shake span { border-color: var(--danger); }
  @keyframes shake { 20%, 60% { transform: translateX(-10px) } 40%, 80% { transform: translateX(10px) } }
  .err { height: 18px; color: var(--danger); font-size: 14px; }
  .pad { display: grid; grid-template-columns: repeat(3, 72px); gap: 14px 26px; }
  .pad button { width: 72px; height: 72px; border-radius: 50%; font-size: 28px; font-weight: 400; background: var(--tint-1); transition: background .1s, transform .08s; }
  .pad button:hover { background: var(--tint-2); }
  .pad button:active { transform: scale(.94); background: var(--tint-3); }
  .pad .bk { background: none; color: var(--text-muted); display: flex; align-items: center; justify-content: center; }
  .pad .bk:disabled { opacity: .3; }
</style>
