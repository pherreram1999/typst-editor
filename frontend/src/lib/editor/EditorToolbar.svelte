<script>
  // Barra de herramientas compacta del editor: solo iconos, con tooltip
  // (nombre · atajo · sintaxis Typst). Reutilizable: no conoce CodeMirror;
  // avisa con onaction(id, arg) y recibe el estado en `active`.
  //
  //   <EditorToolbar file="cap1.typ" active={…} labels={() => […]} onaction={(id, arg) => …} />
  //
  // Herramientas: undo redo bold italic underline code heading(0–3) bullets
  // numbered image figure table math link cite ref(label?) search. Con poco ancho,
  // las de menor prioridad pasan al menú «Más». Teclado: ←/→ recorren los
  // botones (una sola parada de Tab), Esc cierra tooltips y menús.
  import { onDestroy } from 'svelte'

  let { file = '', active = {}, labels = () => [], disabled = false, onaction } = $props()

  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
  const k = (s) => (isMac ? s.replace(/Ctrl\+/g, '⌘').replace(/Alt\+/g, '⌥').replace(/Mayús\+/g, '⇧') : s)

  // Iconos (trazos de 24×24).
  const ICONS = {
    undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h11a5 5 0 0 1 0 10h-3"/>',
    redo: '<path d="m15 14 5-5-5-5"/><path d="M20 9H9a5 5 0 0 0 0 10h3"/>',
    bold: '<path d="M7 5h6a3.5 3.5 0 0 1 0 7H7z" stroke-width="2.4"/><path d="M7 12h7a3.5 3.5 0 0 1 0 7H7z" stroke-width="2.4"/>',
    italic: '<path d="M19 4h-9M14 20H5M15 4 9 20"/>',
    underline: '<path d="M6 4v6a6 6 0 0 0 12 0V4"/><path d="M4 20h16"/>',
    code: '<path d="m16 18 6-6-6-6"/><path d="m8 6-6 6 6 6"/>',
    heading: '<path d="M6 4v16M18 4v16M6 12h12"/>',
    bullets: '<path d="M9 6h11M9 12h11M9 18h11"/><circle cx="4" cy="6" r="1"/><circle cx="4" cy="12" r="1"/><circle cx="4" cy="18" r="1"/>',
    numbered: '<path d="M10 6h11M10 12h11M10 18h11"/><path d="M3 4h2v4"/><path d="M3 8h4"/><path d="M3 14.5a1.5 1.5 0 0 1 3 0c0 1-3 2-3 3.5h3"/>',
    image: '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-5-5L5 21"/>',
    figure: '<rect x="3" y="3" width="18" height="12" rx="2"/><path d="m3 13 5-5 4 4 3-3 6 6"/><path d="M7 20h10"/>',
    table: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 10h18M3 15h18M9 4v16"/>',
    math: '<path d="M18 4H6l6 8-6 8h12"/>',
    link: '<path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7"/><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7"/>',
    ref: '<circle cx="12" cy="12" r="4"/><path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-4 8"/>',
    cite: '<path d="M10 7H6a2 2 0 0 0-2 2v3a2 2 0 0 0 2 2h2v1a3 3 0 0 1-3 3"/><path d="M20 7h-4a2 2 0 0 0-2 2v3a2 2 0 0 0 2 2h2v1a3 3 0 0 1-3 3"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
    chevron: '<path d="m6 9 6 6 6-6"/>',
  }

  // Grupos en orden; `fold`: prioridad de plegado (1 se pliega primero; sin fold, siempre visible).
  const GROUPS = [
    [
      { id: 'undo', label: 'Deshacer', keys: 'Ctrl+Z', fold: 1 },
      { id: 'redo', label: 'Rehacer', keys: 'Ctrl+Mayús+Z', fold: 1 },
    ],
    [
      { id: 'bold', label: 'Negrita', keys: 'Ctrl+B', syntax: '*texto*' },
      { id: 'italic', label: 'Cursiva', keys: 'Ctrl+I', syntax: '_texto_' },
      { id: 'underline', label: 'Subrayado', keys: 'Ctrl+U', syntax: '#underline[…]', fold: 1 },
      { id: 'code', label: 'Código en línea', keys: 'Ctrl+E', syntax: '`código`', fold: 2 },
      { id: 'heading', label: 'Encabezado', keys: 'Ctrl+Alt+1…3', syntax: '= Título', menu: true, fold: 2 },
    ],
    [
      { id: 'bullets', label: 'Lista con viñetas', keys: 'Ctrl+Mayús+8', syntax: '- elemento', fold: 2 },
      { id: 'numbered', label: 'Lista numerada', keys: 'Ctrl+Mayús+7', syntax: '+ elemento', fold: 2 },
    ],
    [
      { id: 'image', label: 'Insertar imagen', keys: 'Ctrl+Alt+I', syntax: '#image("…")' },
      { id: 'figure', label: 'Figura con pie', syntax: '#figure(…, caption: […])', fold: 2 },
      { id: 'table', label: 'Tabla', syntax: '#table(columns: 3, …)', fold: 1 },
      { id: 'math', label: 'Ecuación', keys: 'Ctrl+M', syntax: '$ x $', fold: 1 },
      { id: 'link', label: 'Enlace', keys: 'Ctrl+K', syntax: '#link("…")[…]', fold: 1 },
      { id: 'cite', label: 'Insertar cita de la bibliografía', keys: 'Ctrl+Alt+C', syntax: '@clave', fold: 2 },
      { id: 'ref', label: 'Referencia a una etiqueta', syntax: '@etiqueta', menu: true, fold: 1 },
    ],
  ]
  const SEARCH = { id: 'search', label: 'Buscar y reemplazar', keys: 'Ctrl+F' }

  // ---------- plegado según el ancho ----------
  let bar = $state()
  let width = $state(9999)
  $effect(() => {
    if (!bar) return
    const ro = new ResizeObserver(([e]) => (width = e.contentRect.width))
    ro.observe(bar)
    return () => ro.disconnect()
  })
  const foldLevel = $derived(width < 470 ? 2 : width < 640 ? 1 : 0)
  const visibleGroups = $derived(GROUPS.map((g) => g.filter((t) => !t.fold || t.fold > foldLevel)).filter((g) => g.length))
  const folded = $derived(GROUPS.map((g) => g.filter((t) => t.fold && t.fold <= foldLevel)).filter((g) => g.length))

  const isActive = (t) => (t.id === 'heading' ? active.heading > 0 : !!active[t.id])
  const isDisabled = (t) => disabled || (t.id === 'undo' && !active.canUndo) || (t.id === 'redo' && !active.canRedo)

  // ---------- tooltips ----------
  let tip = $state(null) // id
  let tipTimer
  let lastTip = 0
  function showTip(id, now = false) {
    clearTimeout(tipTimer)
    // Entre botones vecinos, al instante; si no, a los 400 ms.
    if (now || Date.now() - lastTip < 300) tip = id
    else tipTimer = setTimeout(() => (tip = id), 400)
  }
  function hideTip() {
    clearTimeout(tipTimer)
    if (tip) lastTip = Date.now()
    tip = null
  }

  // ---------- menús ----------
  let menu = $state(null) // 'heading' | 'ref' | 'more'
  let refLabels = $state([])
  function openMenu(id) {
    hideTip()
    if (menu === id) return (menu = null)
    if (id === 'ref') refLabels = labels()
    menu = id
  }
  function act(id, arg) {
    menu = null
    hideTip()
    onaction?.(id, arg)
  }
  function click(t) {
    if (t.menu) openMenu(t.id)
    else act(t.id)
  }
  function onWindowPointer(e) {
    if (menu && bar && !bar.contains(e.target)) menu = null
  }
  function onWindowKey(e) {
    if (e.key === 'Escape' && (menu || tip)) {
      menu = null
      hideTip()
    }
  }

  // ---------- teclado: una parada de Tab, ←/→ entre botones ----------
  let focusIdx = $state(0)
  function onBarKey(e) {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight' && e.key !== 'Home' && e.key !== 'End') return
    const btns = [...bar.querySelectorAll('button[data-tool]')]
    const i = btns.indexOf(document.activeElement)
    if (i < 0) return
    e.preventDefault()
    const n =
      e.key === 'Home' ? 0 : e.key === 'End' ? btns.length - 1 : (i + (e.key === 'ArrowRight' ? 1 : -1) + btns.length) % btns.length
    focusIdx = n
    btns[n].focus()
  }
  const tabIndexOf = (i) => (i === focusIdx ? 0 : -1)
  // Índice global de cada botón visible (para el tabindex itinerante).
  const order = $derived([...visibleGroups.flat().map((t) => t.id), 'search', 'more'])

  onDestroy(() => clearTimeout(tipTimer))

  const HEADINGS = [
    [0, 'Texto normal', 'Ctrl+Alt+0', ''],
    [1, 'Título 1', 'Ctrl+Alt+1', '='],
    [2, 'Título 2', 'Ctrl+Alt+2', '=='],
    [3, 'Título 3', 'Ctrl+Alt+3', '==='],
  ]
</script>

<svelte:window onpointerdown={onWindowPointer} onkeydown={onWindowKey} />

{#snippet icon(id, size = 16)}
  <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">{@html ICONS[id]}</svg>
{/snippet}

{#snippet tooltip(t, alignRight = false)}
  <div
    role="tooltip"
    id="tip-{t.id}"
    class="pointer-events-none absolute top-[34px] z-40 flex items-center gap-2 rounded-md bg-slate-900 px-2 py-1.5 text-xs whitespace-nowrap text-slate-50 shadow-lg {alignRight
      ? 'right-0'
      : 'left-1/2 -translate-x-1/2'}"
  >
    <span class="font-semibold">{t.label}</span>
    {#if t.keys}<kbd class="rounded border border-slate-700 bg-slate-800 px-1 font-mono text-[11px] text-slate-300">{k(t.keys)}</kbd>{/if}
    {#if t.syntax}<span class="font-mono text-[11px] text-slate-400">{t.syntax}</span>{/if}
  </div>
{/snippet}

{#snippet button(t, alignRight = false)}
  {@const on = isActive(t)}
  <div class="relative flex">
    <button
      type="button"
      data-tool={t.id}
      tabindex={tabIndexOf(order.indexOf(t.id))}
      class="flex h-7 cursor-pointer items-center justify-center gap-px rounded-md transition-colors disabled:cursor-default disabled:opacity-40 {t.menu &&
      t.id === 'heading'
        ? 'w-9'
        : 'w-7'} {on || menu === t.id ? 'bg-primary-soft text-primary' : 'text-slate-600 hover:bg-slate-200 hover:text-slate-900'} focus-visible:ring-2 focus-visible:ring-primary/40 focus-visible:outline-none"
      aria-label={t.label}
      aria-describedby={tip === t.id ? `tip-${t.id}` : undefined}
      aria-pressed={t.menu ? undefined : on}
      aria-haspopup={t.menu ? 'menu' : undefined}
      aria-expanded={t.menu ? menu === t.id : undefined}
      disabled={isDisabled(t)}
      onclick={() => click(t)}
      onpointerenter={() => !menu && showTip(t.id)}
      onpointerleave={hideTip}
      onfocus={() => !menu && showTip(t.id, true)}
      onblur={hideTip}
    >
      {@render icon(t.id)}
      {#if t.id === 'heading'}{@render icon('chevron', 10)}{/if}
    </button>
    {#if tip === t.id && menu !== t.id}{@render tooltip(t, alignRight)}{/if}
    {#if menu === 'heading' && t.id === 'heading'}
      <div role="menu" aria-label="Encabezado" class="absolute top-[34px] left-0 z-40 flex w-56 flex-col rounded-lg border border-slate-200 bg-surface p-1 shadow-lg">
        {#each HEADINGS as [lvl, label, keys, prefix]}
          <button
            type="button"
            role="menuitemradio"
            aria-checked={(active.heading ?? 0) === lvl}
            class="flex h-8 cursor-pointer items-center gap-2 rounded-md px-2 text-left text-[13px] text-slate-800 hover:bg-slate-100 {(active.heading ?? 0) === lvl
              ? 'bg-primary-tint font-semibold text-primary'
              : ''}"
            onclick={() => act('heading', lvl)}
          >
            <span class="w-7 font-mono text-xs text-slate-500">{prefix}</span>
            <span class="flex-1 {lvl === 1 ? 'text-[15px] font-bold' : lvl === 2 ? 'text-sm font-semibold' : lvl === 3 ? 'font-semibold' : ''}">{label}</span>
            <kbd class="font-mono text-[11px] text-slate-400">{k(keys)}</kbd>
          </button>
        {/each}
      </div>
    {/if}
    {#if menu === 'ref' && t.id === 'ref'}
      {@render refMenu('left-0')}
    {/if}
  </div>
{/snippet}

{#snippet refMenu(pos)}
  <div role="menu" aria-label="Referencias" class="absolute top-[34px] {pos} z-40 flex max-h-72 w-64 flex-col overflow-y-auto rounded-lg border border-slate-200 bg-surface p-1 shadow-lg">
    <span class="px-2 pt-1.5 pb-1 text-[11px] font-semibold tracking-wide text-slate-500 uppercase">Etiquetas de este archivo</span>
    {#each refLabels as l (l)}
      <button type="button" role="menuitem" class="flex h-8 cursor-pointer items-center gap-2 rounded-md px-2 text-left font-mono text-[13px] text-slate-800 hover:bg-slate-100" onclick={() => act('ref', l)}>
        <span class="text-slate-400">@</span>{l}
      </button>
    {:else}
      <p class="px-2 py-1.5 text-xs text-slate-500">No hay etiquetas <code>&lt;…&gt;</code> en este archivo.</p>
    {/each}
    <div class="my-1 h-px bg-slate-200"></div>
    <button type="button" role="menuitem" class="flex h-8 cursor-pointer items-center gap-2 rounded-md px-2 text-left text-[13px] text-slate-800 hover:bg-slate-100" onclick={() => act('cite')}>
      Citar de la bibliografía (.bib)…
    </button>
    <button type="button" role="menuitem" class="flex h-8 cursor-pointer items-center gap-2 rounded-md px-2 text-left text-[13px] text-slate-800 hover:bg-slate-100" onclick={() => act('ref')}>
      Escribir otra referencia o cita…
    </button>
  </div>
{/snippet}

<div
  bind:this={bar}
  role="toolbar"
  aria-label="Herramientas del editor"
  aria-orientation="horizontal"
  tabindex="-1"
  class="flex h-10 min-w-0 shrink-0 items-center gap-0.5 border-b border-slate-200 bg-surface pr-2 pl-3"
  onkeydown={onBarKey}
>
  <span class="mr-1 min-w-0 shrink truncate font-mono text-xs text-slate-600" title={file}>{file || '—'}</span>
  {#if !disabled}
    {#each visibleGroups as g}
      <div aria-hidden="true" class="mx-1.5 h-[18px] w-px shrink-0 bg-slate-200"></div>
      {#each g as t (t.id)}{@render button(t)}{/each}
    {/each}
    <div class="min-w-2 flex-1"></div>
    {@render button(SEARCH, true)}
    <div class="relative flex">
      <button
        type="button"
        data-tool="more"
        tabindex={tabIndexOf(order.indexOf('more'))}
        class="flex size-7 cursor-pointer items-center justify-center rounded-md transition-colors focus-visible:ring-2 focus-visible:ring-primary/40 focus-visible:outline-none {menu ===
        'more'
          ? 'bg-primary-soft text-primary'
          : 'text-slate-600 hover:bg-slate-200 hover:text-slate-900'}"
        aria-label="Más herramientas"
        aria-haspopup="menu"
        aria-expanded={menu === 'more'}
        onclick={() => openMenu('more')}
        onpointerenter={() => !menu && showTip('more')}
        onpointerleave={hideTip}
        onfocus={() => !menu && showTip('more', true)}
        onblur={hideTip}
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="19" cy="12" r="1.6" /></svg>
      </button>
      {#if tip === 'more' && !menu}{@render tooltip({ id: 'more', label: 'Más herramientas' }, true)}{/if}
      {#if menu === 'more'}
        <div role="menu" aria-label="Más herramientas" class="absolute top-[34px] right-0 z-40 flex w-72 flex-col rounded-lg border border-slate-200 bg-surface p-1.5 shadow-lg">
          {#each folded as g, gi}
            {#if gi > 0}<div aria-hidden="true" class="mx-1 my-1.5 h-px bg-slate-200"></div>{/if}
            {#each g as t (t.id)}
              {#if t.id === 'heading'}
                {#each HEADINGS as [lvl, label, keys]}
                  <button type="button" role="menuitem" class="flex h-8 cursor-pointer items-center gap-2.5 rounded-md px-2 text-left text-[13px] text-slate-800 hover:bg-slate-100" onclick={() => act('heading', lvl)}>
                    {@render icon('heading')}<span class="flex-1">{label}</span><kbd class="font-mono text-[11px] text-slate-400">{k(keys)}</kbd>
                  </button>
                {/each}
              {:else if t.id === 'ref'}
                <button type="button" role="menuitem" class="flex h-8 cursor-pointer items-center gap-2.5 rounded-md px-2 text-left text-[13px] text-slate-800 hover:bg-slate-100" onclick={() => openMenu('ref')}>
                  {@render icon('ref')}<span class="flex-1">{t.label}</span><span class="font-mono text-[11px] text-slate-400">@</span>
                </button>
              {:else}
                <button
                  type="button"
                  role="menuitem"
                  disabled={isDisabled(t)}
                  class="flex h-8 cursor-pointer items-center gap-2.5 rounded-md px-2 text-left text-[13px] text-slate-800 hover:bg-slate-100 disabled:cursor-default disabled:opacity-40 {isActive(t)
                    ? 'text-primary'
                    : ''}"
                  onclick={() => act(t.id)}
                >
                  {@render icon(t.id)}<span class="flex-1">{t.label}</span>
                  {#if t.keys}<kbd class="font-mono text-[11px] text-slate-400">{k(t.keys)}</kbd>{:else if t.syntax}<span class="font-mono text-[11px] text-slate-400">{t.syntax.split('(')[0]}</span>{/if}
                </button>
              {/if}
            {/each}
          {:else}
            <p class="px-2 py-1.5 text-xs text-slate-500">Todas las herramientas están a la vista. Las próximas aparecerán aquí.</p>
          {/each}
        </div>
      {/if}
      {#if menu === 'ref' && folded.flat().some((t) => t.id === 'ref')}
        {@render refMenu('right-0')}
      {/if}
    </div>
  {/if}
</div>
