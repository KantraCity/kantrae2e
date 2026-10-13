<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, type Device, type Settings as Prefs, type StorageInfo, type UserHit } from '../lib/api'
  import { plural } from '../lib/util'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Modal from './Modal.svelte'
  import { checkCalls, type CallCheck } from '../lib/call.svelte'
  import { setThemePref, themePref, type ThemePref } from '../lib/theme'

  // ---- privacy: last seen, block list ----
  let seen = $state('')
  let blocked = $state<UserHit[]>([])
  $effect(() => {
    Messenger.LastSeenPrivacy().then((v) => (seen = v)).catch(() => {})
    Messenger.Blocked().then((l) => (blocked = l ?? [])).catch(() => {})
  })
  async function setSeen(v: string) {
    const prev = seen
    seen = v
    const ok = await app.try(() => Messenger.SetLastSeenPrivacy(v))
    if (ok === undefined) seen = prev
  }
  async function unblock(u: UserHit) {
    if (await app.setBlocked(u.userId, false)) blocked = blocked.filter((x) => x.userId !== u.userId)
  }

  // ---- appearance, passcode ----
  let theme = $state<ThemePref>(themePref())

  // ---- data and storage ----
  let storage = $state<StorageInfo | null>(null)
  const loadStorage = async () => (storage = await Messenger.StorageInfo().catch(() => null))
  loadStorage()
  const LIMITS: [number, string][] = [[512 << 20, '512 МБ'], [1 << 30, '1 ГБ'], [2 << 30, '2 ГБ'], [5 * 2 ** 30, '5 ГБ'], [0, 'Без ограничения']]
  function size(n: number) {
    if (n < 1 << 20) return `${Math.max(0, Math.round(n / 1024))} КБ`
    if (n < 1 << 30) return `${(n / 2 ** 20).toFixed(n < 10 * 2 ** 20 ? 1 : 0)} МБ`
    return `${(n / 2 ** 30).toFixed(1)} ГБ`
  }
  async function setLimit(v: number) {
    await app.try(() => Messenger.SetCacheLimit(v))
    await loadStorage()
  }
  async function clearCache() {
    if (!confirm('Удалить загруженные фото, видео и файлы с этого устройства? Сообщения останутся, медиа загрузятся снова при открытии.')) return
    await app.try(() => Messenger.ClearCache())
    await loadStorage()
    app.toast('Кэш очищен')
  }
  function pickTheme(v: ThemePref) {
    theme = v
    setThemePref(v)
  }
  async function setAutoLock(min: number) {
    if ((await app.try(() => Messenger.SetAutoLock(min))) !== undefined) app.passcode.autoLock = min
  }

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
        {:else if prefs.platform === 'ios'}
          <label class="switch-row">
            <span>Работать в фоне<br /><span class="muted small">уведомления приходят сразу, без серверов Apple; расходует больше батареи</span></span>
            <input type="checkbox" class="switch" checked={prefs.background} onchange={(e) => setPref('background', e.currentTarget.checked)} />
          </label>
          {#if !prefs.notificationsAllowed}
            <button class="warn-row" onclick={() => system('notifications')}><Icon name="warning" size={18} /> Уведомления запрещены в настройках iPhone — разрешить</button>
          {/if}
          {#if prefs.background}
            <p class="muted small">Не закрывайте Kantra смахиванием из списка приложений — тогда iPhone завершит её до следующего запуска.</p>
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
      <div class="label"><Icon name="lock" size={18} /> Конфиденциальность</div>
      <div class="select-row">
        <span>Кто видит время захода</span>
        <select class="field small-select" value={seen} onchange={(e) => setSeen(e.currentTarget.value)} disabled={!seen}>
          <option value="everybody">Все</option>
          <option value="chats">Мои чаты</option>
          <option value="nobody">Никто</option>
        </select>
      </div>
      {#if seen === 'nobody'}<p class="muted small">Тогда и вы не видите, когда заходили другие (как в Telegram).</p>{/if}
      <div class="sublabel">Чёрный список{blocked.length ? ` · ${blocked.length}` : ''}</div>
      {#each blocked as u (u.userId)}
        <div class="device">
          <div class="who-row"><Avatar name={u.name || u.username} id={u.userId} user={u.userId} photo={u.avatarId} size={34} />
            <div><div>{u.name || u.username}</div><div class="muted small">@{u.username}</div></div></div>
          <button class="btn flat" onclick={() => unblock(u)}>Разблокировать</button>
        </div>
      {:else}
        <p class="muted small">Пусто. Заблокировать можно в профиле человека (Информация → Заблокировать).</p>
      {/each}
    </div>

    <div class="section">
      <div class="label"><Icon name="lock" size={18} /> Код-пароль</div>
      {#if app.passcode.enabled}
        <div class="select-row">
          <span>Автоблокировка</span>
          <select class="field small-select" value={String(app.passcode.autoLock)} onchange={(e) => setAutoLock(Number(e.currentTarget.value))}>
            <option value="0">Только при запуске</option>
            <option value="1">Через 1 минуту</option>
            <option value="5">Через 5 минут</option>
            <option value="60">Через 1 час</option>
          </select>
        </div>
        <p class="muted small">Пока приложение заблокировано, уведомления приходят без текста.</p>
        <div class="row-actions">
          <button class="btn flat" onclick={() => { app.modal = null; app.lock() }}>Заблокировать сейчас</button>
          <button class="btn flat" onclick={() => (app.modal = { kind: 'passcode', mode: 'change' })}>Изменить</button>
          <button class="btn danger" onclick={() => (app.modal = { kind: 'passcode', mode: 'off' })}>Отключить</button>
        </div>
      {:else}
        <p class="muted small">Четыре цифры для входа в приложение на этом устройстве; приложение блокируется само, когда вы его не используете.</p>
        <button class="btn flat" onclick={() => (app.modal = { kind: 'passcode', mode: 'set' })}>Включить код-пароль</button>
      {/if}
    </div>

    <div class="section">
      <div class="label"><Icon name="palette" size={18} /> Оформление</div>
      <div class="theme-pick">
        {#each [['dark', 'Тёмная'], ['light', 'Светлая'], ['system', 'Как в системе']] as [v, label]}
          <button class="theme" class:on={theme === v} onclick={() => pickTheme(v as ThemePref)}>
            <span class="swatch {v}"><span class="b1"></span><span class="b2"></span></span>{label}
          </button>
        {/each}
      </div>
    </div>

    {#if storage}
      <div class="section">
        <div class="label"><Icon name="file" size={18} /> Данные и память</div>
        <div class="kv"><span>Загруженные медиа</span><span class="muted">{size(storage.cacheBytes)}</span></div>
        <div class="kv"><span>Сообщения и ключи</span><span class="muted">{size(storage.dataBytes)}</span></div>
        <label class="kv">
          <span>Хранить медиа не больше</span>
          <select class="limit" value={storage.cacheLimit} onchange={(e) => setLimit(Number(e.currentTarget.value))}>
            {#each LIMITS as [v, label]}<option value={v}>{label}</option>{/each}
          </select>
        </label>
        <p class="muted small">Старые медиа удаляются с устройства первыми и снова загружаются при открытии.</p>
        <button class="btn flat" onclick={clearCache} disabled={!storage.cacheBytes}>Очистить кэш</button>
      </div>
    {/if}

    <div class="section">
      <div class="label"><Icon name="download" size={18} /> Версия и обновления</div>
      {#if app.update}
        {@const u = app.update}
        <p class="muted small">Kantra {u.current}{u.state === 'off' ? ' (сборка без автообновления)' : ''}</p>
        {#if u.state === 'checking'}<p class="muted small">Проверяем обновления…</p>
        {:else if u.state === 'downloading'}<p class="muted small">Загружается версия {u.latest}…</p>
        {:else if u.state === 'ready'}
          <p class="small">Версия {u.latest} загружена{u.install === 'restart' ? ' — установится при следующем запуске' : ''}.</p>
          <button class="btn flat" onclick={() => app.applyUpdate()}>{u.install === 'restart' ? 'Перезапустить сейчас' : 'Установить'}</button>
        {:else if u.state === 'manual'}
          <p class="small">Вышла версия {u.latest}: обновите Kantra в AltStore / SideStore.</p>
          <button class="btn flat" onclick={() => app.applyUpdate()}>Страница версии</button>
        {:else if u.state === 'failed'}
          <p class="muted small">Не удалось обновиться: {u.error}</p>
          <button class="btn flat" onclick={() => Messenger.CheckForUpdate()}>Проверить ещё раз</button>
        {:else if u.state === 'none'}
          <p class="muted small">Установлена последняя версия.</p>
          <button class="btn flat" onclick={() => Messenger.CheckForUpdate()}>Проверить обновления</button>
        {/if}
      {/if}
      <p class="muted small">Что-то работает не так? Отчёт об ошибках — версия, устройство и журнал работы приложения, без сообщений и контактов.</p>
      <div class="row-btns">
        <button class="btn flat" onclick={() => app.copyReport()}>Скопировать отчёт</button>
        <button class="btn flat" onclick={() => app.saveReport()}>Сохранить файлом</button>
      </div>
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

    <div class="section">
      <div class="label"><Icon name="phone" size={18} /> Звонки</div>
      <p class="muted small">Проверка связи с сервером звонков (STUN/TURN). Занимает несколько секунд.</p>
      <button class="btn flat" disabled={checking} onclick={runCheck}>{checking ? 'Проверка…' : 'Проверить звонки'}</button>
      {#if check}
        <div class="check-res">
          {#if check.error}
            <div class="bad">{check.error}</div>
          {:else}
            <div class:ok={check.relay} class:bad={!check.relay}>
              {check.relay ? '✓ Звонки соединятся из любой сети' : '✗ Пересылка (TURN) недоступна — звонки соединятся не из всех сетей'}
            </div>
            {#each check.probes as p}
              <div class="small probe" class:ok={p.ok} class:bad={!p.ok}>
                {p.ok ? '✓' : '✗'} {p.url.replace(/\?transport=/, ' по ').replace(/^(stun|turn):/, (m) => m.toUpperCase().slice(0, -1) + ' ')}
                <span class="muted">{p.ok ? (p.ip ? `· ${p.ip}` : '') : `· ${p.err}`}</span>
              </div>
            {/each}
            {#if check.relay && /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(check.relayIp)}
              <div class="bad small">Сервер выдаёт приватный адрес {check.relayIp}: проверьте TURN_EXTERNAL_IP в deploy/.env</div>
            {/if}
          {/if}
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
  .section { border-top: 1px solid var(--border); padding: 14px 0 6px; display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
  .label { display: flex; gap: 8px; align-items: center; font-weight: 500; color: var(--accent-light); }
  .fp { font-size: 17px; word-spacing: 6px; }
  .device { width: 100%; display: flex; justify-content: space-between; align-items: center; gap: 8px; padding: 4px 0; }
  .device.revoked { opacity: .5; }
  p { margin: 0; }
  .switch-row { width: 100%; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 6px 0; cursor: pointer; }
  .switch { appearance: none; width: 36px; height: 20px; border-radius: 10px; background: var(--quiet); position: relative; flex-shrink: 0; cursor: pointer; transition: background .15s; }
  .switch::after { content: ''; position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: 50%; background: #fff; transition: transform .15s; }
  .switch:checked { background: var(--accent); }
  .switch:checked::after { transform: translateX(16px); }
  .switch:disabled { opacity: .4; }
  .warn-row { width: 100%; display: flex; align-items: flex-start; gap: 10px; text-align: left; padding: 8px 10px; border-radius: 10px; background: rgba(250, 167, 116, .12); color: #faa774; font-size: 14px; }
  .check-res { margin-top: 10px; display: flex; flex-direction: column; gap: 4px; font-size: 14px; }
  .check-res .ok { color: var(--success); }
  .check-res .bad { color: #ff8a80; }
  .row-actions { display: flex; flex-wrap: wrap; gap: 4px; }
  .theme-pick { display: flex; gap: 10px; flex-wrap: wrap; }
  .theme { display: flex; flex-direction: column; align-items: center; gap: 6px; padding: 8px; border-radius: 12px; font-size: 13px; border: 2px solid transparent; }
  .theme.on { border-color: var(--accent); }
  .swatch { width: 84px; height: 56px; border-radius: 8px; display: flex; flex-direction: column; justify-content: center; gap: 6px; padding: 8px; }
  .swatch.dark { background: #0e1621; }
  .swatch.light { background: #d9e3ea; }
  .swatch.system { background: linear-gradient(135deg, #0e1621 50%, #d9e3ea 50%); }
  .b1, .b2 { height: 10px; border-radius: 5px; width: 60%; }
  .b1 { background: #182533; }
  .b2 { align-self: flex-end; background: #2b5278; }
  .light .b1 { background: #fff; }
  .light .b2 { background: #e1f9cf; }
  .system .b1 { background: #182533; }
  .system .b2 { background: #e1f9cf; }
  .select-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 4px 0; }
  .small-select { width: auto; padding: 6px 10px; background: var(--bg-input); }
  .sublabel { margin: 12px 0 4px; font-size: 13px; color: var(--text-muted); }
  .who-row { display: flex; align-items: center; gap: 10px; }
  .row-btns { display: flex; flex-wrap: wrap; gap: 4px; }
  .kv { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 6px 0; }
  .limit { background: var(--bg-input); color: var(--text); border: 1px solid var(--field-border); border-radius: 8px; padding: 6px 8px; font: inherit; }
</style>
