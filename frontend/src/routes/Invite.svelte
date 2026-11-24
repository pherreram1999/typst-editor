<script>
  import Logo from '../lib/Logo.svelte'
  // Enlace de invitado (#/i/<token>): una URL firmada que caduca y da acceso
  // al proyecto sin cuenta. Primero se muestra una vista previa (proyecto,
  // permiso, caducidad) donde el invitado elige su nombre —uno aleatorio o el
  // suyo, guardado en localStorage— y después el editor completo con una
  // sesión de invitado que vive solo en esta pestaña (sessionStorage).
  import { onMount } from 'svelte'
  import { session, enterGuest, storedToken } from '../lib/session.svelte.js'
  import { inviteInfo, joinInvite, accountRequest, rememberReturn } from '../lib/api.js'
  import { randomGuestName, savedGuestName, saveGuestName } from '../lib/guestName.js'
  import { pageIn } from '../lib/motion.js'

  let { token } = $props()

  const loadWorkspace = () => import('./Workspace.svelte')
  const SESSION_KEY = 'typsteditor.guestSession'

  let info = $state(null)
  let error = $state('')
  let ended = $state('') // aviso al volver aquí porque el acceso terminó
  let name = $state('')
  let generated = $state('') // último nombre aleatorio sorteado
  let busy = $state(false)
  let joined = $state(null) // { project_id, permission, expires_at }
  let input = $state()
  // Con la sesión iniciada: si la cuenta ya tiene acceso al proyecto se abre
  // directamente (#/p/<id>); si no, se entra como invitado con su nombre.
  let checkedFor = null // id de la cuenta ya comprobada
  let noAccess = $state(false)

  const random = $derived(!!generated && name.trim() === generated)
  const colors = ['#1E40AF', '#B91C1C', '#047857', '#7C3AED', '#C2410C', '#0E7490', '#BE185D', '#4D7C0F']
  const initials = (n) => n.split(/\s+/).filter(Boolean).map((w) => w[0]).slice(0, 2).join('').toUpperCase()
  const avatarColor = $derived(colors[[...name.trim()].reduce((h, c) => (h * 31 + c.codePointAt(0)) >>> 0, 7) % colors.length])

  function readStored() {
    try {
      const s = JSON.parse(sessionStorage.getItem(SESSION_KEY) ?? 'null')
      if (s?.invite === token && s.token && new Date(s.expires_at) > new Date()) return s
    } catch {
      /* sin almacenamiento */
    }
    return null
  }
  function forget() {
    try {
      sessionStorage.removeItem(SESSION_KEY)
    } catch {
      /* sin almacenamiento */
    }
  }

  async function loadInfo() {
    error = ''
    try {
      info = await inviteInfo(token)
      document.title = `${info.project_name} · Invitación`
    } catch (e) {
      info = null
      error = e.status === 404 ? 'Este enlace de invitación no es válido, se revocó o ya caducó. Pide uno nuevo a quien te lo compartió.' : e.message
    }
  }

  onMount(() => {
    const pref = savedGuestName()
    if (pref) {
      name = pref.name
      if (pref.random) generated = pref.name
    } else {
      name = generated = randomGuestName()
    }
    // Recargar la pestaña no obliga a volver a entrar.
    const stored = readStored()
    if (stored) {
      joined = stored
      enterGuest(stored.token, stored.user)
    }
    loadInfo()
    // «Salir · cambiar mi nombre» del menú del celular (Workspace).
    window.addEventListener('typsteditor:guest-exit', changeName)
    return () => window.removeEventListener('typsteditor:guest-exit', changeName)
  })

  $effect(() => {
    const acc = session.account
    const tok = storedToken()
    if (!acc || !tok || !info || checkedFor === acc.id) return
    checkedFor = acc.id
    const pid = info.project_id
    accountRequest(tok, `/projects/${pid}`).then(
      () => {
        forget()
        location.replace(`#/p/${pid}`)
      },
      () => {
        noAccess = true
        if (!joined && (random || !name.trim())) name = acc.name
      },
    )
  })

  // El enlace caducó o se revocó mientras se editaba (api.js deja user en null).
  $effect(() => {
    if (joined && !session.user) {
      joined = null
      forget()
      ended = 'Tu acceso como invitado terminó.'
      loadInfo()
    }
  })

  function reroll() {
    name = generated = randomGuestName()
    input?.focus()
  }

  async function submit(e) {
    e.preventDefault()
    const n = name.trim().replace(/\s+/g, ' ')
    if (!n) return
    busy = true
    error = ''
    try {
      const r = await joinInvite(token, n)
      saveGuestName(n, random)
      const stored = { invite: token, token: r.token, user: r.user, project_id: r.project_id, permission: r.permission, expires_at: r.expires_at }
      try {
        sessionStorage.setItem(SESSION_KEY, JSON.stringify(stored))
      } catch {
        /* sin almacenamiento: habrá que volver a entrar al recargar */
      }
      ended = ''
      joined = stored
      enterGuest(r.token, r.user)
    } catch (err) {
      error = err.status === 404 ? 'Este enlace de invitación ya no es válido.' : err.message
      if (err.status === 404) info = null
    } finally {
      busy = false
    }
  }

  // Cambiar de nombre: se sale de la sesión y se vuelve a la vista previa.
  function changeName() {
    joined = null
    forget()
    session.user = null
    session.token = null
    loadInfo()
  }

  const fmtDate = (d) => new Date(d).toLocaleString('es-MX', { dateStyle: 'long', timeStyle: 'short' })
  function remaining(d) {
    const ms = new Date(d) - Date.now()
    const h = ms / 3600000
    if (h < 1) return 'menos de una hora'
    if (h < 48) return `${Math.round(h)} ${Math.round(h) === 1 ? 'hora' : 'horas'}`
    const days = Math.round(h / 24)
    if (days < 60) return `${days} días`
    return `${Math.round(days / 30)} meses`
  }
</script>

{#if joined && session.user}
  <div class="min-w-0">
    <!-- Barra del invitado (en el celular el proyecto ocupa toda la pantalla). -->
    <nav class="bg-nav text-white max-lg:hidden">
      <div class="flex h-14 items-center justify-between gap-3 px-6">
        <div class="flex min-w-0 items-center gap-3">
          <Logo on="dark" />
          <span class="rounded-md bg-white/15 px-2 py-0.5 text-xs font-semibold">Invitado</span>
          {#if joined.permission === 'view'}
            <span class="rounded-md bg-amber-400/90 px-2 py-0.5 text-xs font-semibold text-amber-950">Solo lectura</span>
          {/if}
          <span class="hidden truncate text-xs text-white/70 md:inline" title={fmtDate(joined.expires_at)}>Acceso hasta el {fmtDate(joined.expires_at)}</span>
        </div>
        <div class="flex shrink-0 items-center gap-3">
          <span class="flex size-8 items-center justify-center rounded-full bg-primary-soft text-xs font-bold text-primary" aria-hidden="true">{initials(session.user.name)}</span>
          <span class="text-sm text-white/85">{session.user.name}</span>
          <button
            type="button"
            onclick={changeName}
            class="h-9 cursor-pointer rounded-lg border border-white/40 px-3.5 text-[13px] font-medium text-white hover:bg-white/10">Cambiar nombre</button
          >
        </div>
      </div>
    </nav>
    {#await loadWorkspace() then { default: Workspace }}
      <Workspace id={joined.project_id} />
    {/await}
  </div>
{:else}
  <main class="flex min-h-screen items-center justify-center bg-slate-100 px-4 py-10" in:pageIn>
    <div class="w-full max-w-md">
      <p class="mb-5 flex justify-center"><Logo /></p>
      <div class="card overflow-hidden !p-0">
        {#if info}
          <div class="border-b border-slate-200 bg-primary-tint px-6 py-5">
            <p class="text-xs font-semibold tracking-wide text-primary uppercase">Te invitaron a colaborar</p>
            <h1 class="mt-1 text-xl font-bold break-words text-slate-900">{info.project_name}</h1>
            <p class="mt-1 text-sm text-slate-600">Proyecto de <strong class="font-semibold">{info.owner_name}</strong></p>
            {#if info.label}<p class="mt-2 text-sm text-slate-600 italic">«{info.label}»</p>{/if}
            <div class="mt-3 flex flex-wrap gap-2 text-xs">
              {#if info.permission === 'edit'}
                <span class="badge bg-emerald-100 text-emerald-800">Puedes editar</span>
              {:else}
                <span class="badge bg-amber-100 text-amber-800">Solo lectura</span>
              {/if}
              <span class="badge bg-surface text-slate-600" title={fmtDate(info.expires_at)}>Caduca en {remaining(info.expires_at)}</span>
            </div>
            {#if info.editors?.length}
              <p class="mt-3 text-xs text-slate-600">
                Ahora mismo: {info.editors.slice(0, 3).join(', ')}{info.editors.length > 3 ? ` y ${info.editors.length - 3} más` : ''}
              </p>
            {/if}
          </div>

          <form class="flex flex-col gap-4 px-6 py-5" onsubmit={submit}>
            {#if ended}<div class="alert-info">{ended} Puedes volver a entrar mientras el enlace siga vigente.</div>{/if}
            {#if error}<div class="alert-error">{error}</div>{/if}
            <div>
              <label class="label" for="guest-name">¿Cómo quieres que te vean los demás?</label>
              <div class="flex items-center gap-2">
                <span
                  class="flex size-10 shrink-0 items-center justify-center rounded-full text-sm font-semibold text-white"
                  style="background-color: {avatarColor}"
                  aria-hidden="true">{initials(name) || '?'}</span
                >
                <input
                  id="guest-name"
                  class="input"
                  bind:this={input}
                  bind:value={name}
                  maxlength="40"
                  autocomplete="nickname"
                  required
                />
                <button
                  type="button"
                  class="btn-secondary shrink-0 !px-3"
                  onclick={reroll}
                  title="Otro nombre aleatorio"
                  aria-label="Otro nombre aleatorio"
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="3" /><circle cx="8" cy="8" r="1.2" fill="currentColor" /><circle cx="16" cy="8" r="1.2" fill="currentColor" /><circle cx="12" cy="12" r="1.2" fill="currentColor" /><circle cx="8" cy="16" r="1.2" fill="currentColor" /><circle cx="16" cy="16" r="1.2" fill="currentColor" /></svg>
                </button>
              </div>
              <p class="mt-1.5 text-xs text-slate-500">
                {random ? 'Nombre aleatorio: sortea otro con el dado o escribe el tuyo.' : 'Tu nombre.'} Se recordará en este navegador.
              </p>
            </div>
            <button class="btn-primary w-full" disabled={busy || !name.trim()}>{busy ? 'Entrando…' : 'Entrar al proyecto'}</button>
            {#if session.account}
              {#if noAccess}
                <p class="text-center text-xs text-slate-500">
                  Tienes la sesión iniciada como <strong class="font-semibold">{session.account.name}</strong>, pero tu cuenta no es miembro de este
                  proyecto: entrarás como invitado. <a href="#/">Ir a mis proyectos</a>
                </p>
              {/if}
            {:else}
              <p class="text-center text-xs text-slate-500">
                Entras sin cuenta. ¿Tienes una? <a href="#/login" onclick={() => rememberReturn(`#/i/${token}`)}>Inicia sesión</a>
              </p>
            {/if}
          </form>
        {:else if error}
          <div class="px-6 py-8 text-center">
            <svg class="mx-auto mb-3 text-slate-400" width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7" /><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7" /><path d="M3 3l18 18" /></svg>
            {#if ended}<p class="mb-2 text-sm font-semibold text-slate-800">{ended}</p>{/if}
            <p class="text-sm text-slate-600">{error}</p>
          </div>
        {:else}
          <p class="px-6 py-8 text-center text-sm text-slate-500">Cargando invitación…</p>
        {/if}
      </div>
    </div>
  </main>
{/if}
