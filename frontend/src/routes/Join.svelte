<script>
  // Invitación a un proyecto recibida por correo (#/unirse/<token>): crea la
  // cuenta con ese correo o, con sesión, acepta con la cuenta actual. De un
  // solo uso; al aceptar se abre el proyecto.
  import { session, clearSession } from '../lib/session.svelte.js'
  import { api, joinInfo, joinRegister, rememberReturn } from '../lib/api.js'
  import { pageIn } from '../lib/motion.js'

  let { token } = $props()

  let info = $state(null)
  let loadError = $state('')
  let error = $state('')
  let busy = $state(false)
  let name = $state('')
  let username = $state('')
  let password = $state('')
  let password2 = $state('')

  $effect(() => {
    const t = token
    info = null
    loadError = ''
    joinInfo(t).then(
      (r) => {
        info = r
        username = r.suggested_username
        document.title = `${r.project_name} · Invitación`
      },
      (e) => (loadError = e.message),
    )
  })

  const mismatch = $derived(password2 !== '' && password !== password2)

  async function accept() {
    busy = true
    error = ''
    try {
      const r = await api.acceptJoin(token)
      location.hash = `#/p/${r.project_id}`
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  async function register(e) {
    e.preventDefault()
    if (password !== password2) {
      error = 'Las contraseñas no coinciden.'
      return
    }
    busy = true
    error = ''
    try {
      const r = await joinRegister(token, { name: name.trim(), username: username.trim(), password })
      location.hash = `#/p/${r.project_id}`
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }

  function login() {
    rememberReturn() // volver aquí tras iniciar sesión
    location.hash = '#/login'
  }

  function switchAccount() {
    clearSession()
    login()
  }

  const fmtDate = (d) => new Date(d).toLocaleString('es-MX', { dateStyle: 'long', timeStyle: 'short' })
</script>

<main class="flex min-h-screen items-center justify-center bg-slate-100 px-4 py-10" in:pageIn>
  <div class="w-full max-w-md">
    <p class="mb-5 text-center text-[17px] font-bold text-slate-800">Typst Extend</p>
    <div class="card overflow-hidden !p-0">
      {#if loadError}
        <div class="flex flex-col gap-3 px-7 py-8 text-center">
          <h1 class="text-lg font-bold">Invitación no disponible</h1>
          <p class="text-sm text-slate-600">{loadError}</p>
          <a href="#/" class="btn-secondary no-underline">Ir al inicio</a>
        </div>
      {:else if !info}
        <p class="px-7 py-8 text-center text-sm text-slate-500">Cargando invitación…</p>
      {:else}
        <div class="border-b border-slate-200 bg-primary-tint px-7 py-5">
          <p class="text-xs font-semibold tracking-wide text-primary uppercase">Te invitaron a colaborar</p>
          <h1 class="mt-1 text-xl font-bold break-words">{info.project_name}</h1>
          <p class="mt-1 text-sm text-slate-600">
            {info.inviter_name || info.owner_name} te invitó con <strong>{info.email}</strong>.
          </p>
          <p class="mt-2 text-xs text-slate-500">La invitación vence el {fmtDate(info.expires_at)}.</p>
        </div>
        <div class="flex flex-col gap-4 px-7 py-6">
          {#if error}<div class="alert-error">{error}</div>{/if}
          {#if session.user}
            <p class="text-sm text-slate-600">Has iniciado sesión como <strong>{session.user.name}</strong> ({session.user.username}).</p>
            <button type="button" class="btn-primary w-full" onclick={accept} disabled={busy}>{busy ? 'Aceptando…' : 'Aceptar y abrir el proyecto'}</button>
            <button type="button" class="btn-secondary w-full" onclick={switchAccount}>Usar otra cuenta</button>
          {:else if info.has_account}
            <p class="text-sm text-slate-600">Ya existe una cuenta con <strong>{info.email}</strong>. Inicia sesión para aceptar la invitación.</p>
            <button type="button" class="btn-primary w-full" onclick={login}>Iniciar sesión</button>
          {:else}
            <p class="text-sm text-slate-600">Crea tu cuenta para entrar al proyecto. Podrás iniciar sesión con tu usuario o con {info.email}.</p>
            <form class="flex flex-col gap-4" onsubmit={register}>
              <div class="flex flex-col gap-1.5">
                <label class="label" for="jn-name">Tu nombre</label>
                <input id="jn-name" class="input" required maxlength="128" autocomplete="name" bind:value={name} />
              </div>
              <div class="flex flex-col gap-1.5">
                <label class="label" for="jn-user">Usuario</label>
                <input id="jn-user" class="input" required pattern="[a-zA-Z0-9._\-]{'{3,64}'}" autocomplete="username" autocapitalize="none" spellcheck="false" bind:value={username} />
              </div>
              <div class="flex flex-col gap-1.5">
                <label class="label" for="jn-pass">Contraseña</label>
                <input id="jn-pass" class="input" type="password" minlength="8" maxlength="72" autocomplete="new-password" required bind:value={password} />
                <span class="text-xs text-slate-500">Mínimo 8 caracteres.</span>
              </div>
              <div class="flex flex-col gap-1.5">
                <label class="label" for="jn-pass2">Repite la contraseña</label>
                <input id="jn-pass2" class="input {mismatch ? 'border-red-700' : ''}" type="password" autocomplete="new-password" required aria-invalid={mismatch} bind:value={password2} />
                {#if mismatch}<span class="text-xs text-red-700">Las contraseñas no coinciden.</span>{/if}
              </div>
              <button class="btn-primary w-full" disabled={busy || mismatch}>{busy ? 'Creando la cuenta…' : 'Crear cuenta y abrir el proyecto'}</button>
            </form>
            <p class="text-center text-xs text-slate-500">¿Ya tienes una cuenta con otro correo? <button type="button" class="cursor-pointer text-primary underline" onclick={login}>Inicia sesión</button></p>
          {/if}
        </div>
      {/if}
    </div>
  </div>
</main>
