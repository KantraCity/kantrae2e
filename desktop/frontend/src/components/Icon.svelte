<script lang="ts">
  let { name, size = 24 }: { name: string; size?: number } = $props()

  const paths: Record<string, string> = {
    menu: 'M4 6h16M4 12h16M4 18h16',
    search: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14zM20 20l-4-4',
    pencil: 'M4 20h4L19 9l-4-4L4 16v4zM14 6l4 4',
    send: 'M4 12l16-8-6 16-2.5-6.5L4 12z',
    clip: 'M20 11l-8.5 8.5a5 5 0 0 1-7-7L13 4a3.5 3.5 0 0 1 5 5l-8.5 8.5a2 2 0 0 1-3-3L14 7',
    back: 'M15 5l-7 7 7 7',
    dots: 'M12 5v.01M12 12v.01M12 19v.01',
    file: 'M7 3h7l5 5v13H7zM14 3v5h5',
    down: 'M12 5v12m-5-5l5 5 5-5',
    check: 'M5 12l4 4L19 7',
    lock: 'M7 11V8a5 5 0 0 1 10 0v3M6 11h12v9H6z',
    shield: 'M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z',
    close: 'M6 6l12 12M18 6L6 18',
    device: 'M4 5h16v10H4zM2 19h20',
    plus: 'M12 5v14M5 12h14',
    user: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
    group: 'M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2 20a7 7 0 0 1 14 0M16 4a3.5 3.5 0 0 1 0 7M18 13.5a7 7 0 0 1 4 6.5',
    cloud: 'M7 18h10a4 4 0 0 0 .5-8A6 6 0 0 0 6 9a4.5 4.5 0 0 0 1 9z',
    warning: 'M12 4l9 16H3zM12 10v4M12 17v.01',
    history: 'M4 12a8 8 0 1 0 2.3-5.7M4 4v4h4M12 8v4l3 2',
    logout: 'M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10',
  }
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
  stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d={paths[name]} />
</svg>
