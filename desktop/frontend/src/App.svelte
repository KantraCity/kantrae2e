<script lang="ts">
  import { onMount } from 'svelte'
  import { app } from './lib/app.svelte'
  import Auth from './components/Auth.svelte'
  import ChatInfo from './components/ChatInfo.svelte'
  import ChatView from './components/ChatView.svelte'
  import ApproveLink from './components/ApproveLink.svelte'
  import Drawer from './components/Drawer.svelte'
  import EditProfile from './components/EditProfile.svelte'
  import Logout from './components/Logout.svelte'
  import NewDirect from './components/NewDirect.svelte'
  import NewGroup from './components/NewGroup.svelte'
  import PendingDevice from './components/PendingDevice.svelte'
  import RecoveryPhrase from './components/RecoveryPhrase.svelte'
  import Relogin from './components/Relogin.svelte'
  import Settings from './components/Settings.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import Toasts from './components/Toasts.svelte'
  import Verify from './components/Verify.svelte'

  onMount(() => { app.init() })
</script>

{#if !app.loaded}
  <div class="splash"><img src="/icon.svg" alt="" width="96" /></div>
{:else if !app.loggedIn}
  <Auth />
{:else if app.pending}
  <PendingDevice />
{:else}
  <div class="layout" class:chat-open={!!app.currentId}>
    <Sidebar />
    <ChatView />
  </div>
{/if}

{#if app.pending}{#if app.modal?.kind === 'relogin'}<Relogin />{/if}
{:else if app.modal?.kind === 'drawer'}<Drawer />
{:else if app.modal?.kind === 'editProfile'}<EditProfile />
{:else if app.modal?.kind === 'logout'}<Logout />
{:else if app.modal?.kind === 'newGroup'}<NewGroup />
{:else if app.modal?.kind === 'newDirect'}<NewDirect />
{:else if app.modal?.kind === 'approveLink'}<ApproveLink deviceId={app.modal.deviceId} />
{:else if app.modal?.kind === 'recovery'}<RecoveryPhrase phrase={app.modal.phrase} />
{:else if app.modal?.kind === 'chatInfo'}<ChatInfo chatId={app.modal.chatId} />
{:else if app.modal?.kind === 'verify'}<Verify username={app.modal.username} />
{:else if app.modal?.kind === 'settings'}<Settings />
{:else if app.modal?.kind === 'relogin'}<Relogin />
{/if}

<Toasts />

<style>
  .splash { height: 100%; display: flex; align-items: center; justify-content: center; background: var(--bg-sidebar); }
  .layout { height: 100%; display: grid; grid-template-columns: minmax(300px, 380px) 1fr; }
  @media (max-width: 720px) {
    .layout { grid-template-columns: 1fr; }
    .layout.chat-open :global(.sidebar) { display: none; }
    .layout:not(.chat-open) :global(.chat-view) { display: none; }
  }
</style>
