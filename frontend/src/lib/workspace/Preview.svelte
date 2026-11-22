<script>
  // Vista previa en vivo. Normalmente compila en el navegador
  // (lib/preview/typst.worker.js: typst en WebAssembly, incremental): cada
  // cambio del editor (livePath/liveVersion/liveText) se compila al momento
  // y los cambios de otros archivos (remoteKey) se descargan antes. Si el
  // navegador no puede (sin Worker/WASM, otra versión de typst en el
  // servidor, falta de memoria…) compila el backend: una SVG por página (con
  // las imágenes reducidas) y /preview solo devuelve, por página, su hash y su
  // tamaño. Mientras el compilador del navegador se prepara (la primera vez
  // descarga unos 15 MB) se muestra la compilación del servidor. Aquí se reserva el hueco de cada página y solo se descargan y
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
  import { BrowserCompiler } from '../preview/engine.js'

  let { projectId, refreshKey, remoteKey = 0, livePath = '', liveVersion = 0, liveText = () => null, active = true, onresult, onjump } = $props()

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
  let lastMs = $state(0)
  let timer
  let destroyed = false

  // Compilador del navegador ('local') o del servidor ('server').
  let mode = $state(typeof Worker !== 'undefined' && typeof WebAssembly !== 'undefined' ? 'local' : 'server')
  let engine = null
  let engineReady = null
  let preparing = $state(false) // el compilador del navegador aún no ha compilado
  let localDoc = 0 // compilación (del worker) de las páginas mostradas
  let mainFile = ''
  let filesDirty = true // hay que pedir la lista de archivos al servidor
  let sentPath = '' // archivo cuyo texto en vivo tiene el worker
  let sentVersion = -1
  let localTimer
  let localBusy = false
  let localPending = false
  const sigs = [] // índice -> { sig, hash } del último SVG de cada página (modo local)

  let sync = null // { gen, points: [{page, y, file, line, col}] }
  let syncGen = 0 // aumenta con cada compilación: el mapa queda anticuado
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

  let serverRun = null // compilación del servidor en curso (promesa)

  function compile() {
    if (compiling) return // al terminar se comprueba si hay cambios nuevos
    serverRun = compileServer().finally(() => (serverRun = null))
  }

  async function compileServer() {
    compiling = true
    clearTimeout(syncTimer)
    error = ''
    const key = refreshKey
    const t0 = performance.now()
    try {
      const r = await api.preview(projectId)
      if (destroyed) return
      // El compilador del navegador ya va por delante.
      if (mode === 'local' && localDoc) return
      if (r.rev !== lastRev || !r.ok) {
        lastRev = r.rev
        result = r
        onresult?.(r)
        if (r.ok) {
          setPages(r.pages)
          syncGen++
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
      if (!destroyed && active && mode === 'server' && refreshKey !== key) schedule()
    }
  }

  $effect(() => {
    const key = refreshKey
    if (!active || key === doneKey) return
    // En modo local solo se compila en el servidor hasta tener la primera
    // compilación del navegador.
    if (mode === 'local' && (localDoc || doneKey !== -1)) return
    untrack(schedule)
  })

  // ---------- compilación en el navegador ----------

  function startEngine() {
    engine = new BrowserCompiler(projectId)
    preparing = true
    engineReady = engine.init()
    engineReady.catch(fallback)
  }

  // El navegador no puede compilar: se sigue con el servidor.
  function fallback(err) {
    if (mode !== 'local' || destroyed) return
    console.warn('Vista previa: se compila en el servidor.', err)
    engine?.destroy()
    engine = null
    mode = 'server'
    preparing = false
    localDoc = 0
    doneKey = -1
    if (active) schedule()
  }

  function scheduleLocal(delay) {
    clearTimeout(localTimer)
    localTimer = setTimeout(runLocal, delay)
  }

  async function runLocal() {
    if (mode !== 'local' || destroyed || !active) return
    if (localBusy) {
      localPending = true
      return
    }
    localBusy = true
    localPending = false
    const t0 = performance.now()
    try {
      await engineReady
      // El archivo que se deja de editar vuelve a la versión del servidor.
      if (sentPath && sentPath !== livePath) {
        engine.release(sentPath)
        sentPath = ''
        filesDirty = true
      }
      if (filesDirty) {
        filesDirty = false
        try {
          mainFile = (await engine.syncFiles()).main
        } catch (e) {
          filesDirty = true
          throw e
        }
      }
      const text = livePath ? liveText() : null
      if (text !== null && (livePath !== sentPath || liveVersion !== sentVersion)) {
        engine.source(livePath, text)
        sentPath = livePath
        sentVersion = liveVersion
      }
      // Las dos primeras compilaciones (servidor y navegador) no coinciden:
      // cada una puede ocupar más de 1 GB con un documento grande.
      if (serverRun) await serverRun
      compiling = true
      const r = await engine.compile(mainFile)
      if (destroyed) return
      error = ''
      preparing = false
      const res = { ok: r.ok, diagnostics: r.diagnostics, output: r.output, browser: true }
      result = res
      onresult?.(res)
      if (r.ok) {
        localDoc = r.doc
        setPages(r.pages.map((p, i) => ({ hash: `${r.doc}:${i}`, width: p.width, height: p.height })))
        syncGen++
        if (syncUsed) syncTimer = setTimeout(() => ensureSync().catch(() => {}), 2500)
      }
    } catch (e) {
      if (engine?.broken || preparing) fallback(e)
      else error = e.message
    } finally {
      lastMs = performance.now() - t0
      localBusy = false
      compiling = false
      if (localPending && !destroyed) scheduleLocal(0)
    }
  }

  // Cambios en el editor: se compilan enseguida (el worker ya es
  // incremental). Los de otros archivos se agrupan un momento.
  $effect(() => {
    livePath
    liveVersion
    if (mode !== 'local' || !active) return
    untrack(() => {
      if (!engine) startEngine()
      scheduleLocal(localDoc ? 15 : 0)
    })
  })
  $effect(() => {
    remoteKey
    untrack(() => {
      filesDirty = true
      if (mode === 'local' && active && engine) scheduleLocal(250)
    })
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
    if (sync && sync.gen === syncGen) return sync
    if (!syncLoading) {
      syncBusy = true
      const gen = syncGen
      syncLoading = api
        .previewSync(projectId)
        .then((r) => (sync = { gen, points: r.points }))
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
    if (mode === 'local' && localDoc) return pumpLocal()
    while (inflight < PARALLEL && queue.length) {
      const hash = queue.shift()
      if (urls.has(hash) || loading.has(hash)) continue
      inflight++
      loading.add(hash)
      api
        .previewPage(projectId, hash)
        .then(({ blob }) => {
          if (destroyed) return
          gotPage(hash, URL.createObjectURL(blob))
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

  function gotPage(hash, url) {
    urls.set(hash, url)
    if (stale.length) {
      pages.forEach((p, i) => {
        if (p.hash === hash && stale[i]) stale[i] = undefined
      })
    }
    evict()
  }

  // Firma rápida del SVG de una página: si no cambió respecto a la
  // compilación anterior se reutiliza su blob URL (no se vuelve a decodificar).
  function signature(str) {
    let h = 0x811c9dc5
    for (let i = 0; i < str.length; i += 7) h = Math.imul(h ^ str.charCodeAt(i), 0x01000193)
    return `${str.length}:${h >>> 0}`
  }

  // Modo local: el worker dibuja las páginas pedidas de una vez (en lotes).
  function pumpLocal() {
    if (inflight || !queue.length) return
    const doc = localDoc
    const batch = []
    while (queue.length && batch.length < 8) {
      const hash = queue.shift()
      const [d, i] = hash.split(':').map(Number)
      if (d !== doc || urls.has(hash)) continue
      batch.push(i)
      loading.add(hash)
    }
    if (!batch.length) return
    inflight++
    engine
      .render(doc, batch)
      .then((r) => {
        if (destroyed || r.stale) return
        for (const { index, svg } of r.pages) {
          const hash = `${doc}:${index}`
          const sig = signature(svg)
          const prev = sigs[index]
          let url
          if (prev && prev.sig === sig && urls.has(prev.hash) && pages[index]?.hash === hash) {
            url = urls.get(prev.hash)
            urls.delete(prev.hash)
          } else url = URL.createObjectURL(new Blob([svg], { type: 'image/svg+xml' }))
          sigs[index] = { sig, hash }
          gotPage(hash, url)
        }
      })
      .catch((e) => {
        if (engine?.broken) fallback(e)
      })
      .finally(() => {
        inflight--
        for (const i of batch) loading.delete(`${doc}:${i}`)
        if (!destroyed) pump()
      })
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
    engine?.destroy()
    clearTimeout(localTimer)
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
      {#if preparing && !pages.length}
        <span class="size-2 animate-pulse rounded-full bg-amber-500"></span> Preparando el compilador…
      {:else if compiling && (mode === 'server' || !result || lastMs > 300)}
        <span class="size-2 animate-pulse rounded-full bg-amber-500"></span> Compilando…
      {:else if syncBusy}
        <span class="size-2 animate-pulse rounded-full bg-sky-500"></span> Sincronizando…
      {:else if result && !result.ok}
        <span class="size-2 rounded-full bg-red-600"></span> Error de compilación
      {:else if result}
        <span
          class="size-2 rounded-full bg-emerald-600"
          title={result.browser ? 'Compilado en el navegador' : 'Compilado en el servidor'}
        ></span>
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

  <!-- Visor con zoom (+/−/0) y arrastre: necesita el foco y los eventos de
       teclado y puntero; «region» describe mejor su contenido que un rol de
       widget, así que se silencian los avisos a propósito. -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
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
