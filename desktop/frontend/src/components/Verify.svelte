<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, type DeviceKey } from '../lib/api'
  import Modal from './Modal.svelte'
  import StatusBadge from './StatusBadge.svelte'

  let { username }: { username: string } = $props()
  let keys = $state<DeviceKey[]>([])
  let mine = $state('')
  const isMe = $derived(app.state?.account?.username === username)
  const unverified = $derived(keys.some((k) => k.status === 'seen'))

  async function load() {
    keys = (await app.try(() => Messenger.Keys(username))) ?? []
    mine = (await app.try(() => Messenger.MyFingerprint())) ?? ''
  }
  $effect(() => { load() })

  async function trust(deviceId: string) {
    const n = await app.try(() => Messenger.Trust(username, deviceId))
    if (n !== undefined) {
      app.toast(deviceId ? 'Новый ключ принят' : `Подтверждено устройств: ${n}`)
      await load()
      await app.refreshChats()
    }
  }
</script>

<Modal title={isMe ? 'Мои устройства' : `Ключи: ${username}`} onclose={() => (app.modal = null)} width={480}>
  <p class="muted intro">
    Сравните отпечатки с {isMe ? 'экранами других ваших устройств' : `тем, что видит ${username} у себя (Меню → Отпечаток)`}
    — лично или по голосовой связи. Если они совпадают, подтвердите.
  </p>

  {#each keys as k (k.deviceId)}
    <div class="key" class:conflict={k.status === 'conflict'}>
      <div class="key-head">
        <span class="muted small">устройство {k.deviceId.slice(0, 8)}{k.thisDevice ? ' · это устройство' : ''}</span>
        <StatusBadge status={k.thisDevice ? 'self' : k.status} />
      </div>
      <div class="fp mono">{k.fingerprint}</div>
      {#if k.status === 'conflict'}
        <div class="warn">
          Это устройство появилось с <b>другим ключом</b>. Это может быть атака «человек посередине».
          Отправка в группы с ним заблокирована.
        </div>
        <div class="fp mono new">новый: {k.conflictFingerprint}</div>
        <button class="btn danger" onclick={() => trust(k.deviceId)}>Принять новый ключ (сверено)</button>
      {/if}
    </div>
  {:else}
    <p class="muted">Нет известных устройств — у вас пока нет общих групп.</p>
  {/each}

  {#if !isMe}
    <div class="mine">
      <div class="muted small">Ваш отпечаток (для {username})</div>
      <div class="fp mono">{mine}</div>
    </div>
  {/if}

  {#if unverified}
    <button class="btn block" onclick={() => trust('')}>Отпечатки совпадают — подтвердить</button>
  {/if}
</Modal>

<style>
  .intro { margin: 0 0 14px; line-height: 1.45; font-size: 13px; }
  .key { background: var(--bg-chat); border-radius: 12px; padding: 12px 14px; margin-bottom: 10px; display: flex; flex-direction: column; gap: 8px; }
  .key.conflict { border: 1px solid rgba(236, 57, 66, 0.5); }
  .key-head { display: flex; justify-content: space-between; align-items: center; gap: 8px; }
  .fp { font-size: 17px; word-spacing: 6px; }
  .fp.new { color: #ff8a90; font-size: 15px; }
  .warn { color: #ff8a90; font-size: 13px; line-height: 1.4; }
  .small { font-size: 12px; }
  .mine { margin: 16px 0; padding: 12px 14px; border-radius: 12px; border: 1px dashed #2f3b49; display: flex; flex-direction: column; gap: 6px; }
</style>
