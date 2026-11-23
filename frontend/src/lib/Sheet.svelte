<script>
  // Hoja inferior para pantallas táctiles (menús y paneles en el celular):
  // sube desde abajo sobre un fondo oscurecido. Se cierra con el fondo, con
  // Esc o arrastrando el asa hacia abajo. `tall` le da casi toda la altura
  // (listas largas: el cuerpo hace scroll); sin él se ajusta al contenido.
  import { fade } from 'svelte/transition'
  import { d, sheetIn, sheetOut } from './motion.js'

  let { open = false, title = '', onclose, children, header, tall = false } = $props()

  const titleId = `sheet-${Math.random().toString(36).slice(2, 8)}`
  let panel = $state()
  let dragY = $state(0) // desplazamiento mientras se arrastra el asa
  let drag = $state(null)

  function onkeydown(e) {
    if (open && e.key === 'Escape') {
      e.preventDefault()
      onclose?.()
    }
  }

  $effect(() => {
    if (!open) return
    const previous = document.activeElement
    dragY = 0
    queueMicrotask(() => panel?.focus({ preventScroll: true }))
    return () => previous?.focus?.({ preventScroll: true })
  })

  // Arrastrar el asa (o la cabecera) hacia abajo: más de un tercio cierra.
  function down(e) {
    if (e.target.closest('button, input, a, select')) return
    drag = { y: e.clientY, id: e.pointerId }
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  function move(e) {
    if (drag?.id === e.pointerId) dragY = Math.max(0, e.clientY - drag.y)
  }
  function up(e) {
    if (drag?.id !== e.pointerId) return
    drag = null
    if (panel && dragY > Math.min(160, panel.offsetHeight / 3)) onclose?.()
    else dragY = 0
  }
</script>

<svelte:window {onkeydown} />

{#if open}
  <div class="fixed inset-0 z-50 flex flex-col justify-end">
    <div class="fixed inset-0 bg-slate-900/45" transition:fade={{ duration: d(160) }} onclick={() => onclose?.()} aria-hidden="true"></div>
    <div
      bind:this={panel}
      role="dialog"
      aria-modal="true"
      aria-labelledby={title ? titleId : undefined}
      tabindex="-1"
      class="relative flex w-full flex-col rounded-t-2xl bg-surface shadow-2xl outline-none {tall ? 'h-[85dvh]' : 'max-h-[85dvh]'}"
      style="transform: translateY({dragY}px); transition: {drag ? 'none' : 'transform 160ms ease-out'}; padding-bottom: env(safe-area-inset-bottom)"
      in:sheetIn
      out:sheetOut
    >
      <div class="shrink-0 touch-none" onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={up} role="presentation">
        <div class="mx-auto mt-2 mb-1 h-1 w-9 rounded-full bg-slate-300" aria-hidden="true"></div>
        {#if header}
          {@render header()}
        {:else if title}
          <h2 id={titleId} class="px-5 pt-1 pb-2 text-base font-semibold">{title}</h2>
        {/if}
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        {@render children?.()}
      </div>
    </div>
  </div>
{/if}
