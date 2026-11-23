<script>
  // Visor de imágenes a pantalla completa («spotlight») para el explorador:
  // ajusta la imagen a la ventana, zoom con la rueda (sobre el puntero), doble
  // clic o +/−/0, arrastre para desplazarse y ←/→ para recorrer las imágenes
  // del proyecto. Las URL vienen de imageCache (compartidas con miniaturas).
  import { fade } from 'svelte/transition'
  import { d } from '../motion.js'
  import { imageURL } from './imageCache.js'

  // images: archivos {path, size} en el orden del explorador.
  // oninsert(path): si se pasa, muestra «Insertar» (hay un .typ abierto).
  let { projectId, images = [], oninsert = null } = $props()

  let path = $state(null)
  let url = $state('')
  let error = $state('')
  let natural = $state({ w: 0, h: 0 })
  let intrinsic = $state(true)
  let zoom = $state(1) // escala real: 1 = 100 %
  let fitZoom = $state(1)
  let tx = $state(0)
  let ty = $state(0)
  let stage = $state()
  let closeBtn = $state()
  let drag = null
  let moved = false
  let previous = null

  const MIN = 0.05
  const MAX = 16
  const index = $derived(images.findIndex((f) => f.path === path))
  const file = $derived(images[index])
  const name = $derived(path ? path.split('/').pop() : '')
  const dir = $derived(path && path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '')
  const fitted = $derived(Math.abs(zoom - fitZoom) < 1e-3 && tx === 0 && ty === 0)

  const fmtSize = (n) => (n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(0)} KB` : `${(n / 1048576).toFixed(1)} MB`)

  export function open(p) {
    if (!path) previous = document.activeElement
    path = p
  }

  export function close() {
    path = null
    previous?.focus?.()
    previous = null
  }

  function go(step) {
    if (images.length < 2 || index < 0) return
    path = images[(index + step + images.length) % images.length].path
  }

  // Cargar la imagen actual y precargar las vecinas.
  $effect(() => {
    const p = path
    if (!p) return
    const f = images.find((x) => x.path === p)
    let alive = true
    url = ''
    error = ''
    natural = { w: 0, h: 0 }
    imageURL(projectId, p, f?.size ?? 0).then(
      (u) => alive && (url = u),
      (e) => alive && (error = e.message || 'No se pudo cargar la imagen'),
    )
    const i = images.indexOf(f)
    if (i >= 0 && images.length > 1) {
      for (const n of [images[(i + 1) % images.length], images[(i - 1 + images.length) % images.length]]) {
        imageURL(projectId, n.path, n.size ?? 0).catch(() => {})
      }
    }
    return () => (alive = false)
  })

  // Si la imagen abierta desaparece (borrada o renombrada), cerrar.
  $effect(() => {
    if (path && images.length && index < 0) close()
  })

  // Bloquear el scroll del fondo y enfocar el visor mientras está abierto.
  $effect(() => {
    if (!path) return
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    queueMicrotask(() => closeBtn?.focus())
    return () => (document.body.style.overflow = overflow)
  })

  function computeFit() {
    if (!stage) return 1
    const r = stage.getBoundingClientRect()
    const w = natural.w || r.width
    const h = natural.h || r.height
    return Math.min(1, (r.width - 48) / w, (r.height - 48) / h)
  }

  function fit() {
    fitZoom = computeFit()
    zoom = fitZoom
    tx = 0
    ty = 0
  }

  function onload(e) {
    const img = e.currentTarget
    // Un SVG sin width/height declara 0: se toma el tamaño de la ventana.
    const r = stage.getBoundingClientRect()
    intrinsic = img.naturalWidth > 0 && img.naturalHeight > 0
    natural = intrinsic ? { w: img.naturalWidth, h: img.naturalHeight } : { w: r.width - 48, h: r.height - 48 }
    fit()
  }

  // Al cambiar el tamaño de la ventana, reajustar si estaba ajustada.
  $effect(() => {
    if (!stage) return
    const ro = new ResizeObserver(() => {
      const wasFitted = fitted
      fitZoom = computeFit()
      if (wasFitted) fit()
    })
    ro.observe(stage)
    return () => ro.disconnect()
  })

  // Zoom manteniendo fijo el punto (px, py) relativo al centro del escenario.
  function zoomTo(next, px = 0, py = 0) {
    next = Math.min(MAX, Math.max(MIN, next))
    const k = next / zoom
    tx = px - (px - tx) * k
    ty = py - (py - ty) * k
    zoom = next
    if (Math.abs(zoom - fitZoom) < 1e-3) {
      tx = 0
      ty = 0
    }
  }

  function pointer(e) {
    const r = stage.getBoundingClientRect()
    return [e.clientX - r.left - r.width / 2, e.clientY - r.top - r.height / 2]
  }

  // La rueda necesita un listener no pasivo para poder evitar el scroll.
  $effect(() => {
    if (!stage) return
    const onwheel = (e) => {
      if (!url) return
      e.preventDefault()
      const [px, py] = pointer(e)
      zoomTo(zoom * Math.exp(-e.deltaY * (e.deltaMode === 1 ? 0.05 : 0.0018)), px, py)
    }
    stage.addEventListener('wheel', onwheel, { passive: false })
    return () => stage.removeEventListener('wheel', onwheel)
  })

  function ondblclick(e) {
    if (!url) return
    if (fitted && fitZoom < 1) {
      const [px, py] = pointer(e)
      zoomTo(1, px, py)
    } else fit()
  }

  function onpointerdown(e) {
    if (e.button !== 0 || !url) return
    drag = { x: e.clientX, y: e.clientY, tx, ty }
    moved = false
    stage.setPointerCapture(e.pointerId)
  }
  function onpointermove(e) {
    if (!drag) return
    const dx = e.clientX - drag.x
    const dy = e.clientY - drag.y
    if (Math.abs(dx) + Math.abs(dy) > 3) moved = true
    tx = drag.tx + dx
    ty = drag.ty + dy
  }
  function onpointerup() {
    drag = null
  }
  // Clic en el fondo (sin arrastrar y fuera de la imagen): cerrar.
  function onstageclick(e) {
    if (moved) return
    if (e.target === stage) close()
  }

  function onkeydown(e) {
    if (!path) return
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      close()
    } else if (e.key === 'ArrowRight' || e.key === 'PageDown') {
      e.preventDefault()
      go(1)
    } else if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
      e.preventDefault()
      go(-1)
    } else if (e.key === '+' || e.key === '=') {
      e.preventDefault()
      zoomTo(zoom * 1.25)
    } else if (e.key === '-') {
      e.preventDefault()
      zoomTo(zoom / 1.25)
    } else if (e.key === '0') {
      e.preventDefault()
      fit()
    } else if (e.key === '1') {
      e.preventDefault()
      zoomTo(1)
    } else if (e.key === 'Tab') {
      // Mantener el foco dentro del visor.
      const items = [...document.querySelectorAll('[data-lightbox] button:not([disabled]), [data-lightbox] a[href]')]
      if (!items.length) return
      const i = items.indexOf(document.activeElement)
      if (e.shiftKey && i <= 0) {
        e.preventDefault()
        items[items.length - 1].focus()
      } else if (!e.shiftKey && i === items.length - 1) {
        e.preventDefault()
        items[0].focus()
      }
    }
  }

  function openTab() {
    if (url) window.open(url, '_blank', 'noopener')
  }
</script>

<svelte:window {onkeydown} />

{#snippet tool(label, onclick, icon, disabled = false)}
  <button
    type="button"
    class="flex size-8 cursor-pointer items-center justify-center rounded-md text-white/75 hover:bg-white/10 hover:text-white disabled:cursor-default disabled:opacity-35 disabled:hover:bg-transparent"
    {onclick}
    {disabled}
    aria-label={label}
    title={label}
  >
    {@render icon()}
  </button>
{/snippet}

{#if path}
  <div
    data-lightbox
    class="fixed inset-0 z-[60] flex flex-col bg-black/85 text-white backdrop-blur-sm select-none"
    role="dialog"
    aria-modal="true"
    aria-label="Imagen {name}"
    transition:fade={{ duration: d(160) }}
  >
    <!-- Barra superior -->
    <div class="flex shrink-0 items-center gap-3 bg-gradient-to-b from-black/60 to-transparent px-4 py-2.5">
      <div class="min-w-0 flex-1">
        <div class="truncate text-sm font-semibold">{name}</div>
        <div class="truncate text-xs text-white/60">
          {#if dir}{dir}/ · {/if}{#if natural.w && intrinsic}{natural.w} × {natural.h} px · {/if}{#if file?.size}{fmtSize(file.size)}{/if}
          {#if images.length > 1}<span class="ml-1">· {index + 1} de {images.length}</span>{/if}
        </div>
      </div>
      <div class="flex shrink-0 items-center gap-0.5">
        {#snippet iZoomOut()}<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5M8 11h6" /></svg>{/snippet}
        {#snippet iZoomIn()}<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5M8 11h6M11 8v6" /></svg>{/snippet}
        {#snippet iFit()}<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" /></svg>{/snippet}
        {#snippet iInsert()}<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>{/snippet}
        {#snippet iTab()}<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" /></svg>{/snippet}
        {@render tool('Alejar (−)', () => zoomTo(zoom / 1.25), iZoomOut, !url || zoom <= MIN)}
        <button
          type="button"
          class="h-8 min-w-14 cursor-pointer rounded-md px-1.5 text-xs font-medium text-white/80 tabular-nums hover:bg-white/10 hover:text-white disabled:cursor-default disabled:opacity-35"
          onclick={() => zoomTo(1)}
          disabled={!url}
          title="Tamaño real (1)"
        >{Math.round(zoom * 100)} %</button>
        {@render tool('Acercar (+)', () => zoomTo(zoom * 1.25), iZoomIn, !url || zoom >= MAX)}
        {@render tool('Ajustar a la ventana (0)', fit, iFit, !url || fitted)}
        <span class="mx-1 h-5 w-px bg-white/20"></span>
        {#if oninsert}
          {@render tool('Insertar en el documento', () => {
            oninsert(path)
            close()
          }, iInsert)}
        {/if}
        {@render tool('Abrir en una pestaña', openTab, iTab, !url)}
        <a
          href={url || undefined}
          download={name}
          class="flex size-8 items-center justify-center rounded-md text-white/75 hover:bg-white/10 hover:text-white {url ? '' : 'pointer-events-none opacity-35'}"
          aria-label="Descargar"
          title="Descargar"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 4v11M7 10l5 5 5-5M5 20h14" /></svg>
        </a>
        <button
          bind:this={closeBtn}
          type="button"
          class="ml-1 flex size-8 cursor-pointer items-center justify-center rounded-md text-white/75 hover:bg-white/10 hover:text-white"
          onclick={close}
          aria-label="Cerrar (Esc)"
          title="Cerrar (Esc)"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12" /></svg>
        </button>
      </div>
    </div>

    <!-- Escenario -->
    <div
      bind:this={stage}
      class="relative min-h-0 flex-1 overflow-hidden {url ? (drag ? 'cursor-grabbing' : 'cursor-grab') : ''}"
      onpointerdown={onpointerdown}
      onpointermove={onpointermove}
      onpointerup={onpointerup}
      onpointercancel={onpointerup}
      onclick={onstageclick}
      {ondblclick}
      role="presentation"
    >
      {#if error}
        <div class="absolute inset-0 flex items-center justify-center text-sm text-white/70">{error}</div>
      {:else if !url}
        <div class="absolute inset-0 flex items-center justify-center">
          <span class="size-8 animate-spin rounded-full border-2 border-white/20 border-t-white/80"></span>
        </div>
      {/if}
      {#if url}
        {#key url}
          <img
            src={url}
            alt={name}
            draggable="false"
            {onload}
            onerror={() => (error = 'El navegador no puede mostrar esta imagen')}
            class="lightbox-img absolute top-1/2 left-1/2 max-w-none shadow-2xl {natural.w ? '' : 'opacity-0'}"
            style="width: {(natural.w || 1) * zoom}px; height: {(natural.h || 1) * zoom}px; transform: translate(calc(-50% + {tx}px), calc(-50% + {ty}px)); image-rendering: {zoom >= 2 ? 'pixelated' : 'auto'}"
            in:fade={{ duration: d(140) }}
          />
        {/key}
      {/if}

      {#if images.length > 1}
        <button
          type="button"
          class="absolute top-1/2 left-3 flex size-11 -translate-y-1/2 cursor-pointer items-center justify-center rounded-full bg-black/45 text-white/85 hover:bg-black/70 hover:text-white"
          onclick={(e) => (e.stopPropagation(), go(-1))}
          onpointerdown={(e) => e.stopPropagation()}
          ondblclick={(e) => e.stopPropagation()}
          aria-label="Imagen anterior (←)"
          title="Anterior (←)"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 6-6 6 6 6" /></svg>
        </button>
        <button
          type="button"
          class="absolute top-1/2 right-3 flex size-11 -translate-y-1/2 cursor-pointer items-center justify-center rounded-full bg-black/45 text-white/85 hover:bg-black/70 hover:text-white"
          onclick={(e) => (e.stopPropagation(), go(1))}
          onpointerdown={(e) => e.stopPropagation()}
          ondblclick={(e) => e.stopPropagation()}
          aria-label="Imagen siguiente (→)"
          title="Siguiente (→)"
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 6 6 6-6 6" /></svg>
        </button>
      {/if}
    </div>

    <div class="shrink-0 pb-2 text-center text-[11px] text-white/45">
      Rueda o doble clic para hacer zoom · arrastra para mover{#if images.length > 1} · ← → para cambiar de imagen{/if} · Esc para cerrar
    </div>
  </div>
{/if}

<style>
  /* Cuadrícula de fondo para que se vean las transparencias (PNG/SVG). */
  .lightbox-img {
    background-color: #fff;
    background-image:
      linear-gradient(45deg, #e5e7eb 25%, transparent 25%),
      linear-gradient(-45deg, #e5e7eb 25%, transparent 25%),
      linear-gradient(45deg, transparent 75%, #e5e7eb 75%),
      linear-gradient(-45deg, transparent 75%, #e5e7eb 75%);
    background-size: 16px 16px;
    background-position: 0 0, 0 8px, 8px -8px, -8px 0;
  }
</style>
