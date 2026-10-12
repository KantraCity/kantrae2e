<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, type Device, type Settings as Prefs } from '../lib/api'
  import { plural } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'
  import { checkCalls, type CallCheck } from '../lib/call.svelte'

  let checking = $state(false)
  let check = $state<CallCheck | null>(null)
  async function runCheck() {
    checking = true
    check = null
    try { check = await checkCalls() } finally { checking = false }
  }

  const acc = $derived(app.state?.account)
  let fingerprint = $state('')
  let devices = $state<Device[]>([])
  let backingUp = $state(false)
  let hasRecovery = $state<boolean | null>(null)
  let prefs = $state<Prefs | null>(null)
  const webPermission = $derived(prefs?.platform === 'web' && typeof Notification !== 'undefined' ? Notification.permission : null)
  let creating = $state(false)

  async function load() {
    fingerprint = (await app.try(() => Messenger.MyFingerprint())) ?? ''
    devices = (await app.try(() => Messenger.Devices())) ?? []
    hasRecovery = (await Messenger.HasRecovery().catch(() => null)) ?? null
    prefs = await Messenger.GetSettings().catch(() => null)
  }

  async function setPref(key: 'notifications' | 'preview' | 'background', value: boolean) {
    if (!prefs) return
    const next = { ...prefs, [key]: value }
    if (key === 'notifications' && value && next.platform === 'web' && typeof Notification !== 'undefined'
      && Notification.permission === 'default') {
      await Notification.requestPermission()
    }
    if ((await app.try(() => Messenger.SetSettings(next))) !== undefined) prefs = next
  }

  async function system(what: string) {
    await Messenger.OpenSystemSettings(what)
    // Come back to fresh state after the user returns from system settings.
    setTimeout(load, 1500)
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
      <Avatar name={app.me?.name || acc.name || acc.username} id={acc.userId} user={acc.userId} photo={app.me?.avatarId} size={64} />
      <div class="grow">
        <div class="name">{app.me?.name || acc.name || acc.username}</div>
        <div class="muted small">@{app.me?.username ?? acc.username} · {app.online ? 'в сети' : 'нет соединения'}</div>
      </div>
      <button class="btn flat" onclick={() => (app.modal = { kind: 'editProfile' })}>Изменить</button>
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

    {#if prefs}
      <div class="section">
        <div class="label"><Icon name="bell" size={18} /> Уведомления</div>
        <label class="switch-row">
          <span>Уведомления о сообщениях</span>
          <input type="checkbox" class="switch" checked={prefs.notifications} onchange={(e) => setPref('notifications', e.currentTarget.checked)} />
        </label>
        <label class="switch-row">
          <span>Показывать текст сообщения</span>
          <input type="checkbox" class="switch" checked={prefs.preview} disabled={!prefs.notifications} onchange={(e) => setPref('preview', e.currentTarget.checked)} />
        </label>
        {#if prefs.platform === 'android'}
          <label class="switch-row">
            <span>Работать в фоне<br /><span class="muted small">одно соединение с сервером, без Google</span></span>
            <input type="checkbox" class="switch" checked={prefs.background} onchange={(e) => setPref('background', e.currentTarget.checked)} />
          </label>
          {#if !prefs.notificationsAllowed}
            <button class="warn-row" onclick={() => system('notifications')}><Icon name="warning" size={18} /> Уведомления запрещены в системе — разрешить</button>
          {/if}
          {#if prefs.background && !prefs.batteryUnrestricted}
            <button class="warn-row" onclick={() => system('battery')}>
              <Icon name="warning" size={18} />
              <span>Снять ограничение батареи — иначе в режиме сна Android отключает сеть приложению и сообщения придут с задержкой</span>
            </button>
          {/if}
        {:else if prefs.platform === 'desktop'}
          <label class="switch-row">
            <span>Сворачивать в трей при закрытии окна</span>
            <input type="checkbox" class="switch" checked={prefs.background} onchange={(e) => setPref('background', e.currentTarget.checked)} />
          </label>
        {:else if webPermission === 'denied'}
          <p class="muted small">Браузер запретил уведомления для этой страницы — разрешите их в настройках сайта.</p>
        {/if}
      </div>
    {/if}

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

    <div class="section">
      <div class="label"><Icon name="phone" size={18} /> Звонки</div>
      <p class="muted small">Проверка связи с сервером звонков (STUN/TURN). Занимает несколько секунд.</p>
      <button class="btn flat" disabled={checking} onclick={runCheck}>{checking ? 'Проверка…' : 'Проверить звонки'}</button>
      {#if check}
        <div class="check-res">
          <div class:ok={check.relay} class:bad={!check.relay}>
            {check.relay ? '✓ TURN работает — звонки соединятся из любой сети' : '✗ TURN недоступен — звонки соединятся только в одной сети'}
          </div>
          {#if check.relay && check.relayIp}<div class="muted small">адрес пересылки: {check.relayIp}</div>{/if}
          {#if check.relay && check.relayIp && /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(check.relayIp)}
            <div class="bad small">Сервер выдаёт приватный адрес: добавьте TURN_EXTERNAL_IP=&lt;публичный IP&gt; в deploy/.env</div>
          {/if}
          <div class="muted small">STUN: {check.srflx ? 'да' : 'нет'} · серверы: {check.servers.join(', ') || '—'}</div>
          {#each check.errors as e}<div class="bad small">{e}</div>{/each}
        </div>
      {/if}
    </div>

    <div class="section">
      <button class="btn danger" onclick={() => (app.modal = { kind: 'logout' })}><Icon name="logout" size={18} /> Выйти из аккаунта</button>
    </div>

    {#if app.state?.webMode}
      <p class="muted small">Веб-режим: этот процесс хранит ваши ключи. Не открывайте его другим людям.</p>
    {/if}
  {/if}
</Modal>

<style>
  .profile { display: flex; gap: 16px; align-items: center; margin-bottom: 8px; }
  .name { font-size: 18px; font-weight: 600; }
  .grow { flex: 1; min-width: 0; }
  .small { font-size: 13px; }
  .section { border-top: 1px solid #243140; padding: 14px 0 6px; display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
  .label { display: flex; gap: 8px; align-items: center; font-weight: 500; color: var(--accent-light); }
  .fp { font-size: 17px; word-spacing: 6px; }
  .device { width: 100%; display: flex; justify-content: space-between; align-items: center; gap: 8px; padding: 4px 0; }
  .device.revoked { opacity: .5; }
  p { margin: 0; }
  .switch-row { width: 100%; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 6px 0; cursor: pointer; }
  .switch { appearance: none; width: 36px; height: 20px; border-radius: 10px; background: #3a4a5c; position: relative; flex-shrink: 0; cursor: pointer; transition: background .15s; }
  .switch::after { content: ''; position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: 50%; background: #fff; transition: transform .15s; }
  .switch:checked { background: var(--accent); }
  .switch:checked::after { transform: translateX(16px); }
  .switch:disabled { opacity: .4; }
  .warn-row { width: 100%; display: flex; align-items: flex-start; gap: 10px; text-align: left; padding: 8px 10px; border-radius: 10px; background: rgba(250, 167, 116, .12); color: #faa774; font-size: 14px; }
  .check-res { margin-top: 10px; display: flex; flex-direction: column; gap: 4px; font-size: 14px; }
  .check-res .ok { color: var(--success); }
  .check-res .bad { color: #ff8a80; }
</style>
