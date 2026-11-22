<script>
  // PDF renderizado por `typst compile` y mostrado con el visor del navegador.
  // Solo se recompila cuando la pestaña está visible y el proyecto cambió.
  import { onDestroy, untrack } from 'svelte'
  import { api } from '../api.js'

  let { projectId, refreshKey, active, filename = 'documento.pdf', auto = $bindable(true) } = $props()

  let url = $state('')
  let loading = $state(false)
  let error = $state('')
  let diagnostics = $state([])
  let loadedKey = -1
  let lastMs = 0
  let timer

  // Con documentos que tardan en compilar se espera una pausa mayor al escribir.
  function schedule() {
    clearTimeout(timer)
    timer = setTimeout(load, Math.min(10000, Math.max(1500, lastMs)))
  }

  async function load() {
    if (loading) return
    loading = true
    error = ''
    const key = refreshKey
    const t0 = performance.now()
    try {
      const { blob } = await api.pdf(projectId)
      const next = URL.createObjectURL(new Blob([blob], { type: 'application/pdf' }))
      if (url) URL.revokeObjectURL(url)
      url = next
      diagnostics = []
      loadedKey = key
    } catch (e) {
      error = e.message
      diagnostics = e.data?.diagnostics?.filter((d) => d.severity === 'error') ?? []
      loadedKey = key
    } finally {
      loading = false
      lastMs = performance.now() - t0
      // Hubo cambios mientras se compilaba.
      if (active && auto && refreshKey !== loadedKey) schedule()
    }
  }

  $effect(() => {
    const key = refreshKey
    if (!active) return
    if (loadedKey === -1) {
      load()
    } else if (auto && key !== loadedKey) {
      untrack(schedule)
    }
  })

  onDestroy(() => {
    clearTimeout(timer)
    if (url) URL.revokeObjectURL(url)
  })
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="flex h-10 shrink-0 items-center justify-between gap-2 border-b border-slate-200 bg-surface px-3 text-[13px]">
    <label class="flex items-center gap-2 text-slate-600">
      <input type="checkbox" bind:checked={auto} class="size-3.5 accent-blue-800" />
      Actualizar automáticamente
    </label>
    <div class="flex items-center gap-2">
      <button type="button" class="btn-secondary btn-sm !h-7" onclick={load} disabled={loading}>
        {loading ? 'Compilando…' : 'Compilar PDF'}
      </button>
      {#if url}
        <a class="btn-primary btn-sm !h-7 no-underline hover:text-white" href={url} download={filename}>Descargar</a>
      {/if}
    </div>
  </div>
  {#if error}
    <div class="m-3 flex flex-col gap-2">
      <div class="alert-error">No se pudo generar el PDF.</div>
      {#each diagnostics as dg}
        <p class="font-mono text-xs text-red-800">{dg.file}:{dg.line}:{dg.column} — {dg.message}</p>
      {:else}
        <pre class="whitespace-pre-wrap font-mono text-xs text-red-800">{error}</pre>
      {/each}
    </div>
  {/if}
  <div class="min-h-0 flex-1 bg-slate-200/70">
    {#if url}
      <iframe title="PDF" src={url} class="size-full border-0"></iframe>
    {:else if loading}
      <p class="p-6 text-center text-sm text-slate-500">Generando PDF…</p>
    {/if}
  </div>
</div>
