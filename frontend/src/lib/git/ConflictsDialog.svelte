<script>
  // Diálogo «Conflictos» tras un pull que no se pudo fusionar solo (como en
  // JetBrains): lista los archivos en conflicto y, para cada uno, aceptar tu
  // versión, la del repositorio, o abrir la ventana de fusión. Con todos
  // resueltos, «Completar fusión» envía las resoluciones (onresolve) y el
  // servidor crea el commit de fusión. Hasta entonces no cambia nada: el
  // proyecto sigue como antes del pull.
  //
  // Props: conflict { branch, theirs, files: [{ path, base, ours, theirs, binary, has_* , ours_blob }] }
  //        onresolve(files) -> Promise, onclose()
  import Modal from '../Modal.svelte'
  import MergeView from './MergeView.svelte'

  let { conflict, onresolve, onclose } = $props()

  let res = $state({}) // path -> { take } | { content }
  let merging = $state(null) // archivo abierto en la ventana de fusión
  let busy = $state(false)
  let error = $state('')

  const files = $derived(conflict?.files ?? [])
  const done = $derived(files.filter((f) => res[f.path]).length)

  const sideState = (f, side) => (!f[`has_${side === 'theirs' ? 'theirs' : side}`] ? 'Eliminado' : 'Modificado')
  const canMerge = (f) => !f.binary && f.has_ours && f.has_theirs
  const label = (r) => (!r ? '' : r.take === 'ours' ? 'Tu versión' : r.take === 'theirs' ? 'Versión del repositorio' : 'Fusionado')

  function take(f, side) {
    res[f.path] = { take: side }
  }
  function merged(text) {
    res[merging.path] = { content: text }
    merging = null
  }

  async function complete() {
    error = ''
    busy = true
    try {
      await onresolve(files.map((f) => ({ path: f.path, ours_blob: f.ours_blob, ...res[f.path] })))
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }
  function close() {
    if (busy) return
    if (done && !confirm('¿Cancelar la fusión? Se perderá lo que has resuelto y el proyecto quedará como antes del pull.')) return
    onclose?.()
  }
</script>

<Modal open={!!conflict && !merging} title="Conflictos al hacer pull" wide="xl" fit onclose={close}>
  <div class="flex min-h-0 flex-1 flex-col gap-4">
    <p class="shrink-0 text-sm text-slate-600">
      Tus cambios y los de <code class="font-mono text-[13px]">origin/{conflict?.branch}</code> tocan las mismas partes de
      {files.length === 1 ? 'este archivo' : `estos ${files.length} archivos`}. Elige qué versión conservar o fusiónalos
      línea a línea. El proyecto no cambia hasta que completes la fusión.
    </p>
    {#if error}<div class="alert-error shrink-0">{error}</div>{/if}

    <div class="min-h-0 flex-1 overflow-auto rounded-lg border border-slate-200">
      <table class="w-full text-sm">
        <thead class="sticky top-0 bg-slate-50 text-left text-xs text-slate-500">
          <tr>
            <th class="px-3 py-2 font-medium">Archivo</th>
            <th class="px-3 py-2 font-medium">Tuyo</th>
            <th class="px-3 py-2 font-medium">Repositorio</th>
            <th class="px-3 py-2"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100">
          {#each files as f (f.path)}
            <tr class={res[f.path] ? 'bg-green-50/40' : ''}>
              <td class="px-3 py-2.5">
                <div class="flex items-center gap-2">
                  {#if res[f.path]}
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-green-600" aria-label="Resuelto"><path d="M20 6 9 17l-5-5" /></svg>
                  {:else}
                    <span class="size-2 shrink-0 rounded-full bg-red-500" aria-label="Sin resolver"></span>
                  {/if}
                  <span class="font-mono text-[13px] break-all">{f.path}</span>
                </div>
                {#if res[f.path]}<p class="mt-0.5 pl-6 text-xs text-green-700">{label(res[f.path])}</p>{/if}
                {#if f.binary}<p class="mt-0.5 pl-6 text-xs text-slate-500">Binario o muy grande: elige una versión entera.</p>{/if}
              </td>
              <td class="px-3 py-2.5 text-xs whitespace-nowrap text-slate-600">{sideState(f, 'ours')}</td>
              <td class="px-3 py-2.5 text-xs whitespace-nowrap text-slate-600">{sideState(f, 'theirs')}</td>
              <td class="px-3 py-2.5">
                <div class="flex justify-end gap-1.5">
                  <button type="button" class="btn-secondary btn-sm !h-8 whitespace-nowrap" onclick={() => take(f, 'ours')}>Aceptar tuyo</button>
                  <button type="button" class="btn-secondary btn-sm !h-8 whitespace-nowrap" onclick={() => take(f, 'theirs')}>Aceptar del repositorio</button>
                  <button type="button" class="btn-primary btn-sm !h-8 whitespace-nowrap" onclick={() => (merging = f)} disabled={!canMerge(f)} title={canMerge(f) ? 'Abrir la ventana de fusión' : 'Solo se puede elegir una versión entera'}>Fusionar…</button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    <div class="flex shrink-0 items-center justify-between gap-2">
      <span class="text-xs text-slate-500">{done} de {files.length} resueltos</span>
      <div class="flex gap-2">
        <button type="button" class="btn-secondary btn-sm" onclick={close} disabled={busy}>Cancelar</button>
        <button type="button" class="btn-primary btn-sm" onclick={complete} disabled={busy || done < files.length}>
          {busy ? 'Fusionando…' : 'Completar fusión'}
        </button>
      </div>
    </div>
  </div>
</Modal>

{#if merging}
  <MergeView file={merging} branch={conflict.branch} onapply={merged} oncancel={() => (merging = null)} />
{/if}
