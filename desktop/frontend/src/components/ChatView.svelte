<script lang="ts">
  import { tick } from 'svelte'
  import { app } from '../lib/app.svelte'
  import { Messenger, type Message } from '../lib/api'
  import { dayLabel, plural } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Bubble from './Bubble.svelte'
  import Icon from './Icon.svelte'

  let text = $state('')
  let scroller: HTMLDivElement | undefined = $state()
  let input: HTMLTextAreaElement | undefined = $state()
  let fileInput: HTMLInputElement | undefined = $state()
  let dragging = $state(false)
  let memberCount = $state<number | null>(null)

  const chat = $derived(app.current)
  const messages = $derived(chat ? (app.messages[chat.id] ?? []) : [])

  type Row = { kind: 'day'; label: string; key: string } | { kind: 'msg'; m: Message; first: boolean; last: boolean; key: string }

  const rows = $derived.by(() => {
    const out: Row[] = []
    let prevDay = ''
    messages.forEach((m, i) => {
      const day = new Date(m.sentAt).toDateString()
      if (day !== prevDay) {
        out.push({ kind: 'day', label: dayLabel(m.sentAt), key: 'd' + day })
        prevDay = day
      }
      const prev = messages[i - 1]
      const next = messages[i + 1]
      const same = (a?: Message, b?: Message) =>
        !!a && !!b && a.senderId === b.senderId && a.outgoing === b.outgoing &&
        Math.abs(a.sentAt - b.sentAt) < 5 * 60000 && new Date(a.sentAt).toDateString() === new Date(b.sentAt).toDateString()
      out.push({ kind: 'msg', m, first: !same(prev, m), last: !same(m, next), key: m.id })
    })
    return out
  })

  // Scroll to the bottom when the chat changes or a message arrives while at the bottom.
  let lastChat = ''
  let lastCount = 0
  $effect(() => {
    const id = chat?.id ?? ''
    const count = messages.length
    const switched = id !== lastChat
    const nearBottom = scroller ? scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 160 : true
    const mine = count > lastCount && messages[count - 1]?.outgoing
    lastChat = id
    lastCount = count
    if (switched || nearBottom || mine) tick().then(() => scroller?.scrollTo({ top: scroller.scrollHeight }))
  })

  $effect(() => {
    const id = chat?.id
    const active = chat?.active
    memberCount = null
    if (id && active) Messenger.Members(id).then((ms) => (memberCount = new Set((ms ?? []).map((x) => x.userId)).size)).catch(() => {})
    input?.focus()
  })

  async function send() {
    const t = text
    if (!t.trim()) return
    text = ''
    resize()
    input?.focus()
    // On failure put the text back unless the user already typed something new.
    if (!(await app.send(t)) && !text) text = t
  }

  function key(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      send()
    }
  }

  function resize() {
    if (!input) return
    input.style.height = 'auto'
    input.style.height = Math.min(input.scrollHeight, 180) + 'px'
  }

  async function pick(files: FileList | null | undefined) {
    if (!files) return
    for (const f of Array.from(files)) await app.sendFile(f)
    if (fileInput) fileInput.value = ''
  }

  function drop(e: DragEvent) {
    e.preventDefault()
    dragging = false
    if (chat?.active) pick(e.dataTransfer?.files)
  }
</script>

{#if chat}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <section class="chat-view" ondragover={(e) => { e.preventDefault(); dragging = !!chat.active }} ondragleave={() => (dragging = false)} ondrop={drop}>
    <header>
      <button class="icon-btn back" aria-label="Назад" onclick={() => app.openChat(null)}><Icon name="back" /></button>
      <button class="title" onclick={() => (app.modal = { kind: 'chatInfo', chatId: chat.id })}>
        <Avatar name={chat.name} id={chat.id} size={42} />
        <div class="title-text">
          <div class="name">{chat.name}</div>
          <div class="status muted">
            {#if !chat.active}вы не участник этой группы
            {:else if !app.online}соединение…
            {:else if memberCount !== null}{memberCount} {plural(memberCount, 'участник', 'участника', 'участников')}
            {:else}&nbsp;{/if}
          </div>
        </div>
      </button>
      <button class="icon-btn" aria-label="Информация" onclick={() => (app.modal = { kind: 'chatInfo', chatId: chat.id })}><Icon name="dots" /></button>
    </header>

    <div class="messages" bind:this={scroller}>
      <div class="inner">
        <div class="e2e-note"><Icon name="lock" size={14} /> Сообщения защищены сквозным шифрованием (MLS)</div>
        {#each rows as r (r.key)}
          {#if r.kind === 'day'}
            <div class="day"><span>{r.label}</span></div>
          {:else}
            <Bubble m={r.m} first={r.first} last={r.last} group={true} />
          {/if}
        {/each}
      </div>
    </div>

    {#if chat.active}
      <footer>
        <div class="composer">
          <button class="icon-btn" aria-label="Прикрепить файл" onclick={() => fileInput?.click()}><Icon name="clip" /></button>
          <textarea bind:this={input} bind:value={text} rows="1" placeholder="Сообщение" onkeydown={key} oninput={resize}></textarea>
          <input type="file" multiple hidden bind:this={fileInput} onchange={(e) => pick((e.currentTarget as HTMLInputElement).files)} />
        </div>
        <button class="send" aria-label="Отправить" disabled={!text.trim()} onclick={send}><Icon name="send" /></button>
      </footer>
    {:else}
      <footer class="readonly muted">Вы не участник этой группы — доступна только история</footer>
    {/if}

    {#if dragging}
      <div class="dropzone"><div><Icon name="file" size={40} /><br />Отпустите, чтобы отправить</div></div>
    {/if}
  </section>
{:else}
  <section class="chat-view placeholder">
    <span class="pill">Выберите чат, чтобы начать общение</span>
  </section>
{/if}

<style>
  .chat-view {
    position: relative; display: flex; flex-direction: column; height: 100%; min-width: 0;
    background-color: var(--bg-chat);
    background-image:
      radial-gradient(circle at 20% 20%, rgba(82, 136, 193, 0.08), transparent 45%),
      radial-gradient(circle at 80% 70%, rgba(106, 90, 205, 0.07), transparent 50%);
  }
  header { display: flex; align-items: center; gap: 6px; height: 56px; padding: 0 8px 0 12px; background: var(--bg-header); border-bottom: 1px solid var(--border); }
  .back { display: none; }
  .title { flex: 1; display: flex; align-items: center; gap: 12px; text-align: left; min-width: 0; }
  .title-text { min-width: 0; }
  .name { font-weight: 600; font-size: 15px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .status { font-size: 13px; }
  .messages { flex: 1; overflow-y: auto; padding: 8px 16px; }
  .inner { max-width: 760px; margin: 0 auto; display: flex; flex-direction: column; min-height: 100%; justify-content: flex-end; }
  .e2e-note { align-self: center; display: flex; align-items: center; gap: 6px; font-size: 13px; color: #d4dde6;
    background: rgba(0, 0, 0, 0.3); border-radius: 12px; padding: 6px 12px; margin: 12px 0; text-align: center; }
  .day { display: flex; justify-content: center; margin: 10px 0; position: sticky; top: 4px; z-index: 1; }
  .day span { background: rgba(0, 0, 0, 0.35); color: #fff; font-size: 13px; font-weight: 500; padding: 4px 10px; border-radius: 12px; backdrop-filter: blur(4px); }
  footer { display: flex; align-items: flex-end; gap: 8px; padding: 8px 16px 14px; max-width: 792px; width: 100%; margin: 0 auto; }
  .composer { flex: 1; display: flex; align-items: flex-end; background: var(--bg-sidebar); border-radius: 18px; padding: 4px 6px; box-shadow: 0 1px 3px rgba(0,0,0,.3); }
  textarea { flex: 1; resize: none; border: 0; outline: 0; background: transparent; padding: 10px 6px; font-size: 15px; line-height: 1.35; max-height: 180px; }
  textarea::placeholder { color: var(--text-muted); }
  .send { width: 52px; height: 52px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center;
    flex-shrink: 0; transition: filter .15s, transform .15s; box-shadow: 0 1px 3px rgba(0,0,0,.3); }
  .send:hover:not(:disabled) { filter: brightness(1.1); }
  .send:disabled { background: var(--bg-sidebar); color: var(--text-muted); cursor: default; }
  .readonly { justify-content: center; padding: 18px; font-size: 14px; background: var(--bg-header); max-width: none; }
  .placeholder { align-items: center; justify-content: center; }
  .pill { background: rgba(0, 0, 0, 0.35); padding: 6px 14px; border-radius: 14px; font-size: 14px; color: #d4dde6; }
  .dropzone { position: absolute; inset: 12px; border: 2px dashed var(--accent); border-radius: 16px; background: rgba(14, 22, 33, 0.85);
    display: flex; align-items: center; justify-content: center; text-align: center; color: var(--accent-light); font-size: 18px; pointer-events: none; }
  @media (max-width: 720px) {
    .back { display: inline-flex; }
    .messages { padding: 8px 8px; }
    footer { padding: 6px 8px 10px; }
  }
</style>
