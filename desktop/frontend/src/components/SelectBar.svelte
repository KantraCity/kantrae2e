<script lang="ts">
  // Telegram's selection mode header: count, copy, forward, delete.
  import { app } from '../lib/app.svelte'
  import { plain } from '../lib/format'
  import { plural, timeHM } from '../lib/util'
  import { fly } from 'svelte/transition'
  import Icon from './Icon.svelte'

  let { chatId }: { chatId: string } = $props()
  const chat = $derived(app.chats.find((c) => c.id === chatId))
  const msgs = $derived((app.messages[chatId] ?? []).filter((m) => app.selected.includes(m.id)))
  const canAll = $derived(!!chat?.active && (chat.kind === 'direct' || msgs.every((m) => m.outgoing)))

  function done() { app.selected = [] }
  async function copy() {
    const many = msgs.length > 1
    const text = msgs.map((m) => {
      const body = plain(m.text || m.file?.caption || '') || (m.file ? `[${m.file.name || m.file.kind}]` : '')
      return many ? `${m.outgoing ? 'Вы' : m.sender}, [${timeHM(m.sentAt)}]\n${body}` : body
    }).join('\n\n')
    try {
      await navigator.clipboard.writeText(text)
      app.toast(many ? 'Сообщения скопированы' : 'Текст скопирован')
      done()
    } catch { app.toast('Не удалось скопировать', 'error') }
  }
  function forward() {
    const ids = msgs.map((m) => m.id)
    done()
    app.modal = { kind: 'forward', chatId, ids }
  }
  function del() {
    const ids = msgs.map((m) => m.id)
    done()
    app.modal = { kind: 'deleteMsg', chatId, ids, canAll }
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && done()} />

<header class="sel" in:fly={{ y: -8, duration: 140 }}>
  <button class="icon-btn" aria-label="Отменить выделение" onclick={done}><Icon name="close" /></button>
  <span class="n">{msgs.length} {plural(msgs.length, 'сообщение', 'сообщения', 'сообщений')}</span>
  <button class="act" onclick={copy}><Icon name="copy" size={20} /><span>Копировать</span></button>
  <button class="act" onclick={forward}><Icon name="forward" size={20} /><span>Переслать</span></button>
  <button class="act danger" onclick={del}><Icon name="trash" size={20} /><span>Удалить</span></button>
</header>

<style>
  .sel { display: flex; align-items: center; gap: 4px; height: 56px; padding: 0 8px; background: var(--bg-header); border-bottom: 1px solid var(--border); }
  .n { flex: 1; font-weight: 600; font-size: 15px; padding-left: 6px; }
  .act { display: inline-flex; align-items: center; gap: 6px; padding: 8px 12px; border-radius: 10px; font-size: 14px; font-weight: 500; color: var(--accent-light); }
  .act:hover { background: var(--bg-hover); }
  .danger { color: #e17076; }
  @media (max-width: 720px) { .act span { display: none; } }
</style>
