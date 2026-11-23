<script>
  // Enlaces de cuenta recibidos por correo (#/cuenta/<token>): definir la
  // contraseña de una cuenta nueva (alta por el administrador), elegir una
  // nueva («olvidé mi contraseña») o confirmar un correo. De un solo uso.
  import { session } from '../lib/session.svelte.js'
  import { accountInfo, accountPassword, accountEmail } from '../lib/api.js'
  import { pageIn } from '../lib/motion.js'

  let { token } = $props()

  let info = $state(null)
  let error = $state('')
  let loadError = $state('')
  let busy = $state(false)
  let done = $state('') // mensaje final (correo confirmado)
  let name = $state('')
  let password = $state('')
  let password2 = $state('')
  let show = $state(false)

  $effect(() => {
    const t = token
    info = null
    loadError = ''
    accountInfo(t).then(
      (r) => {
        info = r
        name = r.name
        document.title = r.purpose === 'email' ? 'Confirmar correo · Typst Extend' : 'Contraseña · Typst Extend'
      },
      (e) => (loadError = e.message),
    )
  })

  const mismatch = $derived(password2 !== '' && password !== password2)

  async function submitPassword(e) {
    e.preventDefault()
    if (password !== password2) {
      error = 'Las contraseñas no coinciden.'
      return
    }
    busy = true
    error = ''
    try {
      const body = { password }
      if (info.purpose === 'setup') body.name = name
      await accountPassword(token, body)
      location.hash = '#/'
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }

  async function confirmEmail() {
    busy = true
    error = ''
    try {
      const u = await accountEmail(token)
      if (session.user?.id === u.id) session.user = u
      done = `Listo: ${u.email} es ahora el correo de tu cuenta.`
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }

  const titles = { setup: 'Activa tu cuenta', reset: 'Elige una contraseña nueva', email: 'Confirma tu correo' }
</script>

<main class="flex min-h-screen items-center justify-center bg-slate-100 px-4 py-10" in:pageIn>
  <div class="w-full max-w-md">
    <p class="mb-5 text-center text-[17px] font-bold text-slate-800">Typst Extend</p>
    <div class="card flex flex-col gap-4 !p-7">
      {#if loadError}
        <h1 class="text-xl font-bold">Enlace no válido</h1>
        <p class="text-sm text-slate-600">{loadError}. Puedes pedir uno nuevo desde «¿Olvidaste tu contraseña?» en la pantalla de inicio de sesión.</p>
        <a href="#/login" class="btn-primary w-full no-underline">Ir a iniciar sesión</a>
      {:else if !info}
        <p class="text-center text-sm text-slate-500">Comprobando el enlace…</p>
      {:else if info.purpose === 'email'}
        <h1 class="text-xl font-bold">{titles.email}</h1>
        {#if done}
          <div class="alert-info">{done}</div>
          <a href="#/" class="btn-primary w-full no-underline">Continuar</a>
        {:else}
          <p class="text-sm text-slate-600">
            Vas a usar <strong>{info.email}</strong> en la cuenta <strong>{info.username}</strong>: podrás iniciar sesión con él,
            recuperar tu contraseña y recibir avisos de los proyectos que te compartan.
          </p>
          {#if error}<div class="alert-error">{error}</div>{/if}
          <button type="button" class="btn-primary w-full" onclick={confirmEmail} disabled={busy}>{busy ? 'Confirmando…' : 'Confirmar mi correo'}</button>
        {/if}
      {:else}
        <div>
          <h1 class="text-xl font-bold">{titles[info.purpose]}</h1>
          <p class="mt-1 text-sm text-slate-600">
            {#if info.purpose === 'setup'}
              Define tu contraseña para empezar. Tu usuario es <strong>{info.username}</strong>{#if info.email}; también puedes entrar con <strong>{info.email}</strong>{/if}.
            {:else}
              Cuenta <strong>{info.username}</strong>{#if info.email} ({info.email}){/if}.
            {/if}
          </p>
        </div>
        <form class="flex flex-col gap-4" onsubmit={submitPassword}>
          {#if error}<div class="alert-error">{error}</div>{/if}
          {#if info.purpose === 'setup'}
            <div class="flex flex-col gap-1.5">
              <label class="label" for="ac-name">Tu nombre</label>
              <input id="ac-name" class="input" required maxlength="128" autocomplete="name" bind:value={name} />
            </div>
          {/if}
          <!-- Para que el gestor de contraseñas asocie la nueva a la cuenta. -->
          <input type="text" class="hidden" autocomplete="username" value={info.username} readonly tabindex="-1" aria-hidden="true" />
          <div class="flex flex-col gap-1.5">
            <label class="label" for="ac-pass">Contraseña nueva</label>
            <div class="relative">
              <input id="ac-pass" class="input pr-20" type={show ? 'text' : 'password'} autocomplete="new-password" minlength="8" maxlength="72" required bind:value={password} />
              <button type="button" class="absolute inset-y-0 right-0 cursor-pointer px-3 text-xs font-medium text-slate-500 hover:text-slate-900" onclick={() => (show = !show)}>{show ? 'Ocultar' : 'Mostrar'}</button>
            </div>
            <span class="text-xs text-slate-500">Mínimo 8 caracteres.</span>
          </div>
          <div class="flex flex-col gap-1.5">
            <label class="label" for="ac-pass2">Repite la contraseña</label>
            <input id="ac-pass2" class="input {mismatch ? 'border-red-700' : ''}" type={show ? 'text' : 'password'} autocomplete="new-password" required aria-invalid={mismatch} bind:value={password2} />
            {#if mismatch}<span class="text-xs text-red-700">Las contraseñas no coinciden.</span>{/if}
          </div>
          <button class="btn-primary w-full" disabled={busy || mismatch}>{busy ? 'Guardando…' : info.purpose === 'setup' ? 'Activar mi cuenta' : 'Guardar y entrar'}</button>
        </form>
      {/if}
    </div>
  </div>
</main>
