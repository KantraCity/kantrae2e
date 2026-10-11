<script lang="ts">
  import { app } from '../lib/app.svelte'
  import { Messenger, type LogoutInfo } from '../lib/api'
  import Modal from './Modal.svelte'

  let info = $state<LogoutInfo | null>(null)
  let busy = $state(false)
  $effect(() => { Messenger.GetLogoutInfo().then((i) => (info = i)).catch(() => {}) })
  const risky = $derived(!!info && info.lastDevice && !info.hasRecovery)

  async function logout() {
    busy = true
    const ok = await app.try(() => Messenger.Logout())
    busy = false
    if (ok !== undefined) {
      app.modal = null
      app.toast('Вы вышли из аккаунта на этом устройстве')
    }
  }
</script>

<Modal title="Выход" onclose={() => (app.modal = null)}>
  <div class="body">
    <p>Выйти из аккаунта <b>@{app.state?.account?.username}</b> на этом устройстве? Локальные данные (ключи и
      переписка) будут удалены с него, а устройство — отключено от аккаунта.</p>
    {#if risky}
      <p class="warn">
        Это ваше единственное устройство, и ключ восстановления не создан. После выхода историю сообщений
        на новом устройстве восстановить не получится. Сначала создайте ключ восстановления в настройках.
      </p>
    {:else if info}
      <p class="muted">Войти снова можно в любой момент: подтвердите вход на другом устройстве или ключом восстановления.</p>
    {/if}
    <div class="actions">
      {#if risky}<button class="btn flat" onclick={() => (app.modal = { kind: 'settings' })}>К ключу восстановления</button>{/if}
      <button class="btn danger" disabled={busy || !info} onclick={logout}>{busy ? 'Выход…' : 'Выйти'}</button>
    </div>
  </div>
</Modal>

<style>
  .body { display: flex; flex-direction: column; gap: 12px; }
  p { margin: 0; line-height: 1.45; }
  .warn { background: rgba(225, 112, 118, .12); color: #f19ca0; border-radius: 10px; padding: 10px 12px; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
