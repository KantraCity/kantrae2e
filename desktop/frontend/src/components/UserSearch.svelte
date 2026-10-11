<script lang="ts">
  // Search people by username (global directory), debounced.
  import { Messenger, type UserHit } from '../lib/api'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  let { onpick, selected = [], placeholder = 'Поиск по имени пользователя' }: {
    onpick: (u: UserHit) => void
    selected?: string[]
    placeholder?: string
  } = $props()

  let query = $state('')
  let hits = $state<UserHit[]>([])
  let busy = $state(false)
  let timer: ReturnType<typeof setTimeout> | undefined
  let seq = 0

  function input() {
    clearTimeout(timer)
    const q = query.trim().replace(/^@/, '')
    if (q.length < 2) {
      hits = []
      busy = false
      return
    }
    busy = true
    timer = setTimeout(async () => {
      const my = ++seq
      const r = await Messenger.SearchUsers(q).catch(() => [] as UserHit[])
      if (my === seq) {
        hits = r ?? []
        busy = false
      }
    }, 250)
  }
</script>

<div class="search">
  <Icon name="search" size={18} />
  <!-- svelte-ignore a11y_autofocus -->
  <input {placeholder} bind:value={query} oninput={input} autofocus autocomplete="off" autocapitalize="off" spellcheck="false" />
</div>
<div class="hits">
  {#each hits as u (u.userId)}
    <button class="hit" onclick={() => onpick(u)}>
      <Avatar name={u.username} id={u.userId} size={42} />
      <div class="who">
        <div class="name">{u.username}</div>
        <div class="muted small">@{u.username}</div>
      </div>
      {#if selected.includes(u.username)}<span class="tick"><Icon name="check" size={18} /></span>{/if}
    </button>
  {:else}
    <div class="muted empty">
      {#if busy}Поиск…{:else if query.trim().replace(/^@/, '').length >= 2}Никого не найдено{:else}Введите хотя бы 2 символа ника{/if}
    </div>
  {/each}
</div>

<style>
  .search { display: flex; align-items: center; gap: 8px; background: var(--bg-input); border-radius: 20px; padding: 0 14px; height: 40px; color: var(--text-muted); }
  .search input { flex: 1; background: transparent; border: 0; outline: 0; min-width: 0; color: var(--text); }
  .hits { display: flex; flex-direction: column; margin-top: 8px; min-height: 120px; max-height: 50vh; overflow-y: auto; }
  .hit { display: flex; align-items: center; gap: 12px; padding: 7px 8px; border-radius: 10px; text-align: left; }
  .hit:hover { background: var(--bg-hover); }
  .who { flex: 1; min-width: 0; }
  .name { font-weight: 500; }
  .small { font-size: 13px; }
  .tick { color: #fff; background: var(--accent); border-radius: 50%; width: 24px; height: 24px; display: flex; align-items: center; justify-content: center; }
  .empty { text-align: center; padding: 24px 0; font-size: 14px; }
</style>
