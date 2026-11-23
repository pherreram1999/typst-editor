<script>
  // Cambiar la propia contraseña. forced: la cuenta tiene una contraseña
  // temporal (alta o restablecimiento sin correo) y hay que cambiarla antes
  // de usar la aplicación (el servidor responde 403 a todo lo demás).
  import { api } from './api.js'
  import { session } from './session.svelte.js'

  let { forced = false, ondone } = $props()

  let current = $state('')
  let next = $state('')
  let next2 = $state('')
  let show = $state(false)
  let busy = $state(false)
  let error = $state('')

  const mismatch = $derived(next2 !== '' && next !== next2)

  async function submit(e) {
    e.preventDefault()
    if (next !== next2) {
      error = 'Las contraseñas no coinciden.'
      return
    }
    busy = true
    error = ''
    try {
      session.user = await api.changePassword(current, next)
      current = next = next2 = ''
      ondone?.()
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }
</script>

<form class="flex flex-col gap-4" onsubmit={submit}>
  {#if error}<div class="alert-error" role="alert">{error}</div>{/if}
  <input type="text" class="hidden" autocomplete="username" value={session.user?.username ?? ''} readonly tabindex="-1" aria-hidden="true" />
  <div class="flex flex-col gap-1.5">
    <label class="label" for="cp-current">{forced ? 'Contraseña temporal' : 'Contraseña actual'}</label>
    <input id="cp-current" class="input" type={show ? 'text' : 'password'} autocomplete="current-password" required bind:value={current} />
  </div>
  <div class="flex flex-col gap-1.5">
    <div class="flex items-baseline justify-between">
      <label class="label" for="cp-new">Contraseña nueva</label>
      <button type="button" class="cursor-pointer text-xs text-primary hover:underline" onclick={() => (show = !show)}>{show ? 'Ocultar' : 'Mostrar'}</button>
    </div>
    <input id="cp-new" class="input" type={show ? 'text' : 'password'} autocomplete="new-password" minlength="8" maxlength="72" required bind:value={next} />
    <span class="text-xs text-slate-500">Mínimo 8 caracteres.</span>
  </div>
  <div class="flex flex-col gap-1.5">
    <label class="label" for="cp-new2">Repite la contraseña nueva</label>
    <input id="cp-new2" class="input {mismatch ? 'border-red-700' : ''}" type={show ? 'text' : 'password'} autocomplete="new-password" required aria-invalid={mismatch} bind:value={next2} />
    {#if mismatch}<span class="text-xs text-red-700">Las contraseñas no coinciden.</span>{/if}
  </div>
  <button class="btn-primary w-full" disabled={busy || mismatch}>{busy ? 'Guardando…' : forced ? 'Cambiar y continuar' : 'Cambiar contraseña'}</button>
</form>
