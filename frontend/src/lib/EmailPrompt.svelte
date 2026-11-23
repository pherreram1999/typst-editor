<script>
  // Aviso para las cuentas sin correo (anteriores al inicio de sesión por
  // correo): lo pide y envía un enlace de confirmación; se guarda al abrirlo
  // (#/cuenta/<token>). «Ahora no» lo oculta hasta la próxima sesión del navegador.
  import { onMount } from 'svelte'
  import { slide } from 'svelte/transition'
  import { session } from './session.svelte.js'
  import { api, authConfig } from './api.js'
  import { d } from './motion.js'

  const KEY = 'typsteditor.emailPromptHidden'

  let mailEnabled = $state(false)
  let hidden = $state(false)
  let email = $state('')
  let busy = $state(false)
  let error = $state('')
  let sent = $state('')

  onMount(() => {
    try {
      hidden = sessionStorage.getItem(KEY) === '1'
    } catch {
      /* sin almacenamiento */
    }
    authConfig().then((c) => (mailEnabled = c.mail_enabled), () => {})
  })

  function dismiss() {
    hidden = true
    try {
      sessionStorage.setItem(KEY, '1')
    } catch {
      /* sin almacenamiento */
    }
  }

  async function submit(e) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      const r = await api.changeEmail(email.trim())
      sent = r.sent_to
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }
</script>

{#if mailEnabled && session.user && !session.user.email && session.user.role !== 'guest' && !hidden}
  <div class="border-b border-amber-200 bg-amber-50 px-6 py-3" transition:slide={{ duration: d(180) }}>
    <div class="mx-auto flex max-w-6xl flex-wrap items-center gap-x-4 gap-y-2">
      {#if sent}
        <p class="flex-1 text-sm text-amber-900">
          Te enviamos un enlace a <strong>{sent}</strong>. Ábrelo para confirmar tu correo (vence en 48 horas).
        </p>
        <button type="button" class="btn-secondary btn-sm" onclick={dismiss}>Entendido</button>
      {:else}
        <p class="min-w-60 flex-1 text-sm text-amber-900">
          <strong>Agrega tu correo:</strong> podrás iniciar sesión con él, recuperar tu contraseña y recibir avisos de los proyectos que te compartan.
        </p>
        <form class="flex flex-wrap items-center gap-2" onsubmit={submit}>
          <label class="sr-only" for="prompt-email">Tu correo</label>
          <input id="prompt-email" class="input !h-9 !w-64" type="email" required maxlength="254" placeholder="tu@correo.com" autocomplete="email" bind:value={email} />
          <button class="btn-primary btn-sm" disabled={busy}>{busy ? 'Enviando…' : 'Confirmar correo'}</button>
          <button type="button" class="btn-secondary btn-sm" onclick={dismiss}>Ahora no</button>
        </form>
        {#if error}<p class="w-full text-xs text-red-700">{error}</p>{/if}
      {/if}
    </div>
  </div>
{/if}
