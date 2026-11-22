<script>
  // Vista previa en vivo. El backend compila a una SVG por página (con las
  // imágenes reducidas) y /preview solo devuelve, por página, su hash y su
  // tamaño. Aquí se reserva el hueco de cada página y solo se descargan y
  // montan las que están cerca de la zona visible; al recompilar, las páginas
  // cuyo hash no cambió se conservan tal cual (ni se descargan ni parpadean).
  // Así un documento de cientos de páginas no se carga entero en memoria.
  // Descargar es barato, pero cada SVG montado le cuesta al navegador varios
  // MB (se liberan más tarde, con la recolección de basura): por eso una página
  // solo se monta cuando se queda cerca de la vista un momento, y al desplazarse
  // deprisa no se decodifican las páginas que solo pasan.
  // Solo compila mientras está activa (visible en pantalla); los diagnósticos
  // se entregan con onresult y los muestra el panel de problemas.
  // Sincronización con el código (precisión de bloque, ver typst/sync.go): un
  // clic en una página llama a onjump({file, line, column}) y reveal(file,
  // line) desplaza la vista hasta el bloque de esa línea y lo marca. El mapa
  // se pide la primera vez que se usa y, desde entonces, se renueva en segundo
  // plano cuando se deja de escribir.
  import { onDestroy, untrack, flushSync } from 'svelte'
  import { SvelteMap, SvelteSet } from 'svelte/reactivity'
  import { api } from '../api.js'

  let { projectId, refreshKey, active = true, onresult, onjump } = $props()

  const FETCH_MARGIN = '150% 0px' // se descargan páginas hasta 1,5 pantallas más allá
  const MOUNT_MARGIN = '50% 0px' // y se muestran hasta media pantalla más allá
  const SETTLE = 150 // ms que una página debe seguir cerca para mostrarse
  const KEEP = 24 // páginas descargadas que se conservan fuera de la vista
  const PARALLEL = 4 // descargas simultáneas

  let pages = $state([]) // [{hash, width, height}] de la última compilación correcta
  let result = $state(null) // última respuesta
  let compiling = $state(false)
  let error = $state('')
  let zoom = $state(100)
  const ZOOM_MIN = 40
  const ZOOM_MAX = 400
  let dragging = $state(false)
  let scroller = $state()
  const near = new SvelteSet() // índices de las páginas que se descargan
  const mounted = new SvelteSet() // índices de las páginas que se muestran
  const seen = new SvelteSet() // índices de las páginas visibles ahora
  const urls = new SvelteMap() // hash -> blob URL (en orden de descarga)
  let stale = $state([]) // índice -> URL de la versión anterior, hasta que llega la nueva
  const current = $derived(seen.size ? Math.min(...seen) + 1 : 0)

  let lastRev = -1
  let doneKey = -1 // refreshKey de la última compilación terminada
  let lastMs = 0
  let timer
  let destroyed = false

  let sync = null // { rev, points: [{page, y, file, line, col}] }
  let syncLoading = null
  let syncUsed = false
  let syncTimer
  let syncBusy = $state(false)
  let marker = $state(null) // { i, top, height } en % de la página
  let markerTimer

  const queue = [] // páginas pendientes de descargar (hashes), por prioridad
  const loading = new Set()
  const failed = new Set() // no se reintentan hasta la siguiente compilación
  let inflight = 0

  // ---------- compilación ----------

  function schedule() {
    clearTimeout(timer)
    // Si el documento tarda en compilar se espera una pausa mayor al escribir.
    timer = setTimeout(compile, doneKey === -1 ? 0 : Math.min(2000, Math.max(450, lastMs / 2)))
  }

  async function compile() {
    if (compiling) return // al terminar se comprueba si hay cambios nuevos
    compiling = true
    clearTimeout(syncTimer)
    error = ''
    const key = refreshKey
    const t0 = performance.now()
    try {
      const r = await api.preview(projectId)
      if (destroyed) return
      if (r.rev !== lastRev || !r.ok) {
        lastRev = r.rev
        result = r
        onresult?.(r)
        if (r.ok) {
          setPages(r.pages)
          // Renovar el mapa de sincronización cuando se deje de escribir.
          if (syncUsed) syncTimer = setTimeout(() => ensureSync().catch(() => {}), 2500)
        }
      }
    } catch (e) {
      error = e.message
    } finally {
      lastMs = performance.now() - t0
      compiling = false
      doneKey = key
      if (!destroyed && active && refreshKey !== key) schedule()
    }
  }

  $effect(() => {
    const key = refreshKey
    if (!active || key === doneKey) return
    untrack(schedule)
  })

  function setPages(next) {
    const old = pages
    const carry = []
    for (let i = 0; i < next.length; i++) {
      // Mientras llega la nueva versión de una página se sigue viendo la anterior.
      if (old[i] && old[i].hash !== next[i].hash && !urls.has(next[i].hash)) {
        carry[i] = urls.get(old[i].hash) ?? stale[i]
      }
    }
    stale = carry
    failed.clear()
    pages = next
    evict()
  }

  // ---------- sincronización con el código ----------

  async function ensureSync() {
    if (sync && sync.rev >= lastRev) return sync
    if (!syncLoading) {
      syncBusy = true
      syncLoading = api
        .previewSync(projectId)
        .then((r) => (sync = r))
        .finally(() => {
          syncLoading = null
          syncBusy = false
        })
    }
    return syncLoading
  }

  // Clic en una página: el último bloque que empieza antes del clic.
  async function clickPage(e, i) {
    if (!onjump || !pages[i] || moved) return
    const rect = e.currentTarget.getBoundingClientRect()
    const y = ((e.clientY - rect.top) / rect.height) * pages[i].height
    syncUsed = true
    let s
    try {
      s = await ensureSync()
    } catch (err) {
      error = err.status === 422 ? '' : err.message
      return
    }
    const page = i + 1
    let best = null
    for (const p of s.points) {
      const before = p.page < page || (p.page === page && p.y <= y + 4)
      if (before && (!best || p.page > best.page || (p.page === best.page && p.y >= best.y))) best = p
    }
    best ??= s.points.find((p) => p.page === page)
    if (best) onjump({ file: best.file, line: best.line, column: best.col })
  }

  // Lleva la vista al bloque de file:line (el último que empieza en esa línea
  // o antes). Si una línea aparece en varios sitios (p. ej. un título y su
  // entrada en el índice) se elige el más cercano a sus vecinos.
  export async function reveal(file, line) {
    if (!pages.length || !scroller) return
    syncUsed = true
    let s
    try {
      s = await ensureSync()
    } catch {
      return
    }
    const pts = s.points.filter((p) => p.file === file && p.page <= pages.length)
    if (!pts.length) return
    let target = Math.max(...pts.filter((p) => p.line <= line).map((p) => p.line))
    if (!Number.isFinite(target)) target = Math.min(...pts.map((p) => p.line))
    const cands = pts.filter((p) => p.line === target)
    let p = cands[0]
    if (cands.length > 1) {
      const count = new Map()
      for (const q of pts) count.set(q.line, (count.get(q.line) ?? 0) + 1)
      let ref = null
      for (const q of pts) if (count.get(q.line) === 1 && (!ref || Math.abs(q.line - line) < Math.abs(ref.line - line))) ref = q
      if (ref) {
        const d = (q) => Math.abs(q.page - ref.page)
        p = cands.reduce((a, b) => (d(b) < d(a) || (d(b) === d(a) && b.y > a.y) ? b : a))
      }
    }
    const i = p.page - 1
    const node = [...nodes].find((n) => Number(n.dataset.i) === i)
    if (!node) return
    const r = node.getBoundingClientRect()
    const sr = scroller.getBoundingClientRect()
    const frac = p.y / pages[i].height
    const y = r.top - sr.top + frac * r.height // relativa a lo visible
    if (y < scroller.clientHeight * 0.15 || y > scroller.clientHeight * 0.85) {
      const delta = y - scroller.clientHeight / 3
      // Suave solo en distancias cortas; un salto largo recorrería (y
      // descargaría) todas las páginas intermedias.
      scroller.scrollTo({ top: scroller.scrollTop + delta, behavior: Math.abs(delta) < scroller.clientHeight * 2 ? 'smooth' : 'instant' })
    }
    marker = { i, top: frac * 100, height: (22 / pages[i].height) * 100 }
    clearTimeout(markerTimer)
    markerTimer = setTimeout(() => (marker = null), 1800)
  }

  // ---------- zoom y desplazamiento ----------

  // Cambia el zoom dejando fijo el punto de la página que está bajo (cx, cy)
  // (coordenadas de ventana; por defecto, el centro de la vista).
  function setZoom(z, cx, cy) {
    z = Math.round(Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, z)))
    if (z === zoom || !scroller) return
    const sr = scroller.getBoundingClientRect()
    cx ??= sr.left + sr.width / 2
    cy ??= sr.top + sr.height / 2
    // Página de referencia: la visible que contiene cy, o la más cercana.
    let node = null
    let best = Infinity
    for (const n of nodes) {
      if (!seen.has(Number(n.dataset.i))) continue
      const r = n.getBoundingClientRect()
      const d = cy < r.top ? r.top - cy : cy > r.bottom ? cy - r.bottom : 0
      if (d < best) [best, node] = [d, n]
    }
    const r0 = node?.getBoundingClientRect()
    zoom = z
    flushSync()
    if (node) {
      const r1 = node.getBoundingClientRect()
      scroller.scrollLeft += r1.left + ((cx - r0.left) / r0.width) * r1.width - cx
      scroller.scrollTop += r1.top + ((cy - r0.top) / r0.height) * r1.height - cy
    }
  }

  // Ctrl + rueda (o gesto de pellizco del touchpad, que llega igual).
  function onWheel(e) {
    if (!e.ctrlKey && !e.metaKey) return
    e.preventDefault()
    setZoom(zoom * Math.exp(-e.deltaY * 0.0025), e.clientX, e.clientY)
  }

  // Svelte registra onwheel como pasivo (no admite preventDefault).
  $effect(() => {
    const el = scroller
    if (!el) return
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  })

  // + / − / 0 con la vista previa enfocada (también con Ctrl, sin que el
  // navegador haga zoom de toda la página).
  function onKey(e) {
    if (e.altKey) return
    if (e.key === '+' || e.key === '=') setZoom(zoom + 10)
    else if (e.key === '-' || e.key === '_') setZoom(zoom - 10)
    else if (e.key === '0') setZoom(100)
    else return
    e.preventDefault()
  }

  // Arrastrar con el botón principal mueve la vista (mano). Si hubo
  // movimiento, el clic que sigue no salta al código.
  let drag = null
  let moved = false
  function onPointerDown(e) {
    if (e.button !== 0 || e.pointerType === 'touch') return
    // No en las barras de desplazamiento.
    const r = scroller.getBoundingClientRect()
    if (e.clientX - r.left >= scroller.clientWidth || e.clientY - r.top >= scroller.clientHeight) return
    scroller.focus({ preventScroll: true })
    drag = { x: e.clientX, y: e.clientY, left: scroller.scrollLeft, top: scroller.scrollTop, id: e.pointerId }
    moved = false
  }
  function onPointerMove(e) {
    if (!drag || e.pointerId !== drag.id) return
    const dx = e.clientX - drag.x
    const dy = e.clientY - drag.y
    if (!moved && Math.hypot(dx, dy) < 4) return
    if (!moved) {
      moved = true
      dragging = true
      scroller.setPointerCapture(e.pointerId)
    }
    scroller.scrollLeft = drag.left - dx
    scroller.scrollTop = drag.top - dy
  }
  function onPointerUp(e) {
    if (!drag || e.pointerId !== drag.id) return
    drag = null
    dragging = false
    if (scroller.hasPointerCapture(e.pointerId)) scroller.releasePointerCapture(e.pointerId)
    // El clic llega justo después de pointerup; se ignora si hubo arrastre.
    setTimeout(() => (moved = false))
  }

  // ---------- descarga de páginas ----------

  // Pide las páginas visibles primero y luego las cercanas.
  $effect(() => {
    const order = [...seen, ...[...near].filter((i) => !seen.has(i))]
    const hashes = order.map((i) => pages[i]?.hash).filter(Boolean)
    untrack(() => {
      queue.length = 0
      for (const h of hashes) {
        if (!urls.has(h) && !loading.has(h) && !failed.has(h) && !queue.includes(h)) queue.push(h)
      }
      pump()
    })
  })

  function pump() {
    while (inflight < PARALLEL && queue.length) {
      const hash = queue.shift()
      if (urls.has(hash) || loading.has(hash)) continue
      inflight++
      loading.add(hash)
      api
        .previewPage(projectId, hash)
        .then(({ blob }) => {
          if (destroyed) return
          urls.set(hash, URL.createObjectURL(blob))
          if (stale.length) {
            pages.forEach((p, i) => {
              if (p.hash === hash && stale[i]) stale[i] = undefined
            })
          }
          evict()
        })
        .catch((e) => {
          // La página ya no existe en el servidor (hubo otra compilación):
          // pedir la lista actual. Otros errores se reintentan cuando la
          // página vuelva a acercarse a la vista.
          if (e.status !== 404 || destroyed) return
          failed.add(hash)
          schedule()
        })
        .finally(() => {
          inflight--
          loading.delete(hash)
          if (!destroyed) pump()
        })
    }
  }

  // Libera las páginas descargadas más antiguas que no están cerca de la vista.
  function evict() {
    const keepHash = new Set()
    for (const i of [...near, ...mounted]) if (pages[i]) keepHash.add(pages[i].hash)
    const keepUrl = new Set(stale.filter(Boolean))
    let extra = urls.size - KEEP
    for (const [hash, url] of urls) {
      if (extra <= 0) break
      if (keepHash.has(hash) || keepUrl.has(url)) continue
      URL.revokeObjectURL(url)
      urls.delete(hash)
      extra--
    }
  }

  // ---------- visibilidad ----------

  const nodes = new Set()
  const settling = new Map() // índice -> temporizador hasta montarse
  let observers = []
  function track(set) {
    return (entries) => {
      for (const e of entries) {
        const i = Number(e.target.dataset.i)
        if (e.isIntersecting) set.add(i)
        else set.delete(i)
      }
    }
  }
  function unsettle(i) {
    clearTimeout(settling.get(i))
    settling.delete(i)
  }
  function onClose(entries) {
    for (const e of entries) {
      const i = Number(e.target.dataset.i)
      if (!e.isIntersecting) {
        unsettle(i)
        mounted.delete(i)
      } else if (!mounted.has(i) && !settling.has(i)) {
        settling.set(
          i,
          setTimeout(() => {
            settling.delete(i)
            mounted.add(i)
          }, SETTLE),
        )
      }
    }
  }
  $effect(() => {
    if (!scroller) return
    observers = [
      new IntersectionObserver(track(near), { root: scroller, rootMargin: FETCH_MARGIN }),
      new IntersectionObserver(onClose, { root: scroller, rootMargin: MOUNT_MARGIN }),
      new IntersectionObserver(track(seen), { root: scroller }),
    ]
    for (const n of nodes) for (const o of observers) o.observe(n)
    return () => {
      for (const o of observers) o.disconnect()
      observers = []
    }
  })

  function observe(node, i) {
    node.dataset.i = i
    nodes.add(node)
    for (const o of observers) o.observe(node)
    return {
      destroy() {
        nodes.delete(node)
        for (const o of observers) o.unobserve(node)
        unsettle(i)
        near.delete(i)
        mounted.delete(i)
        seen.delete(i)
      },
    }
  }

  onDestroy(() => {
    destroyed = true
    clearTimeout(timer)
    clearTimeout(syncTimer)
    clearTimeout(markerTimer)
    for (const t of settling.values()) clearTimeout(t)
    for (const url of urls.values()) URL.revokeObjectURL(url)
  })
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="flex h-10 shrink-0 items-center justify-between gap-2 border-b border-slate-200 bg-surface px-3 text-[13px]">
    <div class="flex items-center gap-2 text-slate-600">
      {#if compiling}
        <span class="size-2 animate-pulse rounded-full bg-amber-500"></span> Compilando…
      {:else if syncBusy}
        <span class="size-2 animate-pulse rounded-full bg-sky-500"></span> Sincronizando…
      {:else if result && !result.ok}
        <span class="size-2 rounded-full bg-red-600"></span> Error de compilación
      {:else if result}
        <span class="size-2 rounded-full bg-emerald-600"></span>
        {#if current && pages.length > 1}
          <span class="tabular-nums">Página {current} de {pages.length}</span>
        {:else}
          {pages.length} {pages.length === 1 ? 'página' : 'páginas'}
        {/if}
      {/if}
    </div>
    <div class="flex items-center gap-1">
      <button type="button" class="btn-secondary btn-sm !h-7 !px-2" onclick={() => setZoom(zoom - 10)} aria-label="Alejar">−</button>
      <button type="button" class="w-12 cursor-pointer text-center tabular-nums text-slate-600" onclick={() => setZoom(100)} title="Restablecer (0)">{zoom}%</button>
      <button type="button" class="btn-secondary btn-sm !h-7 !px-2" onclick={() => setZoom(zoom + 10)} aria-label="Acercar">+</button>
    </div>
  </div>

  {#if error}
    <div class="alert-error m-3">{error}</div>
  {/if}

  <div
    bind:this={scroller}
    class="min-h-0 flex-1 overflow-auto bg-slate-200/70 p-4 outline-none select-none {dragging ? 'cursor-grabbing' : 'cursor-grab'}"
    tabindex="0"
    role="region"
    aria-label="Vista previa (Ctrl + rueda o +/− para hacer zoom; arrastra para moverte)"
    onkeydown={onKey}
    onpointerdown={onPointerDown}
    onpointermove={onPointerMove}
    onpointerup={onPointerUp}
    onpointercancel={onPointerUp}
  >
    <div class="mx-auto flex flex-col items-center gap-4" style="width: {zoom}%">
      {#each pages as pg, i (i)}
        {@const src = urls.get(pg.hash) ?? stale[i]}
        <div
          use:observe={i}
          class="relative w-full bg-white shadow-md ring-1 ring-slate-300 [content-visibility:auto]"
          style="aspect-ratio: {pg.width} / {pg.height}"
          onclick={(e) => clickPage(e, i)}
          role="presentation"
          title={onjump ? 'Clic para ir al código · arrastra para moverte' : undefined}
        >
          {#if src && mounted.has(i)}
            <img
              {src}
              alt="Página {i + 1}"
              decoding="async"
              draggable="false"
              class="absolute inset-0 size-full transition-opacity {result && !result.ok ? 'opacity-50' : ''}"
            />
          {:else}
            <span class="absolute inset-0 grid place-items-center text-xs text-slate-300 tabular-nums">{i + 1}</span>
          {/if}
          {#if marker?.i === i}
            <div
              class="pointer-events-none absolute inset-x-0 border-l-4 border-amber-500 bg-amber-200/40"
              style="top: {marker.top}%; height: {marker.height}%"
            ></div>
          {/if}
        </div>
      {/each}
      {#if !pages.length && !compiling && result?.ok}
        <p class="text-sm text-slate-500">El documento no tiene páginas.</p>
      {/if}
    </div>
  </div>
</div>
