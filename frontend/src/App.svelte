<script>
  import { onMount } from 'svelte'
  import { session, clearSession } from './lib/session.svelte.js'
  import { api } from './lib/api.js'
  import Navbar from './lib/Navbar.svelte'
  import { pageIn, pageOut } from './lib/motion.js'
  import Login from './routes/Login.svelte'
  import Projects from './routes/Projects.svelte'
  import Users from './routes/Users.svelte'
  import GitHubAccounts from './routes/GitHubAccounts.svelte'
  // Vista pública de un enlace de revisión (sin sesión), también bajo demanda.
  const loadReview = () => import('./routes/Review.svelte')
  // El editor (CodeMirror) se carga bajo demanda al abrir un proyecto.
  const loadWorkspace = () => import('./routes/Workspace.svelte')

  let path = $state(location.hash.slice(1) || '/')
  // #/p/<id> abre el editor de un proyecto (vista a pantalla completa).
  const projectId = $derived(Number(path.match(/^\/p\/(\d+)$/)?.[1]) || null)
  // #/r/<token>: documento compartido para revisión (público).
  const reviewToken = $derived(path.match(/^\/r\/([A-Za-z0-9_-]+)$/)?.[1] ?? null)

  function onHash() {
    path = location.hash.slice(1) || '/'
  }

  // Validar el token guardado contra el backend.
  async function restoreSession() {
    if (session.token) {
      try {
        session.user = await api.me()
      } catch {
        clearSession()
      }
    }
    session.ready = true
  }

  onMount(() => {
    window.addEventListener('hashchange', onHash)
    restoreSession()
    return () => window.removeEventListener('hashchange', onHash)
  })

  // Guardas de ruta
  $effect(() => {
    if (!session.ready || reviewToken) return
    if (!session.user && path !== '/login') location.hash = '#/login'
    else if (session.user && path === '/login') location.hash = '#/'
    else if (path === '/usuarios' && session.user?.role !== 'admin') location.hash = '#/'
  })
</script>

<!--
  Las vistas que entran y salen se apilan en la misma celda de grid
  ([grid-area:1/1]) para que la animación no haga saltar el layout.
-->
<div class="grid min-h-screen">
  {#if reviewToken}
    {#await loadReview() then { default: Review }}
      <div class="min-w-0 [grid-area:1/1]"><Review token={reviewToken} /></div>
    {/await}
  {:else if !session.ready}
    <div class="flex items-center justify-center text-sm text-slate-500 [grid-area:1/1]" out:pageOut>Cargando…</div>
  {:else if !session.user}
    <div class="min-w-0 [grid-area:1/1]" in:pageIn={{ delay: 120 }} out:pageOut>
      <Login />
    </div>
  {:else}
    <div class="min-w-0 [grid-area:1/1]" in:pageIn={{ delay: 120 }} out:pageOut>
      <Navbar {path} />
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
