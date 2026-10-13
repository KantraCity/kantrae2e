<script lang="ts">
  // Telegram's side menu: profile, account switcher, main actions.
  import { app } from '../lib/app.svelte'
  import { fade, fly, slide } from 'svelte/transition'
  import { cubicOut } from 'svelte/easing'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  let showAccounts = $state(false)
  const others = $derived(app.accounts.filter((a) => !a.active))
  const acc = $derived(app.state?.account)
  const name = $derived(app.me?.name || acc?.name || acc?.username || '')

  function close() { app.modal = null }
  function key(e: KeyboardEvent) { if (e.key === 'Escape') close() }
</script>

<svelte:window onkeydown={key} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={close} transition:fade|global={{ duration: 180 }}></div>
<nav class="drawer" aria-label="Меню" transition:fly|global={{ x: -300, duration: 240, opacity: 1, easing: cubicOut }}>
  <div class="head">
    <button class="me-avatar" aria-label="Мой профиль" onclick={() => (app.modal = { kind: 'editProfile' })}>
      <Avatar name={name} id={acc?.userId} user={acc?.userId} photo={app.me?.avatarId} size={64} />
    </button>
    <button class="me" onclick={() => (showAccounts = !showAccounts)}>
      <div>
        <div class="name">{name}</div>
        <div class="handle">@{app.me?.username ?? acc?.username}</div>
      </div>
      <span class="chev" class:open={showAccounts}><Icon name="down" size={20} /></span>
    </button>
  </div>

  {#if showAccounts}
    <div class="accounts" transition:slide={{ duration: 180 }}>
      {#each others as a (a.id)}
        <button class="item" onclick={() => { close(); app.switchAccount(a.id) }}>
          <Avatar name={a.name || a.username} id={a.userId} size={34} />
          <span class="grow">{a.name || a.username}</span>
          {#if a.unread}<span class="badge">{a.unread}</span>{/if}
        </button>
      {/each}
      <button class="item" onclick={() => { close(); app.addAccount() }}>
        <span class="icon-slot"><Icon name="plus" size={22} /></span>
        <span class="grow">Добавить аккаунт</span>
      </button>
    </div>
    <div class="sep"></div>
  {/if}

  <button class="item" onclick={() => (app.modal = { kind: 'editProfile' })}><span class="icon-slot"><Icon name="user" size={22} /></span>Мой профиль</button>
  <button class="item" onclick={() => (app.modal = { kind: 'newGroup' })}><span class="icon-slot"><Icon name="group" size={22} /></span>Создать группу</button>
  <button class="item" onclick={() => (app.modal = { kind: 'newDirect' })}><span class="icon-slot"><Icon name="chat" size={22} /></span>Новый чат</button>
  <button class="item" onclick={() => (app.modal = { kind: 'settings' })}><span class="icon-slot"><Icon name="settings" size={22} /></span>Настройки</button>
  <div class="foot muted">Kantra · сквозное шифрование MLS</div>
</nav>

<style>
  .backdrop { position: fixed; inset: var(--titlebar-h, 0px) 0 0; background: rgba(0, 0, 0, 0.45); z-index: 40; }
  .drawer { position: fixed; top: var(--titlebar-h, 0px); bottom: 0; left: 0; width: min(300px, 85vw); background: var(--bg-sidebar); z-index: 41;
    display: flex; flex-direction: column; box-shadow: var(--shadow); overflow-y: auto; }
  .head { padding: 20px 18px 10px; background: var(--bg-header); display: flex; flex-direction: column; gap: 12px; }
  .me-avatar { align-self: flex-start; border-radius: 50%; }
  .me { display: flex; align-items: center; justify-content: space-between; text-align: left; }
  .name { font-weight: 600; font-size: 15px; }
  .handle { color: var(--text-muted); font-size: 14px; }
  .chev { color: var(--text-muted); transition: transform .15s; display: inline-flex; }
  .chev.open { transform: rotate(180deg); }
  .accounts { padding: 6px 0; }
  .item { display: flex; align-items: center; gap: 22px; padding: 11px 20px; text-align: left; font-size: 15px; width: 100%; }
  .item:hover { background: var(--bg-hover); }
  .accounts .item { gap: 16px; padding: 8px 18px; }
  .icon-slot { width: 34px; display: inline-flex; justify-content: center; color: var(--text-muted); }
  .grow { flex: 1; }
  .badge { background: var(--accent); color: #fff; font-size: 12px; font-weight: 600; min-width: 20px; height: 20px; padding: 0 6px; border-radius: 10px;
    display: flex; align-items: center; justify-content: center; }
  .sep { height: 1px; background: var(--border); margin: 4px 0; }
  .foot { margin-top: auto; padding: 16px 20px; font-size: 13px; }
  @keyframes slide { from { transform: translateX(-100%); } }
  @keyframes fade { from { opacity: 0; } }
</style>
