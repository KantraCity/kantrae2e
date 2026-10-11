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
  let hasRecovery = $state<boolean | null>(null)
  let creating = $state(false)

  async function load() {
    fingerprint = (await app.try(() => Messenger.MyFingerprint())) ?? ''
    devices = (await app.try(() => Messenger.Devices())) ?? []
    hasRecovery = (await Messenger.HasRecovery().catch(() => null)) ?? null
  }

  async function createRecovery() {
    if (hasRecovery && !confirm('Создать новый ключ восстановления? Старый перестанет работать.')) return
    creating = true
    const phrase = await app.try(() => Messenger.SetupRecovery())
    creating = false
    if (phrase) app.modal = { kind: 'recovery', phrase }
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
      {#each app.links as l (l.deviceId)}
        <div class="device">
          <div>
            <div>{l.name || 'Новое устройство'}</div>
            <div class="muted small">ждёт подтверждения · код {l.code}</div>
          </div>
          <button class="btn" onclick={() => (app.modal = { kind: 'approveLink', deviceId: l.deviceId })}>Проверить</button>
        </div>
      {/each}
      {#each devices.filter((d) => !app.links.some((l) => l.deviceId === d.id)) as d (d.id)}
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
      <div class="label"><Icon name="key" size={18} /> Ключ восстановления</div>
      <p class="muted small">
        {#if hasRecovery === null}…
        {:else if hasRecovery}Создан. Нужен, только если вы потеряете все устройства.
        {:else}Не создан. Новые устройства подтверждаются с этого — но если потеряете все,
          историю можно будет вернуть только по ключу из 12 слов.{/if}
      </p>
      <button class="btn flat" disabled={creating || hasRecovery === null} onclick={createRecovery}>
        {creating ? 'Создание…' : hasRecovery ? 'Создать новый ключ' : 'Создать ключ восстановления'}
      </button>
    </div>

    <div class="section">
      <div class="label"><Icon name="cloud" size={18} /> Резервная копия истории</div>
      <p class="muted small">Сообщения автоматически сохраняются на сервере в зашифрованном виде; ключ есть только на ваших устройствах.</p>
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
