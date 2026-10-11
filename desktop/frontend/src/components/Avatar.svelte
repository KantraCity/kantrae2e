<script lang="ts">
  import { photos } from '../lib/photos.svelte'
  import { avatarColor, initials } from '../lib/util'
  // user + photo: the profile photo (avatarId) of that user, if any.
  let { name, id = name, size = 48, user, photo }: { name: string; id?: string; size?: number; user?: string; photo?: string } = $props()
  const src = $derived(photos.url(user, photo))
</script>

<div class="avatar" style="width:{size}px;height:{size}px;font-size:{Math.round(size * 0.38)}px;background:{avatarColor(id)}">
  {#if src}<img {src} alt="" />{:else}{initials(name)}{/if}
</div>

<style>
  .avatar {
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #fff;
    font-weight: 600;
    flex-shrink: 0;
    user-select: none;
    overflow: hidden;
    background-image: linear-gradient(rgba(255, 255, 255, 0.12), rgba(0, 0, 0, 0.05));
  }
  img { width: 100%; height: 100%; object-fit: cover; }
</style>
