<script lang="ts">
  // "Отложить сообщение": pick a day and time; this device sends it then.
  import type { LinkView } from '../lib/api'
  import { app } from '../lib/app.svelte'
  import Modal from './Modal.svelte'

  let { chatId, text, silent, link }: { chatId: string; text: string; silent: boolean; link: LinkView | null } = $props()

  const pad = (n: number) => String(n).padStart(2, '0')
  const local = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
  const start = new Date(Date.now() + 60 * 60000)
  start.setSeconds(0, 0)
  let when = $state(local(start))
  // svelte-ignore state_referenced_locally
  let quiet = $state(silent)
  let busy = $state(false)
  const at = $derived(new Date(when).getTime())
  const valid = $derived(Number.isFinite(at) && at > Date.now() + 30_000)

  function preset(min: number) {
    const d = new Date(Date.now() + min * 60000)
    d.setSeconds(0, 0)
    when = local(d)
  }
  function tomorrow9() {
    const d = new Date()
    d.setDate(d.getDate() + 1)
    d.setHours(9, 0, 0, 0)
    when = local(d)
  }
  async function go() {
    if (!valid || busy) return
    busy = true
    const args = [chatId, text, at, quiet, link] as const
    if (await app.schedule(...args)) app.modal = null
    busy = false
  }
</script>

<Modal title="Отложить сообщение" onclose={() => (app.modal = null)} width={380}>
  <div class="presets">
    <button class="chip" onclick={() => preset(30)}>Через 30 мин</button>
    <button class="chip" onclick={() => preset(60)}>Через час</button>
    <button class="chip" onclick={() => preset(180)}>Через 3 часа</button>
    <button class="chip" onclick={tomorrow9}>Завтра в 9:00</button>
  </div>
  <input class="field" type="datetime-local" bind:value={when} min={local(new Date())} />
  <label class="check"><input type="checkbox" bind:checked={quiet} /> Без звука</label>
  <p class="muted note">Сообщение отправит это устройство — оно должно быть включено и в сети в это время.</p>
  <div class="actions">
    <button class="btn flat" onclick={() => (app.modal = null)}>Отмена</button>
    <button class="btn" disabled={!valid || busy} onclick={go}>
      {valid ? 'Отправить ' + new Date(at).toLocaleString('ru', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }) : 'Выберите время в будущем'}
    </button>
  </div>
</Modal>

<style>
  .presets { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 12px; }
  .chip { padding: 6px 12px; border-radius: 16px; background: var(--bg-hover); font-size: 13px; }
  .chip:hover { background: var(--bg-active); }
  .field { width: 100%; color-scheme: dark; }
  .check { display: flex; gap: 10px; align-items: center; cursor: pointer; margin: 12px 0 6px; }
  .note { font-size: 13px; margin: 0 0 12px; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
