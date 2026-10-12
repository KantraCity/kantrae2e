<script lang="ts">
  // New poll: a question, 2–10 options, anonymous / several answers.
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import Modal from './Modal.svelte'
  import Icon from './Icon.svelte'
  import { tick } from 'svelte'

  let { chatId }: { chatId: string } = $props()
  let question = $state('')
  let options = $state(['', ''])
  let anonymous = $state(true)
  let multiple = $state(false)
  let busy = $state(false)
  let box: HTMLDivElement | undefined = $state()

  const filled = $derived(options.map((o) => o.trim()).filter(Boolean))
  const ok = $derived(question.trim().length > 0 && filled.length >= 2 && new Set(filled).size === filled.length)

  async function onOption(i: number) {
    // Typing into the last option adds a new empty one, like Telegram.
    if (i === options.length - 1 && options[i].trim() && options.length < 10) options = [...options, '']
  }
  function remove(i: number) {
    options = options.filter((_, j) => j !== i)
    if (options.length < 2) options = [...options, '']
  }
  async function keyOpt(e: KeyboardEvent, i: number) {
    if (e.key === 'Enter') {
      e.preventDefault()
      await tick()
      box?.querySelectorAll<HTMLInputElement>('input.opt')[i + 1]?.focus()
    }
  }
  async function send() {
    if (!ok || busy) return
    busy = true
    const r = await app.try(() => Messenger.SendPoll(chatId, question.trim(), filled, multiple, anonymous))
    busy = false
    if (r === undefined) return
    app.modal = null
    if (r) app.addMessage(r)
  }
</script>

<Modal title="Новый опрос" onclose={() => (app.modal = null)}>
  <label class="lbl" for="pq">Вопрос</label>
  <input id="pq" class="field" bind:value={question} maxlength="255" placeholder="Задайте вопрос" />
  <div class="lbl">Варианты ответа</div>
  <div class="opts" bind:this={box}>
    {#each options as _, i}
      <div class="row">
        <input class="field opt" bind:value={options[i]} maxlength="100" placeholder={i < 2 ? `Вариант ${i + 1}` : 'Добавить вариант…'}
          oninput={() => onOption(i)} onkeydown={(e) => keyOpt(e, i)} />
        {#if options.length > 2 && (options[i] || i < options.length - 1)}
          <button class="icon-btn" aria-label="Убрать вариант" onclick={() => remove(i)}><Icon name="close" size={16} /></button>
        {/if}
      </div>
    {/each}
  </div>
  <p class="muted hint">{options.length >= 10 ? 'Максимум 10 вариантов' : `Можно добавить ещё ${10 - options.length + (options[options.length - 1] ? 0 : 1)}`}</p>
  <label class="check"><input type="checkbox" bind:checked={anonymous} /> Анонимное голосование</label>
  <label class="check"><input type="checkbox" bind:checked={multiple} /> Выбор нескольких ответов</label>
  <div class="actions">
    <button class="btn flat" onclick={() => (app.modal = null)}>Отмена</button>
    <button class="btn" disabled={!ok || busy} onclick={send}>Создать</button>
  </div>
</Modal>

<style>
  .lbl { display: block; font-size: 13px; color: var(--accent-light); font-weight: 600; margin: 4px 0 6px; }
  .field { width: 100%; }
  .opts { display: flex; flex-direction: column; gap: 6px; max-height: 40vh; overflow-y: auto; }
  .row { display: flex; align-items: center; gap: 4px; }
  .hint { font-size: 12px; margin: 6px 0 10px; }
  .check { display: flex; gap: 10px; align-items: center; cursor: pointer; margin-bottom: 8px; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px; }
</style>
