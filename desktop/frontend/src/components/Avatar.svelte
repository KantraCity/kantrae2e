<script lang="ts">
  import { avatarColor, initials } from '../lib/util'
  let { name, id = name, size = 48 }: { name: string; id?: string; size?: number } = $props()
</script>

<div class="avatar" style="width:{size}px;height:{size}px;font-size:{Math.round(size * 0.38)}px;background:{avatarColor(id)}">
  {initials(name)}
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
    background-image: linear-gradient(rgba(255, 255, 255, 0.12), rgba(0, 0, 0, 0.05));
  }
</style>
