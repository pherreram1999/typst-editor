<script>
  import { fade } from 'svelte/transition'
  import { d, modalIn, modalOut } from './motion.js'

  let { open = false, title, onclose, children, wide = false } = $props()

  const titleId = `modal-${Math.random().toString(36).slice(2, 8)}`
  let panel = $state()

  function onkeydown(e) {
    if (!open) return
    if (e.key === 'Escape') {
      e.preventDefault()
      onclose?.()
    } else if (e.key === 'Tab' && panel) {
      // Mantener el foco dentro del modal.
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

  // Al abrir: bloquear scroll del fondo, enfocar el primer campo y
  // devolver el foco al elemento anterior al cerrar.
  $effect(() => {
    if (!open) return
    const previous = document.activeElement
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    queueMicrotask(() => panel?.querySelector('input:not([disabled]), select, button')?.focus())
    return () => {
      document.body.style.overflow = overflow
      previous?.focus?.()
    }
  })
</script>

<svelte:window {onkeydown} />

{#if open}
  <div class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:items-center">
    <!-- Fondo: clic para cerrar -->
    <div
      class="fixed inset-0 bg-slate-900/50 backdrop-blur-[2px]"
      transition:fade={{ duration: d(180) }}
      onclick={() => onclose?.()}
      aria-hidden="true"
    ></div>

    <div
      bind:this={panel}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      class="card relative my-8 w-full {wide ? 'max-w-2xl' : 'max-w-md'} shadow-xl"
      in:modalIn
      out:modalOut
    >
      <div class="flex items-center justify-between border-b border-slate-200 px-6 py-4">
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
      <div class="p-6">
        {@render children?.()}
      </div>
    </div>
  </div>
{/if}
