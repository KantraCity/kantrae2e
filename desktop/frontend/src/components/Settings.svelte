<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, type Device } from '../lib/api'
  import { plural } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'

  const acc = $derived(app.state?.account)
  let fingerprint = $state('')
  let devices = $state<Device[]>([])
  let backingUp = $state(false)

  async function load() {
    fingerprint = (await app.try(() => Messenger.MyFingerprint())) ?? ''
    devices = (await app.try(() => Messenger.Devices())) ?? []
  }
  $effect(() => { load() })

  async function revoke(d: Device) {
    if (!confirm(`Отключить устройство «${d.name}»? Оно будет удалено из всех групп и не сможет вернуться.`)) return
    const n = await app.try(() => Messenger.RevokeDevice(d.id))
    if (n !== undefined) {
      app.toast(`Устройство отключено и удалено из ${n} ${plural(n, 'группы', 'групп', 'групп')}`)
      await load()
    }
  }

  async function backup() {
    backingUp = true
    const n = await app.try(() => Messenger.Backup())
    backingUp = false
    if (n !== undefined) app.toast(n ? `Сохранено сообщений: ${n}` : 'Всё уже сохранено')
  }
</script>

<Modal title="Настройки" onclose={() => (app.modal = null)} width={460}>
  {#if acc}
    <div class="profile">
      <Avatar name={acc.username} id={acc.userId} size={64} />
      <div>
        <div class="name">{acc.username}</div>
        <div class="muted small">{app.online ? 'в сети' : 'нет соединения'} · {app.state?.serverUrl}</div>
      </div>
    </div>

    <div class="section">
      <div class="label"><Icon name="shield" size={18} /> Отпечаток этого устройства</div>
      <div class="fp mono">{fingerprint}</div>
      <button class="btn flat" onclick={() => (app.modal = { kind: 'verify', username: acc.username })}>Сверить мои устройства</button>
    </div>

    <div class="section">
      <div class="label"><Icon name="device" size={18} /> Устройства</div>
      {#each devices as d (d.id)}
        <div class="device" class:revoked={d.revoked}>
          <div>
            <div>{d.name}{d.thisDevice ? ' · это устройство' : ''}</div>
            <div class="muted small">{d.revoked ? 'отключено' : 'с ' + new Date(d.createdAt).toLocaleDateString('ru-RU')}</div>
          </div>
          {#if !d.thisDevice && !d.revoked}
            <button class="btn danger" onclick={() => revoke(d)}>Отключить</button>
          {/if}
        </div>
      {/each}
    </div>

    <div class="section">
      <div class="label"><Icon name="cloud" size={18} /> Резервная копия истории</div>
      <p class="muted small">Сообщения автоматически сохраняются в зашифрованном виде (ключ — ваша seed-фраза).</p>
      <button class="btn flat" disabled={backingUp} onclick={backup}>{backingUp ? 'Сохранение…' : 'Сохранить сейчас'}</button>
    </div>

    {#if app.state?.webMode}
      <p class="muted small">Веб-режим: этот процесс хранит ваши ключи. Не открывайте его другим людям.</p>
    {/if}
  {/if}
</Modal>

<style>
  .profile { display: flex; gap: 16px; align-items: center; margin-bottom: 8px; }
  .name { font-size: 18px; font-weight: 600; }
  .small { font-size: 13px; }
  .section { border-top: 1px solid #243140; padding: 14px 0 6px; display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
  .label { display: flex; gap: 8px; align-items: center; font-weight: 500; color: var(--accent-light); }
  .fp { font-size: 17px; word-spacing: 6px; }
  .device { width: 100%; display: flex; justify-content: space-between; align-items: center; gap: 8px; padding: 4px 0; }
  .device.revoked { opacity: .5; }
  p { margin: 0; }
</style>
