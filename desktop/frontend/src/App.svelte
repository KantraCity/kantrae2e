<script lang="ts">
  import { onMount } from 'svelte'
  import { app } from './lib/app.svelte'
  import Auth from './components/Auth.svelte'
  import ChatInfo from './components/ChatInfo.svelte'
  import ChatView from './components/ChatView.svelte'
  import ApproveLink from './components/ApproveLink.svelte'
  import DeleteMessages from './components/DeleteMessages.svelte'
  import Drawer from './components/Drawer.svelte'
  import Forward from './components/Forward.svelte'
  import MessageMenu from './components/MessageMenu.svelte'
  import SendFiles from './components/SendFiles.svelte'
  import Viewer from './components/Viewer.svelte'
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
  import TitleBar from './components/TitleBar.svelte'
  import ChatMenu from './components/ChatMenu.svelte'
  import DeleteChat from './components/DeleteChat.svelte'
  import CallView from './components/CallView.svelte'
  import { checkPendingCall } from './lib/call.svelte'

  onMount(() => { app.init().then(checkPendingCall) })

  // Resizable chat list (desktop), remembered on this device.
  const SIDEBAR_KEY = 'kantra.sidebar'
  let sidebarW = $state(Number((() => { try { return localStorage.getItem(SIDEBAR_KEY) } catch { return null } })()) || 360)
  let resizing = $state(false)
  function startResize(e: PointerEvent) {
    resizing = true
    ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  }
  function doResize(e: PointerEvent) {
    if (resizing) sidebarW = Math.round(Math.max(260, Math.min(e.clientX, Math.min(640, window.innerWidth - 360))))
  }
  function endResize() {
    if (!resizing) return
    resizing = false
    try { localStorage.setItem(SIDEBAR_KEY, String(sidebarW)) } catch { /* storage unavailable */ }
  }
</script>

<div class="root">
{#if app.state?.titlebar === 'custom' || app.state?.titlebar === 'mac'}<TitleBar style={app.state.titlebar} />{/if}
<div class="content">
{#if !app.loaded}
  <div class="splash"><img src="/icon.svg" alt="" width="96" /></div>
{:else if !app.loggedIn}
  <Auth />
{:else if app.pending}
  <PendingDevice />
{:else}
  <div class="layout" class:chat-open={!!app.currentId} class:resizing style="--sidebar-w:{sidebarW}px">
    <Sidebar />
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="resizer" title="Перетащите, чтобы изменить ширину" onpointerdown={startResize} onpointermove={doResize}
      onpointerup={endResize} onpointercancel={endResize} ondblclick={() => { sidebarW = 360; endResize() }}></div>
    <ChatView />
  </div>
{/if}
</div>
</div>

{#if app.pending}{#if app.modal?.kind === 'relogin'}<Relogin />{/if}
{:else if app.modal?.kind === 'drawer'}<Drawer />
{:else if app.modal?.kind === 'sendFiles'}<SendFiles files={app.modal.files} />
{:else if app.modal?.kind === 'viewer'}<Viewer m={app.modal.m} />
{:else if app.modal?.kind === 'forward'}<Forward chatId={app.modal.chatId} ids={app.modal.ids} />
{:else if app.modal?.kind === 'deleteMsg'}<DeleteMessages chatId={app.modal.chatId} ids={app.modal.ids} canAll={app.modal.canAll} />
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
{:else if app.modal?.kind === 'deleteChat'}<DeleteChat chatId={app.modal.chatId} action={app.modal.action} />
{/if}

{#if app.menu}<MessageMenu />{/if}
{#if app.chatMenu}<ChatMenu />{/if}

<CallView />

<Toasts />

<style>
  .root { height: 100%; display: flex; flex-direction: column; }
  .content { flex: 1; min-height: 0; position: relative; }
  .splash { height: 100%; display: flex; align-items: center; justify-content: center; background: var(--bg-sidebar); }
  .layout { height: 100%; display: grid; grid-template-columns: var(--sidebar-w) 0 1fr; grid-template-rows: minmax(0, 1fr); overflow: hidden; }
  .layout > :global(*) { min-height: 0; min-width: 0; }
  .layout.resizing { cursor: col-resize; user-select: none; }
  .resizer { position: relative; z-index: 4; width: 8px; margin-left: -4px; cursor: col-resize; height: 100%; }
  .resizer:hover, .layout.resizing .resizer { background: rgba(82, 136, 193, .35); }
  @media (max-width: 720px) {
    .layout { grid-template-columns: 1fr; }
    .resizer { display: none; }
    .layout.chat-open :global(.sidebar) { display: none; }
    .layout:not(.chat-open) :global(.chat-view) { display: none; }
    /* Telegram-like push: the chat slides in, the list slides back. */
    .layout.chat-open :global(.chat-view) { animation: push-in .26s cubic-bezier(.2, .8, .2, 1); }
    .layout:not(.chat-open) :global(.sidebar) { animation: pop-back .22s cubic-bezier(.2, .8, .2, 1); }
  }
  @keyframes push-in { from { transform: translateX(100%); } }
  @keyframes pop-back { from { transform: translateX(-24%); opacity: .6; } }
</style>
