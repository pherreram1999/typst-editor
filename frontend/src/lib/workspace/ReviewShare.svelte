<script>
  // Sección «Enlace de revisión» del diálogo Compartir: una compilación
  // guardada del proyecto (solo una; actualizarla reemplaza la anterior) que
  // cualquiera con el enlace puede ver sin cuenta y sin recompilar. La
  // descarga del PDF se puede desactivar.
  import { api } from '../api.js'

  let { project, open } = $props()

  let review = $state(null) // respuesta de GET /review
  let loading = $state(false)
  let busy = $state('') // '' | publish | download | token | delete
  let error = $state('')
  let copied = $state(false)
  let allowNew = $state(true) // permitir descarga al crear el enlace

  async function load() {
    loading = true
    error = ''
    try {
      review = await api.getReview(project.id)
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
  $effect(() => {
    if (open) load()
  })

  const link = $derived(review?.token ? `${location.origin}${location.pathname}#/r/${review.token}` : '')

  async function run(kind, fn) {
    error = ''
    busy = kind
    try {
      review = await fn()
    } catch (e) {
      error = e.data?.diagnostics?.length
        ? `${e.message}. Revisa el panel «Problemas».`
        : e.message
    } finally {
      busy = ''
    }
  }
  const publish = () => run('publish', () => api.publishReview(project.id, review?.enabled ? {} : { allow_download: allowNew }))
  const setDownload = (v) => run('download', () => api.updateReview(project.id, { allow_download: v }))
  function newToken() {
    if (!confirm('Se generará un enlace nuevo y el actual dejará de funcionar. ¿Continuar?')) return
    run('token', () => api.updateReview(project.id, { new_token: true }))
  }
  function disable() {
    if (!confirm('¿Desactivar el enlace de revisión? Quien lo tenga ya no podrá ver el documento y se borrará la compilación guardada.')) return
    run('delete', () => api.deleteReview(project.id))
  }
  async function copy() {
    try {
      await navigator.clipboard.writeText(link)
    } catch {
      const i = document.getElementById('review-link')
      i?.select()
      document.execCommand('copy')
    }
    copied = true
    setTimeout(() => (copied = false), 1800)
  }

  const fmtDate = (d) => (d ? new Date(d).toLocaleString('es-MX', { dateStyle: 'medium', timeStyle: 'short' }) : '')
  const fmtSize = (n) => (n < 1048576 ? `${Math.max(1, Math.round(n / 1024))} KB` : `${(n / 1048576).toFixed(1)} MB`)
</script>

<div class="border-t border-slate-200 pt-4">
  <div class="mb-2 flex items-center gap-2">
    <p class="text-[13px] font-semibold text-slate-800">Enlace de revisión</p>
    {#if review?.enabled}<span class="badge bg-green-50 text-green-700">Activo</span>{/if}
  </div>
  {#if error}<div class="alert-error mb-3">{error}</div>{/if}

  {#if loading && !review}
    <p class="text-sm text-slate-500">Cargando…</p>
  {:else if review && !review.enabled}
    <p class="text-xs text-slate-500">
      Guarda una versión compilada del documento y compártela con un enlace: quien lo abra podrá leerlo sin cuenta y sin
      ver el código. Es una foto fija: los cambios posteriores no aparecen hasta que actualices la versión.
    </p>
    {#if review.can_manage}
      <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
        <label class="flex cursor-pointer items-center gap-2 text-sm">
          <input type="checkbox" class="size-4 accent-[var(--color-primary)]" bind:checked={allowNew} />
          Permitir descargar el PDF
        </label>
        <button type="button" class="btn-primary btn-sm" onclick={publish} disabled={!!busy}>
          {busy === 'publish' ? 'Compilando…' : 'Crear enlace de revisión'}
        </button>
      </div>
    {:else}
      <p class="mt-2 text-xs text-slate-500">Solo el propietario (o un administrador) puede crear el enlace.</p>
    {/if}
  {:else if review?.enabled}
    <div class="flex gap-2">
      <label class="sr-only" for="review-link">Enlace de revisión</label>
      <input id="review-link" class="input !h-9 font-mono !text-xs" readonly value={link} onfocus={(e) => e.currentTarget.select()} />
      <button type="button" class="btn-secondary btn-sm shrink-0" onclick={copy}>{copied ? '¡Copiado!' : 'Copiar'}</button>
      <a class="btn-secondary btn-sm shrink-0" href={link} target="_blank" rel="noopener">Abrir</a>
    </div>
    <p class="mt-2 text-xs text-slate-500">
      Versión del {fmtDate(review.compiled_at)}{review.compiled_by ? ` (por ${review.compiled_by})` : ''}
      {#if review.pages}· {review.pages} {review.pages === 1 ? 'página' : 'páginas'}{/if}
      {#if review.pdf_size}· PDF de {fmtSize(review.pdf_size)}{/if}
    </p>

    {#if review.can_manage}
      <label class="mt-3 flex cursor-pointer items-center justify-between gap-3 rounded-lg border border-slate-200 px-3 py-2 text-sm">
        <span>
          <span class="block font-medium text-slate-900">Permitir descargar el PDF</span>
          <span class="block text-xs text-slate-500">
            {review.allow_download ? 'Los revisores pueden descargar el PDF.' : 'Solo pueden ver las páginas en el navegador; el PDF no se sirve.'}
          </span>
        </span>
        <input
          type="checkbox"
          role="switch"
          class="size-4 shrink-0 accent-[var(--color-primary)]"
          checked={review.allow_download}
          disabled={!!busy}
          onchange={(e) => setDownload(e.currentTarget.checked)}
        />
      </label>
      <div class="mt-3 flex flex-wrap gap-2">
        <button type="button" class="btn-primary btn-sm" onclick={publish} disabled={!!busy} title="Compila el estado actual y reemplaza la versión guardada; el enlace no cambia">
          {busy === 'publish' ? 'Compilando…' : 'Actualizar versión'}
        </button>
        <button type="button" class="btn-secondary btn-sm" onclick={newToken} disabled={!!busy} title="El enlace actual deja de funcionar">Nuevo enlace</button>
        <button type="button" class="btn-secondary btn-sm !text-red-700" onclick={disable} disabled={!!busy}>Desactivar</button>
      </div>
    {:else}
      <p class="mt-2 text-xs text-slate-500">
        {review.allow_download ? 'Los revisores pueden descargar el PDF.' : 'La descarga del PDF está desactivada.'} Solo el propietario puede actualizar o desactivar el enlace.
      </p>
    {/if}
  {/if}
</div>
