<script>
  // Vista previa en vivo: el backend compila con `typst compile --format svg`
  // y devuelve una imagen SVG por página. Se recompila (con antirrebote) cada
  // vez que cambia la revisión del proyecto.
  import { onDestroy } from 'svelte'
  import { api } from '../api.js'

  let { projectId, refreshKey, ondiagnostic, onresult } = $props()

  let pages = $state([]) // blob URLs
  let result = $state(null) // última respuesta
  let compiling = $state(false)
  let error = $state('')
  let zoom = $state(100)
  let lastRev = -1
  let timer
  let again = false

  async function compile() {
    if (compiling) {
      again = true
      return
    }
    compiling = true
    error = ''
    try {
      const r = await api.preview(projectId)
      if (r.rev !== lastRev || !r.ok) {
        lastRev = r.rev
        result = r
        onresult?.(r)
        if (r.ok) {
          const urls = r.pages.map((svg) => URL.createObjectURL(new Blob([svg], { type: 'image/svg+xml' })))
          pages.forEach(URL.revokeObjectURL)
          pages = urls
        }
      }
    } catch (e) {
      error = e.message
    } finally {
      compiling = false
      if (again) {
        again = false
        compile()
      }
    }
  }

  $effect(() => {
    refreshKey // dependencia
    clearTimeout(timer)
    timer = setTimeout(compile, lastRev === -1 ? 0 : 450)
  })

  onDestroy(() => {
    clearTimeout(timer)
    pages.forEach(URL.revokeObjectURL)
  })

  const errors = $derived(result?.diagnostics?.filter((d) => d.severity === 'error') ?? [])
  const warnings = $derived(result?.diagnostics?.filter((d) => d.severity === 'warning') ?? [])
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="flex h-10 shrink-0 items-center justify-between gap-2 border-b border-slate-200 bg-white px-3 text-[13px]">
    <div class="flex items-center gap-2 text-slate-600">
      {#if compiling}
        <span class="size-2 animate-pulse rounded-full bg-amber-500"></span> Compilando…
      {:else if result && !result.ok}
        <span class="size-2 rounded-full bg-red-600"></span> Error de compilación
      {:else if result}
        <span class="size-2 rounded-full bg-emerald-600"></span>
        {pages.length} {pages.length === 1 ? 'página' : 'páginas'}
      {/if}
    </div>
    <div class="flex items-center gap-1">
      <button type="button" class="btn-secondary btn-sm !h-7 !px-2" onclick={() => (zoom = Math.max(40, zoom - 10))} aria-label="Alejar">−</button>
      <button type="button" class="w-12 cursor-pointer text-center tabular-nums text-slate-600" onclick={() => (zoom = 100)} title="Restablecer">{zoom}%</button>
      <button type="button" class="btn-secondary btn-sm !h-7 !px-2" onclick={() => (zoom = Math.min(250, zoom + 10))} aria-label="Acercar">+</button>
    </div>
  </div>

  {#if error}
    <div class="alert-error m-3">{error}</div>
  {/if}

  {#if result && (errors.length || warnings.length || (!result.ok && result.output))}
    <div class="max-h-48 shrink-0 overflow-y-auto border-b border-slate-200 bg-white text-[13px]">
      {#each [...errors, ...warnings] as dg}
        <button
          type="button"
          class="flex w-full cursor-pointer items-start gap-2 px-3 py-1.5 text-left hover:bg-slate-50"
          onclick={() => ondiagnostic?.(dg)}
        >
          <span class="badge shrink-0 {dg.severity === 'error' ? 'bg-red-100 text-red-800' : 'bg-amber-100 text-amber-800'}"
            >{dg.severity === 'error' ? 'error' : 'aviso'}</span
          >
          <span class="shrink-0 font-mono text-slate-500">{dg.file}:{dg.line}:{dg.column}</span>
          <span class="text-slate-800">{dg.message}</span>
        </button>
      {/each}
      {#if !result.ok && !errors.length}
        <pre class="whitespace-pre-wrap px-3 py-2 font-mono text-xs text-red-800">{result.output}</pre>
      {/if}
    </div>
  {/if}

  <div class="min-h-0 flex-1 overflow-auto bg-slate-200/70 p-4">
    <div class="mx-auto flex flex-col items-center gap-4" style="width: {zoom}%">
      {#each pages as src, i (src)}
        <img
          {src}
          alt="Página {i + 1}"
          class="w-full bg-white shadow-md ring-1 ring-slate-300 transition-opacity {result && !result.ok ? 'opacity-50' : ''}"
        />
      {/each}
      {#if !pages.length && !compiling && result?.ok}
        <p class="text-sm text-slate-500">El documento no tiene páginas.</p>
      {/if}
    </div>
  </div>
</div>
