<script>
  // Ventana de fusión a tres bandas, al estilo de JetBrains: a la izquierda
  // tus cambios (local), en el centro el resultado (editable, empieza como la
  // versión base común) y a la derecha los del repositorio. Cada cambio se
  // aplica al resultado con » / « o se descarta con ×; la varita aplica todos
  // los que no tienen conflicto. Los paneles se desplazan juntos y unas
  // bandas unen cada cambio con su lugar en el resultado.
  //
  // Props: file { path, base, ours, theirs }, branch, onapply(text), oncancel()
  import { onMount, untrack } from 'svelte'
  import { EditorState, StateField } from '@codemirror/state'
  import { EditorView, Decoration } from '@codemirror/view'
  import { basicSetup } from 'codemirror'
  import { syntaxHighlighting } from '@codemirror/language'
  import { typstLanguage, typstHighlight } from '../editor/typst-lang.js'
  import { bibLanguage } from '../editor/bib-lang.js'
  import { editorTheme } from '../editor/collab-client.js'
  import { diff3 } from './diff3.js'

  let { file, branch = '', onapply, oncancel } = $props()

  const d = untrack(() => diff3(file.base ?? '', file.ours ?? '', file.theirs ?? ''))
  const lines = { ours: d.ours, theirs: d.theirs }
  const changes = (c, side) => c.type === 'conflict' || c.type === 'both' || c.type === side

  // Posiciones de inicio de cada línea de la base (el resultado empieza como la base).
  const baseOff = [0]
  for (const l of d.base) baseOff.push(baseOff[baseOff.length - 1] + l.length)

  // Estado de cada cambio (aplicado o descartado por cada lado).
  let chunks = $state(
    d.chunks.map((c) => ({
      ...c,
      applied: { ours: false, theirs: false },
      ignored: { ours: false, theirs: false },
    })),
  )
  // Rangos de cada cambio en el resultado, dentro del estado del editor: se
  // mapean en la misma transacción que la edición, así las decoraciones nunca
  // ven posiciones viejas sobre el documento nuevo.
  const resField = StateField.define({
    create: () => d.chunks.map((c) => [baseOff[c.base[0]], baseOff[c.base[1]]]),
    update(ranges, tr) {
      if (!tr.docChanged) return ranges
      return ranges.map(([from, to]) => {
        const f = tr.changes.mapPos(from, -1)
        return [f, Math.max(f, tr.changes.mapPos(to, 1))]
      })
    },
  })
  const resOf = (i) => {
    const [from, to] = views.result.state.field(resField)[i]
    return { from, to }
  }

  const handled = (c, side) => !changes(c, side) || c.applied[side] || c.ignored[side]
  // Como en JetBrains: un conflicto queda resuelto al aplicar uno de sus lados
  // (el otro sigue ofreciendo » / « para añadirlo debajo, o × para descartarlo).
  const resolved = (c) => (handled(c, 'ours') && handled(c, 'theirs')) || (c.type === 'conflict' && (c.applied.ours || c.applied.theirs))
  const pending = $derived(chunks.filter((c) => !resolved(c)))
  const conflicts = $derived(pending.filter((c) => c.type === 'conflict').length)

  let hostL = $state()
  let hostC = $state()
  let hostR = $state()
  let divL = $state()
  let divR = $state()
  let views = { ours: null, result: null, theirs: null }
  let geo = $state({ left: [], right: [], w: 44 })
  let current = $state(-1) // índice del cambio enfocado (navegación)

  const lang = file.path.endsWith('.bib') ? bibLanguage : file.path.endsWith('.typ') ? typstLanguage : []

  // ---------- decoraciones ----------
  const kind = (c, side) => (c.type === 'conflict' ? 'conflict' : side && c[side][0] === c[side][1] ? 'del' : c.base[0] === c.base[1] ? 'add' : 'mod')

  function rangeDeco(doc, from, to, cls, out) {
    if (from === to || doc.length === 0) {
      const line = doc.lineAt(Math.min(from, doc.length))
      out.push(Decoration.line({ class: `cm-m-gap ${cls}` }).range(line.from))
      return
    }
    const a = doc.lineAt(from).number
    const b = doc.lineAt(Math.max(from, to - 1)).number
    for (let n = a; n <= b; n++) out.push(Decoration.line({ class: cls }).range(doc.line(n).from))
  }
  const lineStart = (doc, i) => (i < doc.lines ? doc.line(i + 1).from : doc.length)

  function sideDecos(side) {
    return (view) => {
      const out = []
      for (const c of chunks) {
        if (!changes(c, side)) continue
        const done = c.applied[side] ? ' cm-m-applied' : c.ignored[side] ? ' cm-m-ignored' : ''
        rangeDeco(view.state.doc, lineStart(view.state.doc, c[side][0]), lineStart(view.state.doc, c[side][1]), `cm-m-${kind(c, side)}${done}`, out)
      }
      return Decoration.set(out, true)
    }
  }
  function resultDecos(view) {
    const out = []
    const ranges = view.state.field(resField)
    chunks.forEach((c, i) => {
      const cls = resolved(c) ? 'cm-m-resolved' : `cm-m-${kind(c)}`
      rangeDeco(view.state.doc, ranges[i][0], ranges[i][1], cls + (i === current ? ' cm-m-current' : ''), out)
    })
    return Decoration.set(out, true)
  }

  const mergeTheme = EditorView.theme({
    '&': { height: '100%' },
    '.cm-content': { paddingBottom: '30vh' },
    '.cm-m-mod': { backgroundColor: 'rgb(59 130 246 / 0.14)' },
    '.cm-m-add': { backgroundColor: 'rgb(34 197 94 / 0.16)' },
    '.cm-m-del': { backgroundColor: 'rgb(148 163 184 / 0.22)' },
    '.cm-m-conflict': { backgroundColor: 'rgb(239 68 68 / 0.16)' },
    '.cm-m-applied, .cm-m-ignored, .cm-m-resolved': { backgroundColor: 'rgb(148 163 184 / 0.08) !important' },
    '.cm-m-gap': { backgroundColor: 'transparent !important', boxShadow: 'inset 0 2px 0 var(--m-gap, rgb(148 163 184))' },
    '.cm-m-gap.cm-m-conflict': { '--m-gap': 'rgb(239 68 68)' },
    '.cm-m-gap.cm-m-add, .cm-m-gap.cm-m-mod': { '--m-gap': 'rgb(59 130 246)' },
    '.cm-m-current': { outline: '1px solid rgb(59 130 246 / 0.5)' },
  })

  function makeView(parent, doc, editable, decos, onUpdate, extra = []) {
    return new EditorView({
      parent,
      state: EditorState.create({
        doc,
        extensions: [
          basicSetup,
          lang,
          syntaxHighlighting(typstHighlight),
          editorTheme,
          mergeTheme,
          EditorState.readOnly.of(!editable),
          EditorView.editable.of(editable),
          EditorView.decorations.of(decos),
          EditorView.updateListener.of((u) => onUpdate?.(u)),
          extra,
        ],
      }),
    })
  }

  // Volver a pintar decoraciones y bandas.
  function refresh() {
    for (const v of Object.values(views)) v?.dispatch({})
    schedule()
  }

  // ---------- aplicar / descartar ----------
  function apply(i, side) {
    const c = chunks[i]
    const v = views.result
    const text = lines[side].slice(c[side][0], c[side][1]).join('')
    const other = side === 'ours' ? 'theirs' : 'ours'
    const { from, to } = resOf(i)
    if (c.type === 'conflict' && c.applied[other]) v.dispatch({ changes: { from: to, insert: text }, userEvent: 'input.merge' })
    else v.dispatch({ changes: { from, to, insert: text }, userEvent: 'input.merge' })
    c.applied[side] = true
    if (c.type === 'both') c.applied[other] = true
    refresh()
  }
  function ignore(i, side) {
    chunks[i].ignored[side] = true
    refresh()
  }
  // Varita: aplica todos los cambios sin conflicto.
  function applyNonConflicting() {
    chunks.forEach((c, i) => {
      if (c.type === 'conflict' || resolved(c)) return
      apply(i, c.type === 'theirs' ? 'theirs' : 'ours')
    })
  }

  // ---------- geometría: bandas entre paneles ----------
  let frame = 0
  function schedule() {
    if (!frame) frame = requestAnimationFrame(measure)
  }
  function measure() {
    frame = 0
    const { ours: L, result: C, theirs: R } = views
    if (!L || !C || !R || !divL) return
    const top = divL.getBoundingClientRect().top
    const h = divL.clientHeight
    const y = (v, pos, end = false) => {
      const b = v.lineBlockAt(Math.min(pos, v.state.doc.length))
      return (end ? b.bottom : b.top) + v.documentTop - top
    }
    const span = (v, a, b, byLine) => {
      const doc = v.state.doc
      const from = byLine ? lineStart(doc, a) : a
      const to = byLine ? lineStart(doc, b) : b
      if (from === to || doc.length === 0) {
        const t = from >= doc.length && doc.length > 0 && !byLine ? y(v, doc.length, true) : y(v, from)
        return [t, t]
      }
      return [y(v, from), y(v, to - 1, true)]
    }
    const build = (side, view) =>
      chunks
        .map((c, i) => {
          if (!changes(c, side)) return null
          const [a0, a1] = span(view, c[side][0], c[side][1], true)
          const r = resOf(i)
          const [b0, b1] = span(C, r.from, r.to, false)
          if (Math.max(a1, b1) < -40 || Math.min(a0, b0) > h + 40) return null
          return { i, a0, a1, b0, b1, cls: c.type === 'conflict' ? 'conflict' : kind(c, side), done: handled(c, side), append: c.type === 'conflict' && c.applied[side === 'ours' ? 'theirs' : 'ours'] }
        })
        .filter(Boolean)
    geo = { left: build('ours', L), right: build('theirs', R), w: divL.clientWidth }
  }
  const band = (g, w, flip) => {
    // flip: en el divisor derecho, el panel del lado está a la derecha.
    const [l0, l1, r0, r1] = flip ? [g.b0, g.b1, g.a0, g.a1] : [g.a0, g.a1, g.b0, g.b1]
    const m = w / 2
    return `M0,${l0} C${m},${l0} ${m},${r0} ${w},${r0} L${w},${r1} C${m},${r1} ${m},${l1} 0,${l1} Z`
  }

  // ---------- desplazamiento sincronizado ----------
  // Rangos de líneas de cada cambio en cada panel (el resultado, según res).
  function resLines(i) {
    const doc = views.result.state.doc
    const r = resOf(i)
    const a = doc.lineAt(r.from).number - 1
    const b = r.to > r.from ? doc.lineAt(r.to - 1).number : a
    return [a, b]
  }
  const rangesOf = (pane) => chunks.map((c, i) => (pane === 'result' ? resLines(i) : c[pane]))
  function mapLine(L, from, to) {
    let px = 0
    let py = 0
    for (let k = 0; k < from.length; k++) {
      const [x0, x1] = from[k]
      const [y0, y1] = to[k]
      if (L < x0) break
      if (L < x1) return y0 + Math.floor(((L - x0) * (y1 - y0)) / Math.max(1, x1 - x0))
      px = x1
      py = y1
    }
    return py + (L - px)
  }
  let syncing = 0
  function onScroll(pane) {
    schedule()
    if (syncing) return
    const src = views[pane]
    const h = src.scrollDOM.scrollTop
    const blk = src.lineBlockAtHeight(h)
    const frac = blk.height ? (h - blk.top) / blk.height : 0
    const L = src.state.doc.lineAt(blk.from).number - 1
    const fromR = rangesOf(pane)
    syncing++
    for (const other of ['ours', 'result', 'theirs']) {
      if (other === pane) continue
      const v = views[other]
      const target = Math.min(Math.max(mapLine(L, fromR, rangesOf(other)), 0), v.state.doc.lines - 1)
      const b = v.lineBlockAt(v.state.doc.line(target + 1).from)
      v.scrollDOM.scrollTop = b.top + frac * b.height
    }
    requestAnimationFrame(() => requestAnimationFrame(() => syncing--))
  }

  // ---------- navegación ----------
  function go(dir) {
    const list = chunks.map((c, i) => i).filter((i) => !resolved(chunks[i]))
    if (!list.length) return
    const next = dir > 0 ? (list.find((i) => i > current) ?? list[0]) : ([...list].reverse().find((i) => i < current) ?? list[list.length - 1])
    current = next
    const v = views.result
    const at = resOf(next).from
    v.dispatch({ selection: { anchor: at }, effects: EditorView.scrollIntoView(at, { y: 'center' }) })
    v.focus()
    requestAnimationFrame(() => onScroll('result'))
  }

  onMount(() => {
    views.ours = makeView(hostL, d.ours.join(''), false, sideDecos('ours'))
    views.theirs = makeView(hostR, d.theirs.join(''), false, sideDecos('theirs'))
    views.result = makeView(
      hostC,
      d.base.join(''),
      true,
      resultDecos,
      (u) => {
        if (u.docChanged || u.geometryChanged || u.viewportChanged) schedule()
      },
      resField,
    )
    const subs = ['ours', 'result', 'theirs'].map((p) => {
      const fn = () => onScroll(p)
      views[p].scrollDOM.addEventListener('scroll', fn, { passive: true })
      return () => views[p]?.scrollDOM.removeEventListener('scroll', fn)
    })
    const ro = new ResizeObserver(schedule)
    ro.observe(divL)
    schedule()
    // Empezar en el primer cambio.
    requestAnimationFrame(() => go(1))
    return () => {
      subs.forEach((f) => f())
      ro.disconnect()
      cancelAnimationFrame(frame)
      for (const v of Object.values(views)) v?.destroy()
      views = { ours: null, result: null, theirs: null }
    }
  })

  function finish() {
    if (conflicts && !confirm(`Quedan ${conflicts} ${conflicts === 1 ? 'conflicto' : 'conflictos'} sin resolver. ¿Guardar el resultado tal como está?`)) return
    onapply?.(views.result.state.doc.toString())
  }
  function cancel() {
    const touched = chunks.some((c) => c.applied.ours || c.applied.theirs || c.ignored.ours || c.ignored.theirs) || views.result.state.doc.toString() !== d.base.join('')
    if (touched && !confirm('¿Descartar lo que has fusionado en este archivo?')) return
    oncancel?.()
  }
  function onkeydown(e) {
    if (e.key === 'Escape') (e.preventDefault(), cancel())
    else if (e.altKey && e.key === 'ArrowDown') (e.preventDefault(), go(1))
    else if (e.altKey && e.key === 'ArrowUp') (e.preventDefault(), go(-1))
    else if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') (e.preventDefault(), finish())
  }

  const fill = { conflict: 'rgb(239 68 68 / 0.2)', mod: 'rgb(59 130 246 / 0.18)', add: 'rgb(34 197 94 / 0.2)', del: 'rgb(148 163 184 / 0.3)' }
  const stroke = { conflict: 'rgb(239 68 68 / 0.6)', mod: 'rgb(59 130 246 / 0.55)', add: 'rgb(34 197 94 / 0.6)', del: 'rgb(100 116 139 / 0.55)' }
</script>

<svelte:window {onkeydown} onresize={schedule} />

<div class="fixed inset-0 z-[60] flex flex-col bg-surface" role="dialog" aria-modal="true" aria-label="Fusionar {file.path}">
  <header class="flex shrink-0 items-center gap-3 border-b border-slate-200 px-4 py-2.5">
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="text-primary" aria-hidden="true"><circle cx="6" cy="5" r="2" /><circle cx="6" cy="19" r="2" /><circle cx="18" cy="12" r="2" /><path d="M6 7v10M6 7c0 4 4 5 10 5" /></svg>
    <h2 class="min-w-0 truncate text-[15px] font-semibold">Fusionar <span class="font-mono text-sm font-normal text-slate-600">{file.path}</span></h2>
    <div class="ml-auto flex items-center gap-1.5">
      <button type="button" class="btn-secondary btn-sm !h-8 gap-1.5" onclick={applyNonConflicting} disabled={!pending.some((c) => c.type !== 'conflict')} title="Aplicar todos los cambios que no tienen conflicto">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 4 5 5M3 21 14.5 9.5M18 2v2M22 6h-2M16 8l-2-2" /></svg>
        Aplicar sin conflicto
      </button>
      <button type="button" class="btn-secondary btn-sm !h-8 !w-8 !px-0" onclick={() => go(-1)} disabled={!pending.length} title="Cambio anterior (Alt+↑)" aria-label="Cambio anterior">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="m6 15 6-6 6 6" /></svg>
      </button>
      <button type="button" class="btn-secondary btn-sm !h-8 !w-8 !px-0" onclick={() => go(1)} disabled={!pending.length} title="Cambio siguiente (Alt+↓)" aria-label="Cambio siguiente">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
      </button>
      <span class="ml-2 text-xs text-slate-600">
        {#if pending.length}
          {pending.length} {pending.length === 1 ? 'cambio pendiente' : 'cambios pendientes'}{#if conflicts}, <span class="font-semibold text-red-700">{conflicts} {conflicts === 1 ? 'conflicto' : 'conflictos'}</span>{/if}
        {:else}
          <span class="font-medium text-green-700">Todos los cambios procesados</span>
        {/if}
      </span>
    </div>
  </header>

  <div class="grid shrink-0 grid-cols-[1fr_44px_1fr_44px_1fr] border-b border-slate-200 bg-slate-50 text-xs font-medium text-slate-600">
    <div class="truncate px-3 py-1.5">Tus cambios <span class="font-normal text-slate-400">(local)</span></div>
    <div></div>
    <div class="truncate px-3 py-1.5 text-center">Resultado</div>
    <div></div>
    <div class="truncate px-3 py-1.5 text-right">Del repositorio <span class="font-normal text-slate-400">(origin/{branch})</span></div>
  </div>

  <div class="grid min-h-0 flex-1 grid-cols-[1fr_44px_1fr_44px_1fr]">
    <div bind:this={hostL} class="min-w-0 overflow-hidden"></div>
    <div bind:this={divL} class="relative overflow-hidden border-x border-slate-200 bg-slate-50">
      <svg class="absolute inset-0 size-full" aria-hidden="true">
        {#each geo.left as g (g.i)}
          <path d={band(g, geo.w, false)} fill={g.done ? 'transparent' : fill[g.cls]} stroke={g.done ? 'rgb(148 163 184 / 0.35)' : stroke[g.cls]} stroke-width="1" />
        {/each}
      </svg>
      {#each geo.left as g (g.i)}
        {#if !g.done}
          <div class="absolute left-0.5 flex gap-px" style="top: {Math.max(0, g.a0) + 1}px">
            <button type="button" class="flex size-5 cursor-pointer items-center justify-center rounded bg-surface text-[13px] leading-none font-bold text-primary shadow-sm ring-1 ring-slate-300 hover:bg-primary hover:text-white" title={g.append ? 'Añadir este cambio debajo del ya aplicado' : 'Aplicar este cambio al resultado'} aria-label="Aplicar cambio local" onclick={() => apply(g.i, 'ours')}>»</button>
            <button type="button" class="flex size-5 cursor-pointer items-center justify-center rounded bg-surface text-[13px] leading-none text-slate-500 shadow-sm ring-1 ring-slate-300 hover:bg-slate-200" title="Descartar este cambio" aria-label="Descartar cambio local" onclick={() => ignore(g.i, 'ours')}>×</button>
          </div>
        {/if}
      {/each}
    </div>
    <div bind:this={hostC} class="min-w-0 overflow-hidden"></div>
    <div bind:this={divR} class="relative overflow-hidden border-x border-slate-200 bg-slate-50">
      <svg class="absolute inset-0 size-full" aria-hidden="true">
        {#each geo.right as g (g.i)}
          <path d={band(g, geo.w, true)} fill={g.done ? 'transparent' : fill[g.cls]} stroke={g.done ? 'rgb(148 163 184 / 0.35)' : stroke[g.cls]} stroke-width="1" />
        {/each}
      </svg>
      {#each geo.right as g (g.i)}
        {#if !g.done}
          <div class="absolute right-0.5 flex gap-px" style="top: {Math.max(0, g.a0) + 1}px">
            <button type="button" class="flex size-5 cursor-pointer items-center justify-center rounded bg-surface text-[13px] leading-none text-slate-500 shadow-sm ring-1 ring-slate-300 hover:bg-slate-200" title="Descartar este cambio" aria-label="Descartar cambio del repositorio" onclick={() => ignore(g.i, 'theirs')}>×</button>
            <button type="button" class="flex size-5 cursor-pointer items-center justify-center rounded bg-surface text-[13px] leading-none font-bold text-primary shadow-sm ring-1 ring-slate-300 hover:bg-primary hover:text-white" title={g.append ? 'Añadir este cambio debajo del ya aplicado' : 'Aplicar este cambio al resultado'} aria-label="Aplicar cambio del repositorio" onclick={() => apply(g.i, 'theirs')}>«</button>
          </div>
        {/if}
      {/each}
    </div>
    <div bind:this={hostR} class="min-w-0 overflow-hidden"></div>
  </div>

  <footer class="flex shrink-0 flex-wrap items-center gap-2 border-t border-slate-200 px-4 py-2.5">
    <button type="button" class="btn-secondary btn-sm" onclick={() => onapply?.(file.ours ?? '')} title="Quedarse con tu versión entera">Aceptar la izquierda</button>
    <button type="button" class="btn-secondary btn-sm" onclick={() => onapply?.(file.theirs ?? '')} title="Quedarse con la versión del repositorio entera">Aceptar la derecha</button>
    <span class="ml-2 hidden text-xs text-slate-500 lg:inline">El resultado se puede editar a mano. Alt+↑/↓ recorre los cambios.</span>
    <div class="ml-auto flex gap-2">
      <button type="button" class="btn-secondary btn-sm" onclick={cancel}>Cancelar</button>
      <button type="button" class="btn-primary btn-sm" onclick={finish} title="Ctrl+Enter">Aplicar</button>
    </div>
  </footer>
</div>
