<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger } from '../lib/api'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'

  let { deviceId }: { deviceId: string } = $props()
  const req = $derived(app.links.find((l) => l.deviceId === deviceId))
  let busy = $state(false)

  async function approve() {
    busy = true
    const ok = await app.try(() => Messenger.ApproveLink(deviceId))
    busy = false
    if (ok !== undefined) {
      app.toast(`Устройство «${req?.name ?? ''}» подключено`)
      app.modal = null
      await app.refreshLinks()
    }
  }

  async function reject() {
    if (!confirm('Отклонить вход? Устройство будет отключено от аккаунта.')) return
    busy = true
    const ok = await app.try(() => Messenger.RejectLink(deviceId))
    busy = false
    if (ok !== undefined) {
      app.toast('Вход отклонён', 'warning')
      app.modal = null
      await app.refreshLinks()
    }
  }
</script>

<Modal title="Вход с нового устройства" onclose={() => (app.modal = null)}>
  {#if req}
    <div class="body">
      <div class="icon"><Icon name="device" size={36} /></div>
      <p><b>{req.name || 'Новое устройство'}</b> хочет войти в ваш аккаунт.</p>
      <p class="muted">Подтверждайте, только если это вы. Код на новом устройстве должен совпадать:</p>
      <div class="code mono">{req.code}</div>
      <p class="muted small">Устройство получит доступ к вашим чатам и истории сообщений.</p>
      <div class="actions">
        <button class="btn danger" disabled={busy} onclick={reject}>Это не я</button>
        <button class="btn" disabled={busy} onclick={approve}>{busy ? 'Подключение…' : 'Код совпадает'}</button>
      </div>
    </div>
  {:else}
    <p class="muted">Запрос уже обработан.</p>
  {/if}
</Modal>

<style>
  .body { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 10px; }
  .icon { width: 72px; height: 72px; border-radius: 50%; background: var(--accent); color: #fff; display: flex; align-items: center; justify-content: center; }
  p { margin: 0; line-height: 1.45; }
  .small { font-size: 13px; }
  .code { font-size: 28px; letter-spacing: 3px; word-spacing: 12px; background: var(--bg-chat); border-radius: 12px; padding: 12px 18px; }
  .actions { display: flex; gap: 10px; width: 100%; margin-top: 6px; }
  .actions .btn { flex: 1; }
</style>
