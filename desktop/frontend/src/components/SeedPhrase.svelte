<script lang="ts">
  import { app } from '../lib/app.svelte'

  let { phrase }: { phrase: string } = $props()
  let saved = $state(false)
  const words = $derived(phrase.split(/\s+/))

  async function copy() {
    try {
      await navigator.clipboard.writeText(phrase)
      app.toast('Скопировано. Сохраните фразу в надёжном месте.')
    } catch {
      app.toast('Не удалось скопировать', 'error')
    }
  }
</script>

<div class="wrap">
  <div class="card">
    <h1>Секретная фраза</h1>
    <p class="muted">
      Запишите эти 24 слова. Только по ним можно восстановить историю переписки на новом устройстве.
      Фраза показывается один раз и не хранится на сервере.
    </p>
    <ol class="words">
      {#each words as w, i}
        <li><span class="n">{i + 1}</span>{w}</li>
      {/each}
    </ol>
    <button class="btn flat" onclick={copy}>Скопировать</button>
    <label class="check">
      <input type="checkbox" bind:checked={saved} /> Я сохранил(а) фразу
    </label>
    <button class="btn block" disabled={!saved} onclick={() => (app.seedPhrase = null)}>Продолжить</button>
  </div>
</div>

<style>
  .wrap { height: 100%; display: flex; align-items: center; justify-content: center; padding: 24px; overflow-y: auto; background: var(--bg-sidebar); }
  .card { max-width: 520px; width: 100%; display: flex; flex-direction: column; gap: 14px; text-align: center; }
  h1 { margin: 0; font-size: 24px; }
  p { margin: 0; line-height: 1.45; }
  .words { list-style: none; padding: 16px; margin: 0; display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px 16px;
    background: var(--bg-chat); border-radius: 12px; text-align: left; }
  .words li { font-size: 15px; }
  .n { display: inline-block; width: 24px; color: var(--text-muted); font-size: 12px; }
  .check { display: flex; gap: 8px; align-items: center; justify-content: center; cursor: pointer; }
  @media (max-width: 480px) { .words { grid-template-columns: repeat(2, 1fr); } }
</style>
