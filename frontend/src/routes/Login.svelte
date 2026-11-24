<script>
  import Logo from '../lib/Logo.svelte'
  import { onMount } from 'svelte'
  import { api, authConfig, forgotPassword } from '../lib/api.js'

  let username = $state('')
  let password = $state('')
  let showPassword = $state(false)
  let error = $state('')
  let loading = $state(false)
  // «¿Olvidaste tu contraseña?»: solo si el servidor puede enviar correos.
  let mailEnabled = $state(false)
  let forgot = $state(false)
  let forgotSent = $state('')

  onMount(() => {
    authConfig().then((c) => (mailEnabled = c.mail_enabled), () => {})
  })

  async function sendForgot(e) {
    e.preventDefault()
    error = ''
    loading = true
    try {
      await forgotPassword(username.trim())
      forgotSent = 'Si existe una cuenta con ese usuario o correo, te enviamos un enlace para elegir tu contraseña. Revisa tu bandeja de entrada (y el correo no deseado).'
    } catch (err) {
      error = err.message
    } finally {
      loading = false
    }
  }

  function toggleForgot() {
    forgot = !forgot
    forgotSent = ''
    error = ''
  }

  async function submit(e) {
    e.preventDefault()
    error = ''
    loading = true
    try {
      // La guarda de rutas de App.svelte lleva a #/ o a donde se estaba (p. ej.
      // la autorización de una aplicación).
      await api.login(username, password)
    } catch (err) {
      error = err.message
    } finally {
      loading = false
    }
  }
</script>

<!--
  Celular: franja de color compacta arriba y el formulario como una hoja que
  sube sobre ella, visible sin desplazarse y con controles táctiles grandes.
  Desde md: dos columnas (panel de bienvenida | tarjeta).
-->
<div class="flex min-h-dvh flex-col bg-primary md:flex-row md:bg-slate-50">
  <aside
    class="flex shrink-0 flex-col gap-7 px-6 pt-[calc(env(safe-area-inset-top)+1.75rem)] pb-12 text-white
      md:flex-[1_1_420px] md:justify-between md:gap-8 md:bg-primary md:p-12"
  >
    <div class="flex items-center gap-3">
      <Logo on="dark" size="lg" />
    </div>
    <div class="flex max-w-md flex-col gap-2 md:gap-3">
      <h1 class="text-[28px] leading-tight font-bold tracking-tight md:text-4xl">Bienvenido de nuevo</h1>
      <p class="text-[15px] leading-relaxed text-white/80 md:text-base">
        Inicia sesión con tu usuario o tu correo.
      </p>
    </div>
    <span class="hidden text-[13px] text-white/60 md:block">© {new Date().getFullYear()} nullpointer</span>
  </aside>

  <main
    class="-mt-6 flex flex-1 flex-col rounded-t-3xl bg-slate-50 px-6 pt-8 pb-[calc(env(safe-area-inset-bottom)+1.5rem)]
      md:mt-0 md:flex-[999_1_480px] md:items-center md:justify-center md:rounded-none md:px-4 md:py-12"
  >
    <form
      onsubmit={forgot ? sendForgot : submit}
      class="mx-auto flex w-full max-w-sm flex-col gap-5 md:mx-0 md:rounded-xl md:border md:border-slate-200
        md:bg-surface md:p-8 md:shadow-sm"
    >
      <div class="flex flex-col gap-1">
        <h2 class="text-[22px] font-bold">{forgot ? 'Recuperar el acceso' : 'Iniciar sesión'}</h2>
        <p class="text-sm text-slate-600">
          {forgot
            ? 'Te enviaremos un enlace para elegir una contraseña nueva (o definirla, si aún no lo has hecho).'
            : 'Ingresa tu usuario o correo y tu contraseña.'}
        </p>
      </div>

      {#if error}
        <div class="alert-error" role="alert">{error}</div>
      {/if}

      <div class="flex flex-col gap-1.5">
        <label for="lg-user" class="label">Usuario o correo</label>
        <input id="lg-user" class="input max-md:h-12" type="text" autocomplete="username" autocapitalize="none"
          autocorrect="off" spellcheck="false" enterkeyhint="next" required bind:value={username} />
      </div>
      {#if forgot}
        {#if forgotSent}<div class="alert-info" role="status">{forgotSent}</div>{/if}
        <button type="submit" class="btn-primary mt-1 max-md:h-12 max-md:text-base" disabled={loading}>
          {loading ? 'Enviando…' : 'Enviarme el enlace'}
        </button>
        <button type="button" class="cursor-pointer text-sm text-primary hover:underline" onclick={toggleForgot}>Volver a iniciar sesión</button>
      {:else}
      <div class="flex flex-col gap-1.5">
        <div class="flex items-baseline justify-between gap-2">
          <label for="lg-pass" class="label">Contraseña</label>
          {#if mailEnabled}
            <button type="button" class="cursor-pointer text-xs text-primary hover:underline" onclick={toggleForgot}>¿Olvidaste tu contraseña?</button>
          {/if}
        </div>
        <div class="relative">
          <input id="lg-pass" class="input pr-12 max-md:h-12" type={showPassword ? 'text' : 'password'}
            autocomplete="current-password" autocapitalize="none" autocorrect="off" spellcheck="false"
            enterkeyhint="go" required bind:value={password} />
          <button
            type="button"
            class="absolute inset-y-0 right-0 flex w-12 cursor-pointer items-center justify-center rounded-r-lg
              text-slate-500 hover:text-slate-900 focus-visible:text-primary focus-visible:outline-none"
            aria-label={showPassword ? 'Ocultar contraseña' : 'Mostrar contraseña'}
            aria-pressed={showPassword}
            onclick={() => (showPassword = !showPassword)}
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
              stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              {#if showPassword}
                <path d="M9.88 9.88a3 3 0 1 0 4.24 4.24" />
                <path d="M10.73 5.08A10.43 10.43 0 0 1 12 5c7 0 10 7 10 7a13.16 13.16 0 0 1-1.67 2.68" />
                <path d="M6.61 6.61A13.53 13.53 0 0 0 2 12s3 7 10 7a9.74 9.74 0 0 0 5.39-1.61" />
                <path d="M2 2l20 20" />
              {:else}
                <path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z" />
                <circle cx="12" cy="12" r="3" />
              {/if}
            </svg>
          </button>
        </div>
      </div>
      <button type="submit" class="btn-primary mt-1 max-md:h-12 max-md:text-base" disabled={loading}>
        {loading ? 'Entrando…' : 'Entrar'}
      </button>
      <p class="text-center text-xs text-slate-500">
        ¿Te crearon una cuenta? Define tu contraseña con el enlace que recibiste por correo{mailEnabled ? ' o pídelo de nuevo con «¿Olvidaste tu contraseña?»' : ''}.
      </p>
      {/if}
    </form>

    <span class="mt-auto pt-10 text-center text-[13px] text-slate-500 md:hidden">
      © {new Date().getFullYear()} nullpointer
    </span>
  </main>
</div>
