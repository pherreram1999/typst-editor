<script>
  import { session, clearSession } from './session.svelte.js'

  let { path } = $props()

  const links = $derived([
    { href: '#/', label: 'Proyectos', active: path === '/' || path.startsWith('/p/') },
    { href: '#/github', label: 'GitHub', active: path === '/github' },
    ...(session.user?.role === 'admin'
      ? [{ href: '#/usuarios', label: 'Usuarios', active: path === '/usuarios' }]
      : []),
  ])

  const initials = (n) =>
    n.split(/\s+/).filter(Boolean).map((w) => w[0]).slice(0, 2).join('').toUpperCase()

  function logout() {
    clearSession()
    location.hash = '#/login'
  }
</script>

<nav class="bg-primary text-white">
  <div class="flex min-h-14 flex-wrap items-center justify-between gap-3 px-6">
    <div class="flex flex-wrap items-center gap-6">
      <span class="text-[17px] font-bold">Typst Editor</span>
      {#each links as l}
        <a
          href={l.href}
          class="border-b-2 py-3 text-sm no-underline {l.active
            ? 'border-white font-semibold text-white hover:text-white'
            : 'border-transparent font-medium text-blue-100 hover:text-white'}"
          aria-current={l.active ? 'page' : undefined}>{l.label}</a
        >
      {/each}
    </div>
    <div class="flex items-center gap-3">
      <span
        class="flex size-8 items-center justify-center rounded-full bg-primary-soft text-xs font-bold text-primary"
        aria-hidden="true">{initials(session.user?.name ?? '')}</span
      >
      <span class="hidden text-sm text-blue-100 sm:inline" title={session.user?.username}>{session.user?.name}</span>
      <button
        type="button"
        onclick={logout}
        class="h-9 cursor-pointer rounded-lg border border-blue-300 px-3.5 text-[13px] font-medium text-white hover:bg-primary-hover"
        >Cerrar sesión</button
      >
    </div>
  </div>
</nav>
