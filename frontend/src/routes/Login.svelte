<script>
  import { api } from '../lib/api.js'

  let username = $state('')
  let password = $state('')
  let error = $state('')
  let loading = $state(false)

  async function submit(e) {
    e.preventDefault()
    error = ''
    loading = true
    try {
      await api.login(username, password)
      location.hash = '#/'
    } catch (err) {
      error = err.message
    } finally {
      loading = false
    }
  }
</script>

<div class="flex min-h-screen flex-wrap">
  <aside class="flex flex-[1_1_420px] flex-col justify-between gap-8 bg-primary p-12 text-white">
    <div class="flex items-center gap-3">
      <div class="flex size-10 items-center justify-center rounded-[10px] bg-surface text-primary">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
          stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M4 7V4h16v3" /><path d="M9 20h6" /><path d="M12 4v16" />
        </svg>
      </div>
      <span class="text-lg font-bold">Typst Editor</span>
    </div>
    <div class="flex max-w-md flex-col gap-3">
      <h1 class="text-4xl leading-tight font-bold tracking-tight">Bienvenido de nuevo</h1>
      <p class="text-base leading-relaxed text-blue-100">
        Inicia sesión con la cuenta que te asignó el administrador.
      </p>
    </div>
    <span class="text-[13px] text-blue-200">© {new Date().getFullYear()} Typst Editor</span>
  </aside>

  <main class="flex flex-[999_1_480px] items-center justify-center px-4 py-12">
    <form onsubmit={submit} class="card flex w-full max-w-sm flex-col gap-5 p-8">
      <div class="flex flex-col gap-1">
        <h2 class="text-[22px] font-bold">Iniciar sesión</h2>
        <p class="text-sm text-slate-600">Ingresa tu usuario y contraseña.</p>
      </div>

      {#if error}
        <div class="alert-error" role="alert">{error}</div>
      {/if}

      <div class="flex flex-col gap-1.5">
        <label for="lg-user" class="label">Usuario</label>
        <input id="lg-user" class="input" type="text" autocomplete="username" required bind:value={username} />
      </div>
      <div class="flex flex-col gap-1.5">
        <label for="lg-pass" class="label">Contraseña</label>
        <input id="lg-pass" class="input" type="password" autocomplete="current-password" required
          bind:value={password} />
      </div>
      <button type="submit" class="btn-primary" disabled={loading}>
        {loading ? 'Entrando…' : 'Entrar'}
      </button>
    </form>
  </main>
</div>
