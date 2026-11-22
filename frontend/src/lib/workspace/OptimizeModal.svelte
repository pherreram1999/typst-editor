<script>
  // Optimización sin pérdida de las imágenes PNG/JPEG ya subidas. Se procesan
  // de una en una (cada petición optimiza una imagen) para mostrar el
  // progreso y poder detenerse.
  import Modal from '../Modal.svelte'
  import { api } from '../api.js'

  let { projectId, files = [], open = false, onclose, ondone } = $props()

  const IMAGE = /\.(png|jpe?g)$/i
  const images = $derived(files.filter((f) => IMAGE.test(f.path)).sort((a, b) => b.size - a.size))
  const total = $derived(images.reduce((s, f) => s + f.size, 0))

  let running = $state(false)
  let stop = false
  let results = $state({}) // path -> { before, after, note } | { error }
  let current = $state('')

  const done = $derived(Object.keys(results).length)
  const saved = $derived(Object.values(results).reduce((s, r) => s + (r.before ? r.before - r.after : 0), 0))

  $effect(() => {
    if (open) {
      results = {}
      current = ''
    }
  })

  async function run() {
    running = true
    stop = false
    let changed = false
    for (const f of images) {
      if (stop) break
      if (results[f.path]) continue
      current = f.path
      try {
        const { result } = await api.optimizeImage(projectId, f.path)
        results[f.path] = result
        changed ||= result.after < result.before
      } catch (e) {
        results[f.path] = { error: e.message }
      }
    }
    current = ''
    running = false
    if (changed) ondone?.()
  }

  function close() {
    if (running) {
      stop = true
      return
    }
    onclose?.()
  }

  const fmt = (n) => (n < 1024 ? `${n} B` : n < 1048576 ? `${Math.round(n / 1024)} KB` : `${(n / 1048576).toFixed(1)} MB`)
</script>

<Modal {open} title="Optimizar imágenes" wide onclose={close}>
  <div class="flex flex-col gap-4">
    <p class="text-sm text-slate-600">
      Reduce el peso de las imágenes PNG y JPEG <strong>sin cambiar un solo píxel</strong>: los diagramas y capturas se revisan
      con el mismo detalle y ocupan el mismo espacio en el documento. Los PNG con pocos colores (diagramas) son los que más
      bajan; los JPEG solo pierden metadatos innecesarios.
    </p>
    {#if !images.length}
      <p class="text-sm text-slate-500">El proyecto no tiene imágenes PNG ni JPEG.</p>
    {:else}
      <div class="flex items-center justify-between gap-3 text-sm">
        <span class="text-slate-600">
          {images.length} imágenes · {fmt(total)}
          {#if done}· procesadas {done} de {images.length} · ahorro <strong class="text-emerald-700">{fmt(saved)}</strong>{/if}
        </span>
        {#if running}
          <button type="button" class="btn-secondary btn-sm" onclick={() => (stop = true)}>Detener</button>
        {:else}
          <button type="button" class="btn-primary btn-sm" onclick={run} disabled={done === images.length}>
            {done ? 'Continuar' : 'Optimizar todas'}
          </button>
        {/if}
      </div>
      {#if running || done}
        <div class="h-2 overflow-hidden rounded-full bg-slate-200">
          <div class="h-full bg-primary transition-[width]" style="width: {(done / images.length) * 100}%"></div>
        </div>
      {/if}
      <ul class="max-h-80 overflow-y-auto rounded-lg border border-slate-200 text-[13px]">
        {#each images as f (f.path)}
          {@const r = results[f.path]}
          <li class="flex items-center gap-3 border-b border-slate-100 px-3 py-1.5 last:border-0">
            <span class="min-w-0 flex-1 truncate font-mono text-xs text-slate-700" title={f.path}>{f.path}</span>
            {#if current === f.path}
              <span class="flex items-center gap-1.5 text-amber-700"><span class="size-2 animate-pulse rounded-full bg-amber-500"></span>optimizando…</span>
            {:else if r?.error}
              <span class="text-red-700">{r.error}</span>
            {:else if r && r.after < r.before}
              <span class="tabular-nums text-slate-500">{fmt(r.before)} → <strong class="text-emerald-700">{fmt(r.after)}</strong></span>
              <span class="w-12 text-right tabular-nums font-semibold text-emerald-700">−{Math.round((1 - r.after / r.before) * 100)} %</span>
            {:else if r}
              <span class="text-slate-500">{r.note || 'sin cambios'}</span>
            {:else}
              <span class="tabular-nums text-slate-500">{fmt(f.size)}</span>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</Modal>
