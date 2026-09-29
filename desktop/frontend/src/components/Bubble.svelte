<script lang="ts">
  import type { Message } from '../lib/api'
  import { Messenger } from '../lib/api'
  import { app } from '../lib/app.svelte'
  import { timeHM, fileSize, nameColor, downloadBase64 } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  let { m, first, last, group }: { m: Message; first: boolean; last: boolean; group: boolean } = $props()
  let downloading = $state(false)

  async function download() {
    if (downloading) return
    downloading = true
    const f = await app.try(() => Messenger.DownloadFile(m.chatId, m.seq))
    downloading = false
    if (f) downloadBase64(f.name, f.mime, f.base64)
  }
</script>

<div class="line" class:out={m.outgoing} class:last>
  {#if group && !m.outgoing}
    <div class="avatar-slot">{#if last}<Avatar name={m.sender} id={m.senderId} size={34} />{/if}</div>
  {/if}
  <div class="bubble" class:tail={last}>
    {#if group && first && !m.outgoing}
      <div class="sender" style="color:{nameColor(m.senderId)}">{m.sender}</div>
    {/if}
    {#if m.origin === 'shared'}
      <div class="origin" title="Пересланo другим участником, пока ваших устройств не было в сети">
        <Icon name="history" size={13} /> из истории участника
      </div>
    {/if}
    {#if m.kind === 'file' && m.file}
      <button class="file" onclick={download} title="Скачать">
        <span class="file-icon">{#if downloading}<span class="spinner"></span>{:else}<Icon name="down" size={22} />{/if}</span>
        <span class="file-meta">
          <span class="file-name">{m.file.name}</span>
          <span class="file-size">{fileSize(m.file.size)}</span>
        </span>
      </button>
    {:else}
      <span class="text">{m.text}</span>
    {/if}
    <span class="time">
      {timeHM(m.sentAt)}
      {#if m.outgoing}<span class="check"><Icon name="check" size={14} /></span>{/if}
    </span>
  </div>
</div>

<style>
  .line { display: flex; align-items: flex-end; gap: 8px; margin-bottom: 2px; }
  .line.last { margin-bottom: 8px; }
  .line.out { justify-content: flex-end; }
  .avatar-slot { width: 34px; flex-shrink: 0; }
  .bubble {
    position: relative; max-width: min(520px, 75%);
    background: var(--bubble-in); border-radius: var(--radius-bubble);
    padding: 6px 10px 7px 11px; line-height: 1.38; font-size: 15px;
    word-wrap: break-word; overflow-wrap: anywhere;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.25);
  }
  .out .bubble { background: var(--bubble-out); }
  .bubble.tail { border-bottom-left-radius: 4px; }
  .out .bubble.tail { border-bottom-left-radius: var(--radius-bubble); border-bottom-right-radius: 4px; }
  .bubble.tail::before {
    content: ''; position: absolute; bottom: 0; left: -7px; width: 12px; height: 16px;
    background: radial-gradient(circle at 0 0, transparent 11px, var(--bubble-in) 12px);
  }
  .out .bubble.tail::before { left: auto; right: -7px; background: radial-gradient(circle at 100% 0, transparent 11px, var(--bubble-out) 12px); }
  .text { white-space: pre-wrap; }
  .sender { font-weight: 600; font-size: 14px; margin-bottom: 2px; white-space: nowrap; }
  .origin { display: flex; align-items: center; gap: 4px; font-size: 12px; color: var(--text-bubble-muted); margin-bottom: 3px; white-space: nowrap; }
  .time { float: right; margin: 8px 0 -4px 12px; font-size: 12px; color: var(--text-muted); display: inline-flex; align-items: center; gap: 3px; white-space: nowrap; user-select: none; }
  .out .time { color: var(--text-bubble-muted); }
  .check { color: var(--accent-light); display: inline-flex; }
  .file { display: flex; align-items: center; gap: 12px; text-align: left; padding: 4px 0; white-space: normal; }
  .file-icon { width: 46px; height: 46px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .out .file-icon { background: #fff; color: var(--bubble-out); }
  .file-meta { display: flex; flex-direction: column; min-width: 0; }
  .file-name { font-weight: 500; overflow: hidden; text-overflow: ellipsis; }
  .file-size { font-size: 13px; color: var(--text-muted); }
  .out .file-size { color: var(--text-bubble-muted); }
  .spinner { width: 20px; height: 20px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: spin .8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg) } }
</style>
