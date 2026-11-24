<script>
  import Logo from './Logo.svelte'
  import { session, clearSession } from './session.svelte.js'
  import { theme, setTheme, THEMES } from './theme.svelte.js'
  import Modal from './Modal.svelte'
  import ChangePassword from './ChangePassword.svelte'

  let passwordOpen = $state(false)
  let passwordDone = $state(false)

  let { path } = $props()

  const links = $derived([
    { href: '#/', label: 'Proyectos', active: path === '/' || path.startsWith('/p/') },
    { href: '#/github', label: 'GitHub', active: path === '/github' },
    { href: '#/mcp', label: 'Agentes', active: path === '/mcp' },
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

<nav class="bg-nav text-white">
  <div class="flex min-h-14 flex-wrap items-center justify-between gap-3 px-6">
    <div class="flex flex-wrap items-center gap-6">
      <Logo on="dark" />
      {#each links as l}
        <a
          href={l.href}
          class="border-b-2 py-3 text-sm no-underline {l.active
            ? 'border-white font-semibold text-white hover:text-white'
            : 'border-transparent font-medium text-white/75 hover:text-white'}"
          aria-current={l.active ? 'page' : undefined}>{l.label}</a
        >
      {/each}
    </div>
    <div class="flex items-center gap-3">
      <div class="flex items-center rounded-lg bg-white/10 p-0.5" role="radiogroup" aria-label="Tema">
        {#each THEMES as t (t.key)}
          <button
            type="button"
            role="radio"
            aria-checked={theme.current === t.key}
            title="Tema {t.label.toLowerCase()}"
            aria-label="Tema {t.label.toLowerCase()}"
            onclick={() => setTheme(t.key)}
            class="flex size-8 cursor-pointer items-center justify-center rounded-md transition-colors {theme.current === t.key
              ? 'bg-white/25 text-white'
              : 'text-white/70 hover:bg-white/10 hover:text-white'}"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              {#if t.key === 'light'}
                <circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
              {:else if t.key === 'dark'}
                <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />
              {:else}
                <circle cx="12" cy="12" r="9" /><path d="M12 3a9 9 0 0 1 0 18z" fill="currentColor" />
              {/if}
            </svg>
          </button>
        {/each}
      </div>
      <span
        class="flex size-8 items-center justify-center rounded-full bg-primary-soft text-xs font-bold text-primary"
        aria-hidden="true">{initials(session.user?.name ?? '')}</span
      >
      <button
        type="button"
        class="hidden cursor-pointer text-sm text-white/80 hover:text-white hover:underline sm:inline"
        title="{session.user?.username}{session.user?.email ? ` · ${session.user.email}` : ''} — cambiar contraseña"
        onclick={() => ((passwordDone = false), (passwordOpen = true))}>{session.user?.name}</button
      >
      <button
        type="button"
        onclick={logout}
        class="h-9 cursor-pointer rounded-lg border border-white/40 px-3.5 text-[13px] font-medium text-white hover:bg-white/10"
        >Cerrar sesión</button
      >
    </div>
  </div>
</nav>

<Modal open={passwordOpen} title="Cambiar contraseña" onclose={() => (passwordOpen = false)}>
  {#if passwordDone}
    <div class="flex flex-col gap-4">
      <div class="alert-info">Tu contraseña se cambió.</div>
      <div class="flex justify-end"><button type="button" class="btn-secondary" onclick={() => (passwordOpen = false)}>Cerrar</button></div>
    </div>
  {:else}
    <ChangePassword ondone={() => (passwordDone = true)} />
  {/if}
</Modal>
