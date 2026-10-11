<script lang="ts">
  import { app } from '../lib/app.svelte'
  import Modal from './Modal.svelte'

  let { phrase }: { phrase: string } = $props()
  let saved = $state(false)
  const words = $derived(phrase.split(/\s+/))

  async function copy() {
    try {
      await navigator.clipboard.writeText(phrase)
      app.toast('Скопировано. Сохраните ключ в надёжном месте и очистите буфер обмена.')
    } catch {
      app.toast('Не удалось скопировать', 'error')
    }
  }

  function download() {
    const user = app.state?.account?.username ?? ''
    const text = `Kantra — ключ восстановления для @${user}\n\n${words.map((w, i) => `${i + 1}. ${w}`).join('\n')}\n\n` +
      `Храните в надёжном месте. Любой, у кого есть этот ключ и ваш пароль, получит доступ к истории сообщений.\n`
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
    a.download = `kantra-recovery-${user}.txt`
    a.click()
    setTimeout(() => URL.revokeObjectURL(a.href), 1000)
  }
</script>

<Modal title="Ключ восстановления" onclose={() => saved && (app.modal = null)} width={480}>
  <div class="body">
    <p class="muted">
      Запишите эти 12 слов. Если вы потеряете все устройства, по ним (и паролю) можно
      восстановить историю на новом. Ключ показывается один раз и заменяет предыдущий.
    </p>
    <ol class="words">
      {#each words as w, i}
        <li><span class="n">{i + 1}</span>{w}</li>
      {/each}
    </ol>
    <div class="row">
      <button class="btn flat" onclick={copy}>Скопировать</button>
      <button class="btn flat" onclick={download}>Скачать .txt</button>
    </div>
    <label class="check"><input type="checkbox" bind:checked={saved} /> Я сохранил(а) ключ</label>
    <button class="btn block" disabled={!saved} onclick={() => (app.modal = null)}>Готово</button>
  </div>
</Modal>

<style>
  .body { display: flex; flex-direction: column; gap: 14px; text-align: center; }
  p { margin: 0; line-height: 1.45; }
  .words { list-style: none; padding: 16px; margin: 0; display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px 16px;
    background: var(--bg-chat); border-radius: 12px; text-align: left; }
  .words li { font-size: 15px; }
  .n { display: inline-block; width: 24px; color: var(--text-muted); font-size: 12px; }
  .row { display: flex; justify-content: center; gap: 8px; }
  .check { display: flex; gap: 8px; align-items: center; justify-content: center; cursor: pointer; }
  @media (max-width: 480px) { .words { grid-template-columns: repeat(2, 1fr); } }
</style>
