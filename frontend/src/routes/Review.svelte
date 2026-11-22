<script>
  // Vista pública de un enlace de revisión (#/r/<token>): muestra la versión
  // compilada guardada, sin cuenta y sin compilar. Las páginas son SVG que se
  // montan solo cerca de la pantalla (un documento largo no carga todo a la
  // vez) y el PDF se ofrece solo si el propietario permitió descargarlo.
  import { reviewInfo, reviewPageURL, reviewPdfURL } from '../lib/api.js'

  let { token } = $props()

  let info = $state(null)
  let error = $state('')
  let zoom = $state(0) // 0 = aún sin calcular (ajustar al ancho)
  let near = $state(new Set()) // páginas cerca de la pantalla (montadas)
  let current = $state(1)
  let scroller = $state()

  const PX = 96 / 72 // pt → px
  const maxW = $derived(info ? Math.max(...info.pages.map((p) => p.width)) : 595)

  $effect(() => {
    const t = token
    info = null
    error = ''
    reviewInfo(t).then(
      (r) => {
        info = r
        document.title = `${r.name} · Revisión`
      },
      (e) => (error = e.status === 404 ? 'Este enlace de revisión no existe o fue desactivado.' : e.message),
    )
  })

  // Ajustar al ancho al cargar (como máximo 100 %).
  $effect(() => {
    if (!info || zoom || !scroller) return
    zoom = Math.min(1, Math.max(0.3, (scroller.clientWidth - 48) / (maxW * PX)))
  })

  const setZoom = (z) => (zoom = Math.min(4, Math.max(0.3, Math.round(z * 100) / 100)))

  // Montar las páginas cercanas y seguir la página actual.
  function observe(node, n) {
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          const s = new Set(near)
          if (e.isIntersecting) s.add(n)
          else s.delete(n)
          near = s
        }
      },
      { root: scroller, rootMargin: '1200px 0px' },
    )
    const cur = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && (current = n), {
      root: scroller,
      rootMargin: '-45% 0px -55% 0px',
    })
    io.observe(node)
    cur.observe(node)
    return { destroy: () => (io.disconnect(), cur.disconnect()) }
  }

  function onwheel(e) {
    if (!e.ctrlKey) return
    e.preventDefault()
    setZoom(zoom * (e.deltaY < 0 ? 1.1 : 1 / 1.1))
  }
  $effect(() => {
    if (!scroller) return
    scroller.addEventListener('wheel', onwheel, { passive: false })
    return () => scroller.removeEventListener('wheel', onwheel)
  })

  function goTo(n) {
    n = Math.min(Math.max(1, n), info.pages.length)
    scroller.querySelector(`[data-page="${n}"]`)?.scrollIntoView({ block: 'start' })
  }

  const fmtDate = (d) => new Date(d).toLocaleString('es-MX', { dateStyle: 'long', timeStyle: 'short' })
  const fmtSize = (n) => (n < 1048576 ? `${Math.max(1, Math.round(n / 1024))} KB` : `${(n / 1048576).toFixed(1)} MB`)
  // Sin descarga permitida, no ofrecer «Guardar imagen» en el menú contextual
  // (solo disuade: lo que se ve en pantalla siempre se puede capturar).
  const noMenu = (e) => !info?.allow_download && e.preventDefault()
</script>

<div class="flex h-screen flex-col bg-slate-200">
  <header class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b border-slate-300 bg-surface px-4 py-2.5">
    <div class="min-w-0 flex-1">
      <p class="truncate text-sm font-semibold text-slate-900">{info?.name ?? 'Documento en revisión'}</p>
      {#if info}
        <p class="truncate text-xs text-slate-500">
          Versión para revisión de {info.owner} · {fmtDate(info.compiled_at)} · {info.pages.length}
          {info.pages.length === 1 ? 'página' : 'páginas'}
        </p>
      {/if}
    </div>
    {#if info}
      <div class="flex items-center gap-1 text-sm text-slate-600">
        <label class="sr-only" for="review-page">Página</label>
        <input
          id="review-page"
          class="input !h-8 w-14 !px-2 text-center"
          type="number"
          min="1"
          max={info.pages.length}
          value={current}
          onchange={(e) => goTo(+e.currentTarget.value)}
        />
        <span>/ {info.pages.length}</span>
      </div>
      <div class="flex items-center gap-1">
        <button type="button" class="btn-secondary btn-sm !h-8 !w-8 !px-0" onclick={() => setZoom(zoom / 1.2)} aria-label="Alejar">−</button>
        <button type="button" class="btn-secondary btn-sm !h-8 w-16 !px-0 tabular-nums" onclick={() => setZoom(Math.min(1, (scroller.clientWidth - 48) / (maxW * PX)))} title="Ajustar al ancho">
          {Math.round(zoom * 100)} %
        </button>
        <button type="button" class="btn-secondary btn-sm !h-8 !w-8 !px-0" onclick={() => setZoom(zoom * 1.2)} aria-label="Acercar">+</button>
      </div>
      {#if info.allow_download}
        <a class="btn-primary btn-sm !h-8 gap-1.5" href={reviewPdfURL(token)} download>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3v12m-5-5 5 5 5-5" /><path d="M5 21h14" /></svg>
          Descargar PDF{info.pdf_size ? ` (${fmtSize(info.pdf_size)})` : ''}
        </a>
      {/if}
    {/if}
  </header>

  <div bind:this={scroller} class="min-h-0 flex-1 overflow-auto">
    {#if error}
      <div class="mx-auto mt-24 max-w-md px-6 text-center">
        <svg class="mx-auto mb-4 text-slate-400" width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7" /><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7" /><path d="m3 3 18 18" /></svg>
        <p class="font-semibold text-slate-800">No se puede abrir el documento</p>
        <p class="mt-1 text-sm text-slate-600">{error}</p>
      </div>
    {:else if !info}
      <p class="mt-24 text-center text-sm text-slate-500">Cargando…</p>
    {:else if zoom}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="flex w-max min-w-full flex-col items-center gap-4 p-6" oncontextmenu={noMenu}>
        {#each info.pages as pg, i (i)}
          <div
            data-page={i + 1}
            use:observe={i + 1}
            class="relative shrink-0 bg-white shadow-md"
            style="width: {pg.width * PX * zoom}px; height: {pg.height * PX * zoom}px;"
          >
            {#if near.has(i + 1)}
              <img
                src={reviewPageURL(token, i + 1, info.version)}
                alt="Página {i + 1}"
                class="block size-full select-none"
                draggable="false"
                decoding="async"
              />
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
