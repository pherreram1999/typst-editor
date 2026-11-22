<script>
  // Editor visual de tablas Typst.
  // - openInsert(): elegir dimensiones (selector tipo cuadrícula o números) y
  //   editar celdas; onapply({ code }) con «table(…)» para insertarlo.
  // - openEdit(src): abre una tabla existente (parseTable); insertar filas o
  //   columnas, combinar y separar celdas conserva todo el contenido (y los
  //   demás argumentos: stroke, fill, align por columna, comentarios…).
  // onclose() al cancelar.
  import Modal from '../Modal.svelte'
  import * as T from '../editor/tablecode.js'

  let { onapply, onclose } = $props()

  let open = $state(false)
  let mode = $state('insert') // insert | edit
  let model = $state(null)
  let sel = $state({ r0: 0, c0: 0, r1: 0, c1: 0 })
  let history = $state([]) // instantáneas para «Deshacer»
  let message = $state('')
  let picker = $state({ r: 0, c: 0 }) // selector de tamaño (insertar)
  let showCode = $state(false)
  let pickerOpen = $state(false)
  let pickerBox = $state()
  let indent = ''

  export function openInsert() {
    mode = 'insert'
    model = T.newTable(3, 3, true)
    indent = ''
    reset()
  }
  export function openEdit(src, lineIndent = '') {
    const p = T.parseTable(src)
    if (!p.ok) return p.error
    mode = 'edit'
    model = p.model
    indent = lineIndent
    reset()
    return null
  }
  function reset() {
    sel = { r0: 0, c0: 0, r1: 0, c1: 0 }
    history = []
    message = ''
    showCode = false
    pickerOpen = false
    open = true
  }

  const grid = $derived(model ? T.gridOf(model) : [])
  const rect = $derived(model ? T.normalizeRect(model, sel) : sel)
  const selCell = $derived(grid[rect.r0]?.[rect.c0])
  const single = $derived(!!selCell && selCell.r === rect.r0 && selCell.c === rect.c0 && selCell.r + selCell.rs - 1 === rect.r1 && selCell.c + selCell.cs - 1 === rect.c1)
  const canMerge = $derived(model ? T.canMerge(model, rect) : false)
  const canSplit = $derived(single && (selCell.rs > 1 || selCell.cs > 1))
  const code = $derived(model ? T.generate(model, indent) : '')
  const merged = $derived(model ? model.cells.filter((c) => c.rs > 1 || c.cs > 1).length : 0)

  // Cambios de estructura: guardar instantánea para deshacer.
  function change(fn) {
    const snap = $state.snapshot(model)
    message = ''
    const r = fn()
    if (r === false) {
      model = snap // no dejar cambios a medias
      return false
    }
    history = [...history.slice(-49), snap]
    return r
  }
  function undo() {
    if (!history.length) return
    model = history[history.length - 1]
    history = history.slice(0, -1)
    clampSel()
  }
  function clampSel() {
    const cl = (v, n) => Math.min(Math.max(v, 0), n - 1)
    sel = { r0: cl(sel.r0, model.R), c0: cl(sel.c0, model.C), r1: cl(sel.r1, model.R), c1: cl(sel.c1, model.C) }
  }

  const confirmLost = (lost) => !lost.some((c) => !T.isEmpty(c)) || confirm(`Se borrará el contenido de ${lost.filter((c) => !T.isEmpty(c)).length} celda(s). ¿Continuar?`)

  function rowsWithContent(r0, r1) {
    return model.cells.filter((c) => c.r >= r0 && c.r <= r1 && c.r + c.rs - 1 <= r1 && !T.isEmpty(c))
  }
  function colsWithContent(c0, c1) {
    return model.cells.filter((c) => c.c >= c0 && c.c <= c1 && c.c + c.cs - 1 <= c1 && !T.isEmpty(c))
  }

  const ops = {
    rowAbove: () => {
      const { r0, c0 } = rect
      change(() => (T.insertRow(model, r0), (sel = { r0, c0, r1: r0, c1: c0 })))
    },
    rowBelow: () => {
      const { r1, c0 } = rect
      change(() => (T.insertRow(model, r1 + 1), (sel = { r0: r1 + 1, c0, r1: r1 + 1, c1: c0 })))
    },
    colLeft: () => {
      const { r0, c0 } = rect
      change(() => (T.insertCol(model, c0, c0), (sel = { r0, c0, r1: r0, c1: c0 })))
    },
    colRight: () => {
      const { r0, c1 } = rect
      change(() => (T.insertCol(model, c1 + 1, c1), (sel = { r0, c0: c1 + 1, r1: r0, c1: c1 + 1 })))
    },
    delRows: () => {
      const { r0, r1 } = rect
      if (r1 - r0 + 1 >= model.R) return (message = 'La tabla necesita al menos una fila')
      if (!confirmLost(rowsWithContent(r0, r1))) return
      change(() => {
        for (let r = r1; r >= r0; r--) T.deleteRow(model, r)
        clampSel()
      })
    },
    delCols: () => {
      const { c0, c1 } = rect
      if (c1 - c0 + 1 >= model.C) return (message = 'La tabla necesita al menos una columna')
      if (!confirmLost(colsWithContent(c0, c1))) return
      change(() => {
        for (let c = c1; c >= c0; c--) T.deleteCol(model, c)
        clampSel()
      })
    },
    merge: () => {
      if (!canMerge) return
      const r = { ...rect }
      change(() => {
        const cell = T.merge(model, r)
        if (cell) sel = { r0: cell.r, c0: cell.c, r1: cell.r + cell.rs - 1, c1: cell.c + cell.cs - 1 }
      })
    },
    split: () => {
      if (!canSplit) return
      const cell = selCell
      const { r0, c0 } = rect
      change(() => {
        T.split(model, cell)
        sel = { r0, c0, r1: r0, c1: c0 }
      })
    },
  }

  function setDim(kind, v) {
    const n = Math.min(Math.max(parseInt(v) || 1, 1), kind === 'rows' ? 200 : 30)
    const ok = change(() => (kind === 'rows' ? T.setRows(model, n) : T.setCols(model, n)))
    if (ok === false) {
      message = `Solo se quitan ${kind === 'rows' ? 'filas' : 'columnas'} vacías del final: para borrar contenido usa «Eliminar ${kind === 'rows' ? 'fila' : 'columna'}».`
    }
    clampSel()
    titleHeader()
  }
  function setHeader(v) {
    const n = Math.max(0, parseInt(v) || 0)
    if (change(() => T.setHeaderRows(model, n)) === false) message = 'Una celda combinada cruza esa fila: sepárala antes de cambiar el encabezado.'
  }
  function pickSize(r, c) {
    if (change(() => T.setRows(model, r) && T.setCols(model, c)) === false) message = 'Solo se quitan filas y columnas vacías del final.'
    clampSel()
    titleHeader()
  }
  // Tabla nueva: las celdas vacías del encabezado se titulan «Columna N».
  function titleHeader() {
    if (mode !== 'insert') return
    for (const cell of model.cells) if (cell.r === 0 && model.headerRows && T.isEmpty(cell)) cell.body.text = `*Columna ${cell.c + 1}*`
  }

  // Anchos de columna: «columns: 3» equivale a (auto, auto, auto).
  function setWidth(c, v) {
    v = v.trim() || 'auto'
    const ws = model.widths ? [...model.widths] : Array(model.C).fill('auto')
    if (ws[c] === v) return
    change(() => {
      ws[c] = v
      model.widths = ws
      if (model.widthsForm !== 'array') model.widthsForm = 'array'
      if (!model.named.some((n) => n.name === 'columns')) model.named.unshift({ name: 'columns', value: '', lead: '' })
    })
  }

  // ---------- selección con el ratón (clic, arrastrar, Mayús+clic) ----------
  let dragFrom = null
  function down(e, cell) {
    if (e.button !== 0) return
    if (e.shiftKey) {
      e.preventDefault()
      sel = { r0: Math.min(sel.r0, cell.r), c0: Math.min(sel.c0, cell.c), r1: Math.max(sel.r0, cell.r + cell.rs - 1), c1: Math.max(sel.c0, cell.c + cell.cs - 1) }
      return
    }
    dragFrom = cell
    sel = { r0: cell.r, c0: cell.c, r1: cell.r + cell.rs - 1, c1: cell.c + cell.cs - 1 }
  }
  function enter(cell) {
    if (!dragFrom || cell === dragFrom) return
    const a = dragFrom
    // Al pasar a otra celda deja de ser selección de texto.
    if (document.activeElement instanceof HTMLTextAreaElement) document.activeElement.blur()
    window.getSelection()?.removeAllRanges()
    sel = { r0: Math.min(a.r, cell.r), c0: Math.min(a.c, cell.c), r1: Math.max(a.r + a.rs - 1, cell.r + cell.rs - 1), c1: Math.max(a.c + a.cs - 1, cell.c + cell.cs - 1) }
  }
  const up = () => (dragFrom = null)

  // Llevar la celda seleccionada a la vista (al insertar filas/columnas o con Tab
  // en tablas grandes); sus márgenes de scroll evitan los encabezados fijos.
  $effect(() => {
    if (!open || !model) return
    const { r0, c0 } = rect
    model.R, model.C // también cuando cambia la estructura
    requestAnimationFrame(() => document.querySelector(`[data-cell="${r0}-${c0}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' }))
  })

  const inSel = (cell) => cell.r >= rect.r0 && cell.r + cell.rs - 1 <= rect.r1 && cell.c >= rect.c0 && cell.c + cell.cs - 1 <= rect.c1
  const multi = $derived(rect.r0 !== rect.r1 || rect.c0 !== rect.c1)

  // Atajos dentro del diálogo.
  function keydown(e) {
    const mod = e.ctrlKey || e.metaKey
    if (mod && e.key.toLowerCase() === 'm' && !e.shiftKey) (e.preventDefault(), ops.merge())
    else if (mod && e.shiftKey && e.key.toLowerCase() === 'm') (e.preventDefault(), ops.split())
    else if (mod && e.key === 'Enter') (e.preventDefault(), apply())
    else if (mod && e.key.toLowerCase() === 'z' && !(e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLInputElement)) (e.preventDefault(), undo())
  }
  // Tab / Mayús+Tab entre celdas como en una hoja de cálculo; Enter = salto de línea.
  function cellKey(e, cell) {
    if (e.key !== 'Tab') return
    const idx = model.cells.indexOf(cell)
    const next = model.cells[idx + (e.shiftKey ? -1 : 1)]
    if (!next) return
    e.preventDefault()
    sel = { r0: next.r, c0: next.c, r1: next.r + next.rs - 1, c1: next.c + next.cs - 1 }
    queueMicrotask(() => document.querySelector(`[data-cell="${next.r}-${next.c}"]`)?.focus())
  }

  function apply() {
    open = false
    onapply?.({ code: T.generate(model, indent), mode })
  }
  function cancel() {
    open = false
    onclose?.()
  }

  const sectionClass = (cell) => {
    const s = T.sectionOf(model, cell.r)
    return s === 'header' ? 'bg-slate-100 font-semibold' : s === 'footer' ? 'bg-slate-50 italic' : 'bg-surface'
  }
  const colName = (c) => String.fromCharCode(65 + (c % 26)) + (c >= 26 ? Math.floor(c / 26) : '')

  const Btn = 'btn-secondary btn-sm !h-8 !px-2.5 gap-1.5'
</script>

<svelte:window onpointerup={up} onpointerdown={(e) => pickerOpen && !pickerBox?.contains(e.target) && (pickerOpen = false)} />

<Modal {open} title={mode === 'edit' ? 'Editar tabla' : 'Insertar tabla'} wide={model && model.C > 5 ? 'full' : 'xl'} fit onclose={cancel}>
  {#if model}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="flex min-h-0 flex-1 flex-col gap-4" onkeydown={keydown}>
      <div class="flex shrink-0 flex-wrap items-end gap-4">
        {#if mode === 'insert'}
          <!-- Selector rápido de tamaño, desplegable para no quitarle alto a la cuadrícula. -->
          <div class="relative flex flex-col gap-1.5" bind:this={pickerBox}>
            <span class="label">Tamaño</span>
            <button type="button" class="btn-secondary btn-sm !h-9 gap-1.5" aria-expanded={pickerOpen} onclick={() => (pickerOpen = !pickerOpen)}>
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M3 9h18M3 15h18M9 3v18M15 3v18" /></svg>
              Cuadrícula
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
            </button>
            {#if pickerOpen}
              <div class="absolute top-full left-0 z-30 mt-1 flex flex-col gap-1.5 rounded-lg border border-slate-200 bg-surface p-2.5 shadow-lg">
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <div class="grid w-max grid-cols-[repeat(10,1rem)] gap-0.5" onpointerleave={() => (picker = { r: 0, c: 0 })}>
                  {#each Array(8) as _, r}
                    {#each Array(10) as _, c}
                      <button
                        type="button"
                        tabindex="-1"
                        aria-label="{r + 1} × {c + 1}"
                        class="size-4 cursor-pointer rounded-[3px] border {r < (picker.r || model.R) && c < (picker.c || model.C) ? 'border-primary bg-primary/30' : 'border-slate-300 bg-surface'}"
                        onpointerenter={() => (picker = { r: r + 1, c: c + 1 })}
                        onclick={() => ((pickerOpen = false), pickSize(r + 1, c + 1))}
                      ></button>
                    {/each}
                  {/each}
                </div>
                <span class="text-center text-xs text-slate-500">{picker.r ? `${picker.r} × ${picker.c}` : `${model.R} × ${model.C}`}</span>
              </div>
            {/if}
          </div>
        {/if}
        <label class="flex flex-col gap-1.5">
          <span class="label">Filas</span>
          <input class="input !h-9 w-20" type="number" min="1" max="200" value={model.R} onchange={(e) => setDim('rows', e.currentTarget.value)} />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="label">Columnas</span>
          <input class="input !h-9 w-20" type="number" min="1" max="30" value={model.C} onchange={(e) => setDim('cols', e.currentTarget.value)} />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="label">Filas de encabezado</span>
          <input class="input !h-9 w-20" type="number" min="0" max={model.R - model.footerRows} value={model.headerRows} onchange={(e) => setHeader(e.currentTarget.value)} />
        </label>
        <p class="ml-auto self-center text-xs text-slate-500">
          {model.R} × {model.C}{merged ? ` · ${merged} combinada${merged === 1 ? '' : 's'}` : ''}
        </p>
      </div>

      <!-- Herramientas -->
      <div class="flex shrink-0 flex-wrap items-center gap-1.5" role="toolbar" aria-label="Herramientas de tabla">
        <button type="button" class={Btn} onclick={ops.rowAbove} title="Insertar fila encima">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><rect x="3" y="12" width="18" height="9" rx="1.5" /><path d="M12 3v6M9 6h6" /></svg>
          Fila arriba
        </button>
        <button type="button" class={Btn} onclick={ops.rowBelow} title="Insertar fila debajo">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><rect x="3" y="3" width="18" height="9" rx="1.5" /><path d="M12 15v6M9 18h6" /></svg>
          Fila abajo
        </button>
        <button type="button" class={Btn} onclick={ops.colLeft} title="Insertar columna a la izquierda">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><rect x="12" y="3" width="9" height="18" rx="1.5" /><path d="M3 12h6M6 9v6" /></svg>
          Columna izq.
        </button>
        <button type="button" class={Btn} onclick={ops.colRight} title="Insertar columna a la derecha">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><rect x="3" y="3" width="9" height="18" rx="1.5" /><path d="M15 12h6M18 9v6" /></svg>
          Columna der.
        </button>
        <span class="mx-1 h-6 w-px bg-slate-200" aria-hidden="true"></span>
        <button type="button" class={Btn} onclick={ops.merge} disabled={!canMerge} title="Combinar las celdas seleccionadas (Ctrl+M); el contenido se une, no se pierde">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="1.5" /><path d="m8 9 3 3-3 3M16 9l-3 3 3 3" /></svg>
          Combinar
        </button>
        <button type="button" class={Btn} onclick={ops.split} disabled={!canSplit} title="Separar la celda combinada (Ctrl+Mayús+M)">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="1.5" /><path d="M12 5v14M7 12h2M15 12h2" /></svg>
          Separar
        </button>
        <span class="mx-1 h-6 w-px bg-slate-200" aria-hidden="true"></span>
        <button type="button" class="{Btn} !text-red-700" onclick={ops.delRows} title="Eliminar las filas seleccionadas">Eliminar fila</button>
        <button type="button" class="{Btn} !text-red-700" onclick={ops.delCols} title="Eliminar las columnas seleccionadas">Eliminar columna</button>
        <button type="button" class="{Btn} ml-auto" onclick={undo} disabled={!history.length} title="Deshacer el último cambio de estructura (Ctrl+Z)">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 14 4 9l5-5" /><path d="M4 9h11a5 5 0 0 1 0 10h-3" /></svg>
          Deshacer
        </button>
      </div>

      {#if message}<p class="alert-info shrink-0 !py-2 text-sm">{message}</p>{/if}

      <!-- Cuadrícula -->
      <!-- Solo esta zona hace scroll (en los dos ejes); letras de columna y números de fila quedan fijos. -->
      <div class="min-h-48 flex-1 overflow-auto overscroll-contain rounded-lg border border-slate-200 bg-slate-50 px-2 pb-2">
        <div class="grid w-max min-w-full gap-px" style="grid-template-columns: 1.75rem repeat({model.C}, minmax(8rem, 1fr));">
          <span class="sticky top-0 left-0 z-20 bg-slate-50" style="grid-column: 1; grid-row: 1;"></span>
          {#each Array(model.C) as _, c}
            <div class="sticky top-0 z-10 flex items-center gap-1 bg-slate-50 px-1 pt-2 pb-1" style="grid-column: {c + 2}; grid-row: 1;">
              <span class="text-[11px] font-semibold text-slate-400">{colName(c)}</span>
              <input
                class="h-6 min-w-0 flex-1 rounded border border-transparent bg-transparent px-1 font-mono text-[11px] text-slate-600 hover:border-slate-300 focus:border-primary focus:bg-surface focus:outline-none"
                value={model.widths?.[c] ?? 'auto'}
                title="Ancho de la columna (auto, 1fr, 3cm, 30%…)"
                aria-label="Ancho de la columna {colName(c)}"
                onchange={(e) => setWidth(c, e.currentTarget.value)}
              />
            </div>
          {/each}
          {#each Array(model.R) as _, r}
            <span class="sticky left-0 z-10 flex items-start justify-center bg-slate-50 pt-2 text-[11px] font-semibold text-slate-400" style="grid-column: 1; grid-row: {r + 2};">{r + 1}</span>
          {/each}
          {#each model.cells as cell (cell)}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="relative flex min-h-10 rounded-[3px] ring-inset {sectionClass(cell)} {inSel(cell) ? 'ring-2 ring-primary' : 'ring-1 ring-slate-200'}"
              style="grid-column: {cell.c + 2} / span {cell.cs}; grid-row: {cell.r + 2} / span {cell.rs};"
              onpointerdown={(e) => down(e, cell)}
              onpointerenter={() => enter(cell)}
            >
              <textarea
                data-cell="{cell.r}-{cell.c}"
                rows="1"
                class="w-full scroll-mt-10 scroll-ml-9 resize-none bg-transparent px-2 py-2 text-sm leading-snug focus:outline-none [field-sizing:content] {cell.body.kind === 'expr' ? 'font-mono text-[13px] text-slate-600' : ''}"
                title={cell.body.kind === 'expr' ? 'Expresión de código Typst' : 'Contenido (marcado Typst)'}
                aria-label="Celda {colName(cell.c)}{cell.r + 1}"
                bind:value={cell.body.text}
                onkeydown={(e) => cellKey(e, cell)}
              ></textarea>
              {#if multi && inSel(cell)}<span class="pointer-events-none absolute inset-0 rounded-[3px] bg-primary/10"></span>{/if}
              {#if cell.rs > 1 || cell.cs > 1}
                <span class="pointer-events-none absolute right-1 bottom-0.5 text-[10px] text-slate-400">{cell.cs > 1 ? `${cell.cs} col` : ''}{cell.cs > 1 && cell.rs > 1 ? ' · ' : ''}{cell.rs > 1 ? `${cell.rs} fil` : ''}</span>
              {/if}
              {#if cell.extra.length}
                <span class="pointer-events-none absolute top-0.5 right-1 text-[10px] text-slate-400" title={cell.extra.join(', ')}>⚙</span>
              {/if}
            </div>
          {/each}
        </div>
      </div>
      <p class="shrink-0 text-xs text-slate-500">
        Arrastra o usa Mayús+clic para seleccionar varias celdas y combinarlas. Las celdas usan marcado Typst (<code>*negrita*</code>, <code>$x^2$</code>…). Tab pasa a la siguiente celda.
      </p>

      <details bind:open={showCode} class="shrink-0 text-sm">
        <summary class="cursor-pointer text-slate-500 select-none hover:text-slate-900">Ver código</summary>
        <pre class="mt-2 max-h-[min(12rem,25dvh)] overflow-auto rounded-lg border border-slate-200 bg-slate-50 p-3 font-mono text-xs">{showCode ? (mode === 'insert' ? '#' : '') + code : ''}</pre>
      </details>

      <div class="flex shrink-0 items-center justify-end gap-2">
        <button type="button" class="btn-secondary btn-sm" onclick={cancel}>Cancelar</button>
        <button type="button" class="btn-primary btn-sm" onclick={apply} title="Ctrl+Enter">{mode === 'edit' ? 'Aplicar cambios' : 'Insertar tabla'}</button>
      </div>
    </div>
  {/if}
</Modal>
