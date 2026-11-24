<script>
  import Logo from './lib/Logo.svelte'
  import { onMount } from 'svelte'
  import { session, clearSession, leaveGuest, storedToken, forgetAccount } from './lib/session.svelte.js'
  import { accountRequest, rememberReturn, takeReturn } from './lib/api.js'
  import Navbar from './lib/Navbar.svelte'
  import EmailPrompt from './lib/EmailPrompt.svelte'
  import ChangePassword from './lib/ChangePassword.svelte'
  import { pageIn, pageOut } from './lib/motion.js'
  import Login from './routes/Login.svelte'
  import Projects from './routes/Projects.svelte'
  import Users from './routes/Users.svelte'
  import GitHubAccounts from './routes/GitHubAccounts.svelte'
  import McpAccess from './routes/McpAccess.svelte'
  import OAuthConsent from './routes/OAuthConsent.svelte'
  // Vista pública de un enlace de revisión (sin sesión), también bajo demanda.
  const loadReview = () => import('./routes/Review.svelte')
  // Enlace de invitado (URL firmada): vista previa y editor sin cuenta.
  const loadInvite = () => import('./routes/Invite.svelte')
  // Enlaces recibidos por correo: contraseña/confirmar correo e invitación a un proyecto.
  const loadAccount = () => import('./routes/Account.svelte')
  const loadJoin = () => import('./routes/Join.svelte')
  // El editor (CodeMirror) se carga bajo demanda al abrir un proyecto.
  const loadWorkspace = () => import('./routes/Workspace.svelte')

  let path = $state(location.hash.slice(1) || '/')
  // #/p/<id> abre el editor de un proyecto (vista a pantalla completa).
  const projectId = $derived(Number(path.match(/^\/p\/(\d+)$/)?.[1]) || null)
  // #/r/<token>: documento compartido para revisión (público).
  const reviewToken = $derived(path.match(/^\/r\/([A-Za-z0-9_-]+)$/)?.[1] ?? null)
  // #/i/<token>: entrar como invitado con un enlace firmado.
  const inviteToken = $derived(path.match(/^\/i\/([A-Za-z0-9_.-]+)$/)?.[1] ?? null)
  // #/cuenta/<token>: definir o restablecer la contraseña, confirmar un correo.
  const accountToken = $derived(path.match(/^\/cuenta\/([A-Za-z0-9_.-]+)$/)?.[1] ?? null)
  // #/unirse/<token>: invitación a un proyecto enviada por correo.
  const joinToken = $derived(path.match(/^\/unirse\/([A-Za-z0-9_.-]+)$/)?.[1] ?? null)

  function onHash() {
    path = location.hash.slice(1) || '/'
  }

  // Validar el token guardado contra el backend. Con un enlace de invitado
  // abierto también se valida (Invite.svelte usa session.account para llevar
  // al proyecto a quien ya tiene acceso), pero sin pisar la sesión de invitado.
  async function restoreSession() {
    const tok = storedToken()
    if (tok) {
      try {
        const me = await accountRequest(tok, '/auth/me')
        if (storedToken() === tok) {
          session.account = me
          if (!session.guest) session.user = me
        }
      } catch {
        if (storedToken() === tok) forgetAccount()
      }
    }
    session.ready = true
  }

  onMount(() => {
    window.addEventListener('hashchange', onHash)
    restoreSession()
    return () => window.removeEventListener('hashchange', onHash)
  })

  // Al salir de un enlace de invitado vuelve la sesión propia (si la hay).
  $effect(() => {
    if (inviteToken || !session.guest) return
    leaveGuest()
    session.ready = false
    restoreSession()
  })

  // Guardas de ruta
  $effect(() => {
    if (!session.ready || reviewToken || inviteToken || accountToken || joinToken || session.guest) return
    if (!session.user && path !== '/login') {
      rememberReturn()
      location.hash = '#/login'
    } else if (session.user && path === '/login') location.hash = takeReturn() ?? '#/'
    else if (path === '/usuarios' && session.user?.role !== 'admin') location.hash = '#/'
  })
</script>

<!--
  Las vistas que entran y salen se apilan en la misma celda de grid
  ([grid-area:1/1]) para que la animación no haga saltar el layout.
-->
<div class="grid min-h-screen">
  {#if inviteToken}
    {#await loadInvite() then { default: Invite }}
      {#key inviteToken}
        <div class="min-w-0 [grid-area:1/1]"><Invite token={inviteToken} /></div>
      {/key}
    {/await}
  {:else if accountToken}
    {#await loadAccount() then { default: Account }}
      {#key accountToken}
        <div class="min-w-0 [grid-area:1/1]"><Account token={accountToken} /></div>
      {/key}
    {/await}
  {:else if joinToken && session.ready}
    {#await loadJoin() then { default: Join }}
      {#key joinToken}
        <div class="min-w-0 [grid-area:1/1]"><Join token={joinToken} /></div>
      {/key}
    {/await}
  {:else if reviewToken}
    {#await loadReview() then { default: Review }}
      <div class="min-w-0 [grid-area:1/1]"><Review token={reviewToken} /></div>
    {/await}
  {:else if !session.ready}
    <div class="flex items-center justify-center text-sm text-slate-500 [grid-area:1/1]" out:pageOut>Cargando…</div>
  {:else if !session.user}
    <div class="min-w-0 [grid-area:1/1]" in:pageIn={{ delay: 120 }} out:pageOut>
      <Login />
    </div>
  {:else if session.user.must_change_password}
    <!-- Contraseña temporal (alta o restablecimiento sin correo): cambio obligatorio. -->
    <main class="flex min-h-screen items-center justify-center bg-slate-100 px-4 py-10 [grid-area:1/1]" in:pageIn>
      <div class="w-full max-w-md">
        <p class="mb-5 flex justify-center"><Logo /></p>
        <div class="card flex flex-col gap-4 !p-7">
          <div>
            <h1 class="text-xl font-bold">Cambia tu contraseña</h1>
            <p class="mt-1 text-sm text-slate-600">
              Hola, {session.user.name}. Entraste con una contraseña temporal; elige una propia para continuar.
            </p>
          </div>
          <ChangePassword forced />
          <button type="button" class="cursor-pointer text-center text-sm text-slate-500 hover:text-slate-900" onclick={() => (clearSession(), (location.hash = '#/login'))}>Cerrar sesión</button>
        </div>
      </div>
    </main>
  {:else}
    <div class="min-w-0 [grid-area:1/1]" in:pageIn={{ delay: 120 }} out:pageOut>
      <!-- En el celular el proyecto ocupa toda la pantalla (como Google Docs). -->
      <div class={projectId ? 'max-lg:hidden' : ''}><Navbar {path} /></div>
      {#if !projectId}<EmailPrompt />{/if}
      {#if projectId}
        {#key projectId}
          {#await loadWorkspace() then { default: Workspace }}
            <Workspace id={projectId} />
          {/await}
        {/key}
      {:else if path === '/' || path === ''}
        <!-- Proyectos ocupa todo el ancho (barra lateral + cuadrícula). -->
        <div in:pageIn={{ delay: 120 }}>
          <Projects />
        </div>
      {:else}
        <main class="mx-auto grid max-w-6xl px-6 py-8">
          {#key path}
            <div class="min-w-0 [grid-area:1/1]" in:pageIn={{ delay: 120 }} out:pageOut>
              {#if path === '/usuarios' && session.user.role === 'admin'}
                <Users />
              {:else if path === '/github'}
                <GitHubAccounts />
              {:else if path === '/mcp'}
                <McpAccess />
              {:else if path.startsWith('/oauth?')}
                <OAuthConsent query={path.slice('/oauth?'.length)} />
              {:else}
                <Projects />
              {/if}
            </div>
          {/key}
        </main>
      {/if}
    </div>
  {/if}
</div>
