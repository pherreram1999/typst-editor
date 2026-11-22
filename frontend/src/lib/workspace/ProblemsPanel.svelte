<script>
  // Ventana de herramientas «Problemas» (al estilo de los IDE de JetBrains):
  // barra siempre visible con el número de errores y avisos de la última
  // compilación, y un cuerpo plegable y redimensionable con los diagnósticos
  // agrupados por archivo. Alt+6 la muestra u oculta.
  import { onDestroy } from 'svelte'

  let { result, ondiagnostic } = $props()

  const KEY = 'typsteditor.problems'
  function load() {
    try {
      return JSON.parse(localStorage.getItem(KEY)) ?? {}
    } catch {
      return {}
    }
  }
  const saved = load()
  let open = $state(saved.open ?? true)
  let height = $state(saved.height ?? 180) // px
  let showErrors = $state(true)
  let showWarnings = $state(true)
  let collapsed = $state({}) // archivo -> plegado

  $effect(() => {
    const v = JSON.stringify({ open, height })
    try {
      localStorage.setItem(KEY, v)
    } catch {}
  })

  const diagnostics = $derived(result?.diagnostics ?? [])
  const errorCount = $derived(diagnostics.filter((d) => d.severity === 'error').length)
  const warningCount = $derived(diagnostics.filter((d) => d.severity === 'warning').length)
  // Fallo sin diagnósticos reconocibles: se muestra la salida en bruto.
  const rawOutput = $derived(result && !result.ok && !errorCount ? result.output || 'Error de compilación' : '')

  const groups = $derived.by(() => {
    const m = new Map()
    for (const dg of diagnostics) {
      if (dg.severity === 'error' ? !showErrors : !showWarnings) continue
      if (!m.has(dg.file)) m.set(dg.file, [])
      m.get(dg.file).push(dg)
    }
    // Errores primero dentro de cada archivo, luego por posición.
    const rank = (d) => (d.severity === 'error' ? 0 : 1)
    return [...m].map(([file, list]) => ({
      file,
      list: list.sort((a, b) => rank(a) - rank(b) || a.line - b.line || a.column - b.column),
    }))
  })

  // ---------- redimensionado ----------
  let panel = $state()
  let stopResize
  function startResize(ev) {
    ev.preventDefault()
    const startY = ev.clientY
    const startH = height
    const max = Math.max(120, (panel?.parentElement?.clientHeight ?? 600) - 120)
    const move = (e) => (height = Math.min(max, Math.max(80, startH + (startY - e.clientY))))
    const up = () => stopResize?.()
    stopResize = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      stopResize = null
    }
    document.body.style.cursor = 'row-resize'
    document.body.style.userSelect = 'none'
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }
  onDestroy(() => stopResize?.())

  function onkeydown(e) {
    if (e.altKey && !e.ctrlKey && !e.metaKey && e.code === 'Digit6') {
      e.preventDefault()
      open = !open
    }
  }
</script>

<svelte:window {onkeydown} />

<div bind:this={panel} class="flex shrink-0 flex-col border-t border-slate-300 bg-surface text-[13px]">
  {#if open}
    <div
      class="h-1 shrink-0 cursor-row-resize transition-colors hover:bg-primary/40"
      onpointerdown={startResize}
      role="separator"
      aria-orientation="horizontal"
      aria-label="Redimensionar panel de problemas"
    ></div>
  {/if}

  <!-- Barra de título -->
  <div class="flex h-8 shrink-0 items-center gap-1 px-2 {open ? 'border-b border-slate-200 bg-slate-50' : 'bg-slate-50'}">
    <button
      type="button"
      class="flex h-full cursor-pointer items-center gap-2 pr-2 font-medium text-slate-700 hover:text-slate-950"
      onclick={() => (open = !open)}
      aria-expanded={open}
      title="{open ? 'Ocultar' : 'Mostrar'} problemas (Alt+6)"
    >
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="transition-transform {open ? '' : '-rotate-90'}" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
      Problemas
    </button>

    <button
      type="button"
      class="flex h-6 cursor-pointer items-center gap-1 rounded px-1.5 tabular-nums hover:bg-slate-200/70 {showErrors ? '' : 'opacity-40'}"
      onclick={() => {
        showErrors = !showErrors
        open = true
      }}
      title="{errorCount} {errorCount === 1 ? 'error' : 'errores'} · clic para filtrar"
      aria-pressed={showErrors}
    >
      <svg width="13" height="13" viewBox="0 0 24 24" class="text-red-600" aria-hidden="true"><circle cx="12" cy="12" r="10" fill="currentColor" /><path d="M15 9l-6 6M9 9l6 6" stroke="#fff" stroke-width="2.5" stroke-linecap="round" /></svg>
      <span class={errorCount ? 'font-semibold text-red-700' : 'text-slate-500'}>{errorCount}</span>
    </button>
    <button
      type="button"
      class="flex h-6 cursor-pointer items-center gap-1 rounded px-1.5 tabular-nums hover:bg-slate-200/70 {showWarnings ? '' : 'opacity-40'}"
      onclick={() => {
        showWarnings = !showWarnings
        open = true
      }}
      title="{warningCount} {warningCount === 1 ? 'aviso' : 'avisos'} · clic para filtrar"
      aria-pressed={showWarnings}
    >
      <svg width="13" height="13" viewBox="0 0 24 24" class="text-amber-500" aria-hidden="true"><path d="M12 2 1 21h22L12 2Z" fill="currentColor" /><path d="M12 9v5M12 17.5v.5" stroke="#fff" stroke-width="2.5" stroke-linecap="round" /></svg>
      <span class={warningCount ? 'font-semibold text-amber-700' : 'text-slate-500'}>{warningCount}</span>
    </button>

    <span class="flex-1"></span>
    {#if open}
      <button
        type="button"
        class="flex size-6 cursor-pointer items-center justify-center rounded text-slate-500 hover:bg-slate-200/70 hover:text-slate-900"
        onclick={() => (open = false)}
        title="Ocultar (Alt+6)"
        aria-label="Ocultar panel de problemas"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M5 12h14" /></svg>
      </button>
    {/if}
  </div>

  <!-- Contenido -->
  {#if open}
    <div class="min-h-0 overflow-y-auto" style="height: {height}px">
      {#if !result}
        <p class="px-3 py-2 text-slate-500">Compilando…</p>
      {:else if rawOutput}
        <pre class="whitespace-pre-wrap px-3 py-2 font-mono text-xs text-red-800">{rawOutput}</pre>
      {:else if !groups.length}
        <p class="flex items-center gap-2 px-3 py-2 text-slate-500">
          {#if diagnostics.length}
            Ningún problema coincide con el filtro.
          {:else}
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="text-emerald-600" aria-hidden="true"><path d="M20 6 9 17l-5-5" /></svg>
            Sin problemas.
          {/if}
        </p>
      {:else}
        <ul class="py-1">
          {#each groups as g (g.file)}
            <li>
              <button
                type="button"
                class="flex w-full cursor-pointer items-center gap-1.5 px-2 py-0.5 text-left hover:bg-slate-100"
                onclick={() => (collapsed[g.file] = !collapsed[g.file])}
                aria-expanded={!collapsed[g.file]}
              >
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" class="text-slate-400 transition-transform {collapsed[g.file] ? '-rotate-90' : ''}" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
                <span class="font-mono font-medium text-slate-800">{g.file}</span>
                <span class="text-xs text-slate-500">{g.list.length} {g.list.length === 1 ? 'problema' : 'problemas'}</span>
              </button>
              {#if !collapsed[g.file]}
                <ul>
                  {#each g.list as dg, i (i)}
                    <li>
                      <button
                        type="button"
                        class="flex w-full cursor-pointer items-start gap-2 py-0.5 pr-3 pl-7 text-left hover:bg-primary-tint"
                        onclick={() => ondiagnostic?.(dg)}
                        title="Ir a {dg.file}:{dg.line}:{dg.column}"
                      >
                        {#if dg.severity === 'error'}
                          <svg width="13" height="13" viewBox="0 0 24 24" class="mt-0.5 shrink-0 text-red-600" aria-label="error"><circle cx="12" cy="12" r="10" fill="currentColor" /><path d="M15 9l-6 6M9 9l6 6" stroke="#fff" stroke-width="2.5" stroke-linecap="round" /></svg>
                        {:else}
                          <svg width="13" height="13" viewBox="0 0 24 24" class="mt-0.5 shrink-0 text-amber-500" aria-label="aviso"><path d="M12 2 1 21h22L12 2Z" fill="currentColor" /><path d="M12 9v5M12 17.5v.5" stroke="#fff" stroke-width="2.5" stroke-linecap="round" /></svg>
                        {/if}
                        <span class="min-w-0 flex-1 text-slate-800">{dg.message}</span>
                        <span class="shrink-0 font-mono text-xs text-slate-500">:{dg.line}:{dg.column}</span>
                      </button>
                    </li>
                  {/each}
                </ul>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}
</div>
