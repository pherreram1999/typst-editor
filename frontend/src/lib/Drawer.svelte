<script>
  // Panel lateral a la derecha, de alto completo: cabecera y pie fijos y el
  // contenido con su propio scroll (para formularios que pueden crecer).
  // Mismo comportamiento que Modal: Esc o clic en el fondo cierran, el foco
  // queda dentro y vuelve al elemento anterior al cerrar.
  // `footer` (snippet opcional) queda siempre visible abajo; un botón de
  // envío ahí puede apuntar a un formulario del cuerpo con form="<id>".
  import { fade } from 'svelte/transition'
  import { d, drawerIn, drawerOut } from './motion.js'

  let { open = false, title, onclose, children, footer, width = 'max-w-xl' } = $props()

  const titleId = `drawer-${Math.random().toString(36).slice(2, 8)}`
  let panel = $state()

  function onkeydown(e) {
    if (!open) return
    if (e.key === 'Escape') {
      e.preventDefault()
      onclose?.()
    } else if (e.key === 'Tab' && panel) {
      const items = panel.querySelectorAll(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href]',
      )
      if (!items.length) return
      const first = items[0]
      const last = items[items.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  $effect(() => {
    if (!open) return
    const previous = document.activeElement
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    queueMicrotask(() => panel?.querySelector('.drawer-body input:not([disabled]), .drawer-body select, button')?.focus())
    return () => {
      document.body.style.overflow = overflow
      previous?.focus?.()
    }
  })
</script>

<svelte:window {onkeydown} />

{#if open}
  <div class="fixed inset-0 z-50 flex justify-end">
    <div
      class="fixed inset-0 bg-slate-900/40 backdrop-blur-[2px]"
      transition:fade={{ duration: d(180) }}
      onclick={() => onclose?.()}
      aria-hidden="true"
    ></div>

    <div
      bind:this={panel}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      class="relative flex h-full w-full {width} flex-col border-l border-slate-200 bg-surface shadow-2xl"
      in:drawerIn
      out:drawerOut
    >
      <div class="flex shrink-0 items-center justify-between border-b border-slate-200 px-6 py-4">
        <h2 id={titleId} class="text-[17px] font-semibold">{title}</h2>
        <button
          type="button"
          onclick={() => onclose?.()}
          class="-mr-2 flex size-9 cursor-pointer items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100 hover:text-slate-900"
          aria-label="Cerrar"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
            stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12" /></svg>
        </button>
      </div>
      <div class="drawer-body min-h-0 flex-1 overflow-y-auto overscroll-contain px-6 py-5">
        {@render children?.()}
      </div>
      {#if footer}
        <div class="shrink-0 border-t border-slate-200 bg-slate-50 px-6 py-3">
          {@render footer()}
        </div>
      {/if}
    </div>
  </div>
{/if}
