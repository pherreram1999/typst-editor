<script>
  // Editor visual de un .bib: lista de entradas (búsqueda, orden por añadidas
  // recientemente…) y formulario de la elegida. Trabaja sobre el mismo
  // documento colaborativo que el editor de código (session: CollabSession):
  // cada cambio del formulario se aplica como una edición mínima del texto
  // (solo el valor del campo tocado), así que llega a los demás usuarios en
  // tiempo real, entra en el historial (Ctrl+Z) y no reformatea el archivo.
  import { onDestroy, untrack } from 'svelte'
  import { undo, redo, undoDepth, redoDepth } from '@codemirror/commands'
  import Modal from '../Modal.svelte'
  import NameList from './NameList.svelte'
  import {
    parseBib, setField, setType, setKey, deleteEntry, formatEntry, appendEntry, nowStamp, suggestKey, balancedValue,
    typeInfo, missingRequired, prepareImport, fetchDoi, latexToText,
    ENTRY_TYPES, FIELD_LABELS, COMMON, META, LONG_FIELDS, NAME_FIELDS,
  } from './bibtex.js'
  import { itemsOf, sortItems, filterItems } from './library.js'

  let { projectId, path, session = null, version = 0, focusKey = '', onshowcode, ontoast } = $props()

  const parsed = $derived.by(() => {
    version
    return parseBib(session?.loaded ? session.text : '')
  })
  const ready = $derived((version, !!session?.loaded))
  const items = $derived(itemsOf(path, parsed.entries))

  let query = $state('')
  let sort = $state(localStorage.getItem('typsteditor.bibsort') ?? 'recent')
  $effect(() => {
    try {
      localStorage.setItem('typsteditor.bibsort', sort)
    } catch {
      /* sin almacenamiento */
    }
  })
  const shown = $derived(filterItems(sortItems(projectId, items, sort), query))

  const keyCount = $derived.by(() => {
    const m = new Map()
    for (const e of parsed.entries) m.set(e.key, (m.get(e.key) ?? 0) + 1)
    return m
  })
  const takenKeys = () => new Set(parsed.entries.map((e) => e.key))

  // ---------- selección ----------
  // Posición (seguida por la sesión a través de todas las ediciones) del
  // inicio de la entrada elegida, más su clave por si la posición se pierde.
  let sel = $state.raw(null)
  let selKey = ''
  function findEntry(p) {
    if (!sel) return null
    return p.entries.find((e) => e.from === sel.pos) ?? p.entries.find((e) => e.key === selKey) ?? null
  }
  const selected = $derived.by(() => {
    version
    return findEntry(parsed)
  })

  function select(entry, focusFirst = false) {
    flushAll()
    if (sel) session?.untrack(sel)
    sel = entry && session ? session.track(entry.from) : null
    selKey = entry?.key ?? ''
    autoKey = false
    if (focusFirst) queueMicrotask(() => form?.querySelector('input:not([disabled]), textarea')?.focus())
  }
  // Primera selección (o la pedida desde «Insertar cita»).
  let wanted = untrack(() => focusKey)
  $effect(() => {
    const list = shown
    if (!ready) return
    untrack(() => {
      if (wanted) {
        const e = parsed.entries.find((x) => x.key === wanted)
        wanted = ''
        if (e) return select(e)
      }
      if (!selected && list.length && !sel) select(list[0].entry)
    })
  })
  export function reveal(key) {
    const e = parsed.entries.find((x) => x.key === key)
    if (e) select(e)
  }

  // ---------- borrador del formulario ----------
  let draft = $state({ type: '', key: '', fields: {} })
  let draftFor = null
  let added = $state([]) // campos añadidos a mano (visibles aunque estén vacíos)
  let errors = $state({})
  let focused = null
  const pending = new Map() // campo -> temporizador
  let autoKey = false // entrada recién creada: la clave sigue a autor/año/título

  const valueOf = (f) => (f.complex ? (f.display ?? f.value) : f.value)

  $effect(() => {
    const e = selected
    untrack(() => sync(e))
  })
  function sync(e) {
    if (!e) {
      draftFor = null
      return
    }
    const fresh = draftFor !== sel
    const fields = {}
    for (const f of e.fields) fields[f.name.toLowerCase()] = valueOf(f)
    if (fresh) {
      draftFor = sel
      draft = { type: e.type.toLowerCase(), key: e.key, fields }
      added = []
      errors = {}
      return
    }
    // Lo que está escribiendo el usuario manda sobre lo que llega del documento.
    const busy = (n) => pending.has(n) || focused === n
    if (!busy('#type')) draft.type = e.type.toLowerCase()
    if (!busy('#key')) draft.key = e.key
    for (const n of new Set([...Object.keys(fields), ...Object.keys(draft.fields)])) {
      if (!busy(n)) draft.fields[n] = fields[n] ?? ''
    }
    selKey = e.key
  }

  function change(name, value, delay = 450) {
    if (name === '#key') {
      draft.key = value
      autoKey = false
    } else if (name === '#type') draft.type = value
    else draft.fields[name] = value
    clearTimeout(pending.get(name))
    pending.set(name, setTimeout(() => flush(name), delay))
  }
  function flushAll() {
    for (const n of [...pending.keys()]) flush(n)
  }

  const KEY_OK = /^[^\s,{}()"#%'=\\]+$/
  function flush(name) {
    clearTimeout(pending.get(name))
    pending.delete(name)
    if (!session?.loaded) return
    const text = session.text
    const e = findEntry(parseBib(text))
    if (!e) return
    let changes = []
    if (name === '#type') changes = setType(e, draft.type)
    else if (name === '#key') {
      const k = draft.key.trim()
      if (!k) return void (errors = { ...errors, '#key': 'La clave no puede estar vacía' })
      if (!KEY_OK.test(k)) return void (errors = { ...errors, '#key': 'Sin espacios ni , { } ( ) " # % \' = \\' })
      if (k !== e.key && takenKeys().has(k)) return void (errors = { ...errors, '#key': 'Ya hay otra entrada con esa clave' })
      errors = { ...errors, '#key': '' }
      changes = setKey(e, k)
      selKey = k
    } else {
      const v = draft.fields[name] ?? ''
      if (!balancedValue(v)) return void (errors = { ...errors, [name]: 'Las llaves { } no están equilibradas: no se guardó' })
      errors = { ...errors, [name]: '' }
      changes = setField(text, e, name, v.replace(/\r?\n\s*\n/g, '\n'))
      // Entrada nueva: la clave se va formando con autor, año y título.
      if (autoKey && ['author', 'editor', 'year', 'date', 'title'].includes(name)) {
        const k = suggestKey({ ...draft.fields }, new Set([...takenKeys()].filter((x) => x !== e.key)))
        if (k !== e.key) {
          changes = [...changes, ...setKey(e, k)]
          selKey = k
          draft.key = k
        }
      }
    }
    session.applyChanges(changes)
  }

  onDestroy(() => {
    flushAll()
    if (sel) session?.untrack(sel)
  })

  // ---------- campos visibles ----------
  const info = $derived(typeInfo(draft.type || 'misc'))
  const typeOptional = $derived((ENTRY_TYPES[draft.type]?.optional ?? ['author', 'year']).filter((n) => !info.required.includes(n)))
  const has = (n) => !!(draft.fields[n] ?? '').trim()
  const visibleCommon = $derived(COMMON.filter((n) => has(n) || added.includes(n)))
  const others = $derived(
    Object.keys(draft.fields).filter(
      (n) => (has(n) || added.includes(n)) && !info.required.includes(n) && !typeOptional.includes(n) && !COMMON.includes(n) && !META.has(n),
    ),
  )
  const metaFields = $derived(Object.keys(draft.fields).filter((n) => META.has(n) && has(n)))
  const missing = $derived(new Set(missingRequired(draft.type || 'misc', has)))
  const complexNames = $derived(new Set((selected?.fields ?? []).filter((f) => f.complex).map((f) => f.name.toLowerCase())))
  const addable = $derived(
    [...COMMON, ...Object.keys(FIELD_LABELS)].filter(
      (n, i, a) => a.indexOf(n) === i && !META.has(n) && !info.required.includes(n) && !typeOptional.includes(n) && !visibleCommon.includes(n) && !others.includes(n),
    ),
  )
  let newField = $state('')
  function addField(name) {
    const n = name.trim().toLowerCase()
    if (!n || !/^[a-z][a-z0-9_:-]*$/.test(n)) return
    if (!added.includes(n)) added = [...added, n]
    draft.fields[n] ??= ''
    newField = ''
    queueMicrotask(() => form?.querySelector(`[data-field="${n}"]`)?.focus())
  }

  const entryMissing = (e) => missingRequired(e.type, (n) => e.fields.some((f) => f.name.toLowerCase() === n && f.value.trim()))
  const label = (n) => FIELD_LABELS[n] ?? n

  // ---------- acciones ----------
  let typeMenu = $state(false)
  function create(type) {
    typeMenu = false
    if (!session?.loaded) return
    flushAll()
    const key = suggestKey({}, takenKeys())
    const ch = appendEntry(session.text, formatEntry(type, key, [['creationdate', nowStamp()]]))
    session.applyChanges([{ from: ch.from, to: ch.to, insert: ch.insert }])
    if (sel) session.untrack(sel)
    sel = session.track(ch.start)
    selKey = key
    draftFor = null
    autoKey = true
    query = ''
    queueMicrotask(() => form?.querySelector('input:not([disabled]), textarea')?.focus())
  }

  function duplicate() {
    const e = selected
    if (!e) return
    flushAll()
    const text = session.text
    let snippet = text.slice(e.from, e.to)
    const p = parseBib(snippet).entries[0]
    if (!p) return
    let key = `${e.key}-2`
    for (let k = 3; takenKeys().has(key); k++) key = `${e.key}-${k}`
    const ch = [...setKey(p, key), ...setField(snippet, p, 'creationdate', nowStamp())].sort((a, b) => b.from - a.from)
    for (const c of ch) snippet = snippet.slice(0, c.from) + c.insert + snippet.slice(c.to)
    const a = appendEntry(text, snippet)
    session.applyChanges([{ from: a.from, to: a.to, insert: a.insert }])
    if (sel) session.untrack(sel)
    sel = session.track(a.start)
    selKey = key
    draftFor = null
    ontoast?.(`Copia creada: ${key}`)
  }

  function remove() {
    const e = selected
    if (!e || !confirm(`¿Eliminar la referencia «${e.key}»? Las citas @${e.key} del documento dejarán de funcionar.`)) return
    pending.forEach((t) => clearTimeout(t))
    pending.clear()
    const i = shown.findIndex((x) => x.entry.from === e.from)
    const next = shown[i + 1]?.entry ?? shown[i - 1]?.entry ?? null
    const nextKey = next?.key
    session.applyChanges(deleteEntry(session.text, e))
    if (sel) session.untrack(sel)
    sel = null
    const fresh = parseBib(session.text).entries.find((x) => x.key === nextKey)
    if (fresh) select(fresh)
  }

  const canUndo = $derived((version, session?.view ? undoDepth(session.view.state) > 0 : false))
  const canRedo = $derived((version, session?.view ? redoDepth(session.view.state) > 0 : false))
  const runUndo = () => (flushAll(), session?.view && undo(session.view))
  const runRedo = () => session?.view && redo(session.view)

  // ---------- DOI del formulario: rellenar campos vacíos ----------
  let doiBusy = $state(false)
  async function fillFromDoi() {
    const doi = draft.fields.doi ?? ''
    if (!doi.trim()) return
    doiBusy = true
    try {
      const src = await fetchDoi(doi)
      const e = parseBib(src).entries[0]
      if (!e) throw new Error('La respuesta no contiene una entrada BibTeX')
      let n = 0
      for (const f of e.fields) {
        const name = f.name.toLowerCase()
        if (META.has(name) || has(name)) continue
        draft.fields[name] = f.complex ? (f.display ?? f.value) : f.value
        flush(name)
        n++
      }
      if (draft.type === 'misc' && ENTRY_TYPES[e.type.toLowerCase()]) change('#type', e.type.toLowerCase(), 0)
      ontoast?.(n ? `Se completaron ${n} campo(s) desde el DOI` : 'No había campos vacíos que completar')
    } catch (err) {
      ontoast?.(err.message, 'error')
    } finally {
      doiBusy = false
    }
  }

  // ---------- importar ----------
  let importOpen = $state(false)
  let importTab = $state('paste') // paste | doi
  let importText = $state('')
  let importDoi = $state('')
  let importError = $state('')
  let importBusy = $state(false)
  const importPreview = $derived(importTab === 'paste' && importText.trim() ? prepareImport(importText, takenKeys()) : null)

  function openImport() {
    importText = ''
    importDoi = ''
    importError = ''
    importOpen = true
  }
  async function doImport(e) {
    e?.preventDefault()
    importError = ''
    if (!session?.loaded) return
    let prep
    if (importTab === 'doi') {
      importBusy = true
      try {
        prep = prepareImport(await fetchDoi(importDoi), takenKeys(), { keyFromFields: true })
      } catch (err) {
        importError = err.message
        return
      } finally {
        importBusy = false
      }
    } else prep = prepareImport(importText, takenKeys())
    if (!prep.count) {
      importError = prep.errors[0] ? `No se reconoció ninguna entrada: ${prep.errors[0].message} (línea ${prep.errors[0].line})` : 'No se reconoció ninguna entrada BibTeX'
      return
    }
    flushAll()
    const a = appendEntry(session.text, prep.text)
    session.applyChanges([{ from: a.from, to: a.to, insert: a.insert }])
    importOpen = false
    query = ''
    const first = parseBib(session.text).entries.find((x) => x.key === prep.keys[0])
    if (first) select(first)
    ontoast?.(prep.count === 1 ? `Referencia importada: ${prep.keys[0]}` : `${prep.count} referencias importadas`)
  }

  let form = $state()
  const SORT_LABELS = { recent: 'Añadidas recientemente', file: 'Orden del archivo', author: 'Autor', year: 'Año', title: 'Título', key: 'Clave' }
  const hasLatex = (v) => /\\[a-zA-Z'"`^~]/.test(v ?? '')
  const inputType = (n) => (n === 'urldate' ? 'date' : n === 'url' ? 'url' : 'text')
  const urlOf = (n, v) => (n === 'doi' ? `https://doi.org/${v.replace(/^(https?:\/\/(dx\.)?doi\.org\/|doi:\s*)/i, '')}` : v)
</script>

{#snippet fieldRow(n, required = false)}
  {@const v = draft.fields[n] ?? ''}
  <div class="flex flex-col gap-1">
    <div class="flex items-center gap-2">
      <label for="bib-{n}" class="label">
        {label(n)}{#if required}<span class="text-red-600" title="Obligatorio">*</span>{/if}
        {#if label(n) !== n}<span class="ml-1 font-mono text-[11px] font-normal text-slate-400">{n}</span>{/if}
      </label>
      {#if (n === 'url' || n === 'doi') && v.trim()}
        <a href={urlOf(n, v.trim())} target="_blank" rel="noopener noreferrer" class="text-xs text-primary">abrir ↗</a>
      {/if}
      {#if n === 'doi' && v.trim()}
        <button type="button" class="text-xs text-primary underline disabled:opacity-50" disabled={doiBusy} onclick={fillFromDoi} title="Consulta doi.org y completa los campos vacíos">
          {doiBusy ? 'Consultando…' : 'Completar campos vacíos'}
        </button>
      {/if}
      {#if !required && !info.required.includes(n) && !typeOptional.includes(n)}
        <button
          type="button"
          class="ml-auto text-xs text-slate-400 hover:text-red-700"
          onclick={() => {
            added = added.filter((x) => x !== n)
            change(n, '', 0)
          }}>Quitar</button
        >
      {/if}
    </div>
    {#if NAME_FIELDS.has(n)}
      <NameList
        value={v}
        label={label(n)}
        onchange={(val) => change(n, val)}
        onfocus={() => (focused = n)}
        onblur={() => ((focused = null), flush(n))}
      />
    {:else if LONG_FIELDS.has(n)}
      <textarea
        id="bib-{n}"
        data-field={n}
        class="input !h-auto py-2 !text-[13px] leading-snug"
        rows={n === 'abstract' ? 5 : 2}
        value={v}
        oninput={(e) => change(n, e.currentTarget.value)}
        onfocus={() => (focused = n)}
        onblur={() => ((focused = null), pending.has(n) && flush(n))}
      ></textarea>
    {:else}
      <input
        id="bib-{n}"
        data-field={n}
        type={inputType(n)}
        class="input !h-9 !text-[13px] {n === 'doi' || n === 'url' || n === 'isbn' || n === 'pages' ? 'font-mono' : ''}"
        inputmode={n === 'year' || n === 'volume' || n === 'number' ? 'numeric' : undefined}
        value={v}
        placeholder={n === 'pages' ? '12--34' : n === 'year' ? '2026' : n === 'doi' ? '10.1000/xyz123' : ''}
        oninput={(e) => change(n, e.currentTarget.value, n === 'urldate' ? 0 : 450)}
        onfocus={() => (focused = n)}
        onblur={() => ((focused = null), pending.has(n) && flush(n))}
      />
    {/if}
    {#if errors[n]}<p class="text-xs text-red-700">{errors[n]}</p>{/if}
    {#if complexNames.has(n) && !pending.has(n)}
      <p class="text-[11px] text-slate-400">Usa macros (@string o meses); si lo editas se guardará como texto.</p>
    {/if}
    {#if hasLatex(v)}
      <p class="text-[11px] text-slate-500">Se verá: {latexToText(v)}</p>
    {/if}
  </div>
{/snippet}

<div class="@container flex h-full min-h-0 flex-col bg-surface">
  <!-- Barra -->
  <div class="flex shrink-0 flex-wrap items-center gap-2 border-b border-slate-200 px-3 py-2">
    <div class="relative">
      <button type="button" class="btn-primary btn-sm !h-8" disabled={!ready} onclick={() => (typeMenu = !typeMenu)} aria-haspopup="menu" aria-expanded={typeMenu}>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
        Nueva
      </button>
      {#if typeMenu}
        <div class="fixed inset-0 z-30" role="presentation" onclick={() => (typeMenu = false)}></div>
        <div role="menu" class="absolute top-9 left-0 z-40 flex w-64 flex-col rounded-lg border border-slate-200 bg-surface p-1 shadow-lg">
          {#each Object.entries(ENTRY_TYPES) as [t, ti]}
            <button type="button" role="menuitem" class="flex h-8 cursor-pointer items-center justify-between gap-2 rounded-md px-2 text-left text-[13px] text-slate-800 hover:bg-slate-100" onclick={() => create(t)}>
              <span>{ti.label}</span><span class="font-mono text-[11px] text-slate-400">@{t}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>
    <button type="button" class="btn-secondary btn-sm !h-8" disabled={!ready} onclick={openImport} title="Pegar BibTeX (Google Scholar, Zotero…) o buscar por DOI">Importar</button>
    <input class="input !h-8 min-w-36 flex-1 !text-[13px]" type="search" placeholder="Buscar…" bind:value={query} aria-label="Buscar referencias" />
    <select class="input !h-8 !w-auto !text-[13px]" bind:value={sort} aria-label="Ordenar">
      {#each Object.entries(SORT_LABELS) as [k, l]}<option value={k}>{l}</option>{/each}
    </select>
    <span class="text-xs whitespace-nowrap text-slate-500">{shown.length === items.length ? items.length : `${shown.length} de ${items.length}`} ref.</span>
    <div class="flex">
      <button type="button" class="flex size-8 cursor-pointer items-center justify-center rounded-md text-slate-600 hover:bg-slate-200 disabled:opacity-40" disabled={!canUndo} onclick={runUndo} title="Deshacer" aria-label="Deshacer">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 14 4 9l5-5" /><path d="M4 9h11a5 5 0 0 1 0 10h-3" /></svg>
      </button>
      <button type="button" class="flex size-8 cursor-pointer items-center justify-center rounded-md text-slate-600 hover:bg-slate-200 disabled:opacity-40" disabled={!canRedo} onclick={runRedo} title="Rehacer" aria-label="Rehacer">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 14 5-5-5-5" /><path d="M20 9H9a5 5 0 0 0 0 10h3" /></svg>
      </button>
    </div>
  </div>

  {#if parsed.errors.length}
    <div class="flex shrink-0 flex-wrap items-center gap-2 border-b border-amber-200 bg-amber-50 px-3 py-2 text-[13px] text-amber-800">
      <span class="flex-1">
        {parsed.errors.length === 1 ? 'Hay un error de sintaxis' : `Hay ${parsed.errors.length} errores de sintaxis`}: {parsed.errors[0].message} (línea {parsed.errors[0].line}). Las entradas afectadas no aparecen aquí.
      </span>
      <button type="button" class="btn-secondary btn-sm !h-7" onclick={() => onshowcode?.(parsed.errors[0].line)}>Ver en el código</button>
    </div>
  {/if}

  {#if !ready}
    <p class="flex flex-1 items-center justify-center text-sm text-slate-500">Cargando…</p>
  {:else}
    <div class="flex min-h-0 flex-1 flex-col @2xl:flex-row">
      <!-- Lista -->
      <ul class="max-h-[38%] shrink-0 overflow-y-auto border-b border-slate-200 @2xl:max-h-none @2xl:w-80 @2xl:border-r @2xl:border-b-0" aria-label="Referencias">
        {#each shown as x (x.entry.from)}
          {@const miss = entryMissing(x.entry)}
          {@const dup = (keyCount.get(x.key) ?? 0) > 1}
          {@const on = selected?.from === x.entry.from}
          <li>
            <button
              type="button"
              class="flex w-full cursor-pointer flex-col gap-0.5 border-b border-slate-100 px-3 py-2 text-left {on ? 'bg-primary-tint' : 'hover:bg-slate-50'}"
              aria-current={on}
              onclick={() => select(x.entry)}
            >
              <span class="flex w-full items-center gap-1.5">
                <span class="min-w-0 flex-1 truncate text-[13px] font-medium text-slate-900">{x.title || '(sin título)'}</span>
                {#if dup}<span class="size-2 shrink-0 rounded-full bg-red-500" title="Clave repetida"></span>
                {:else if miss.length}<span class="size-2 shrink-0 rounded-full bg-amber-500" title="Faltan: {miss.map(label).join(', ')}"></span>{/if}
              </span>
              <span class="truncate text-xs text-slate-500">{x.authors || '—'}{x.year ? ` · ${x.year}` : ''}</span>
              <span class="truncate font-mono text-[11px] {dup ? 'text-red-700' : 'text-slate-400'}">@{x.key} · {ENTRY_TYPES[x.type]?.label ?? x.type}</span>
            </button>
          </li>
        {:else}
          <li class="px-4 py-10 text-center text-sm text-slate-500">
            {#if query}Ninguna referencia coincide con «{query}».{:else}Este archivo no tiene referencias. Crea una con «Nueva» o pégalas con «Importar».{/if}
          </li>
        {/each}
      </ul>

      <!-- Formulario -->
      <div class="min-h-0 min-w-0 flex-1 overflow-y-auto" bind:this={form}>
        {#if selected}
          <div class="mx-auto flex max-w-3xl flex-col gap-4 p-4">
            <div class="grid gap-3 @lg:grid-cols-2">
              <div class="flex flex-col gap-1">
                <label for="bib-type" class="label">Tipo</label>
                <select id="bib-type" class="input !h-9 !text-[13px]" value={draft.type} onchange={(e) => change('#type', e.currentTarget.value, 0)}>
                  {#each Object.entries(ENTRY_TYPES) as [t, ti]}<option value={t}>{ti.label} (@{t})</option>{/each}
                  {#if !ENTRY_TYPES[draft.type]}<option value={draft.type}>@{draft.type}</option>{/if}
                </select>
              </div>
              <div class="flex flex-col gap-1">
                <div class="flex items-center gap-2">
                  <label for="bib-key" class="label">Clave para citar</label>
                  <button
                    type="button"
                    class="ml-auto text-xs text-primary underline"
                    title="A partir del primer autor, el año y el título"
                    onclick={() => change('#key', suggestKey(draft.fields, new Set([...takenKeys()].filter((k) => k !== selected.key))), 0)}>Generar</button
                  >
                </div>
                <input
                  id="bib-key"
                  class="input !h-9 font-mono !text-[13px] {errors['#key'] || (keyCount.get(draft.key) ?? 0) > 1 ? '!border-red-400' : ''}"
                  value={draft.key}
                  oninput={(e) => change('#key', e.currentTarget.value, 700)}
                  onfocus={() => (focused = '#key')}
                  onblur={() => ((focused = null), pending.has('#key') && flush('#key'))}
                  spellcheck="false"
                />
                {#if errors['#key']}<p class="text-xs text-red-700">{errors['#key']}</p>
                {:else if (keyCount.get(draft.key) ?? 0) > 1}<p class="text-xs text-red-700">Hay otra entrada con esta clave: cámbiala o borra la repetida.</p>
                {:else}<p class="text-[11px] text-slate-500">Se cita como <code class="font-mono">@{draft.key}</code>. Si la cambias, actualiza las citas que ya existan.</p>{/if}
              </div>
            </div>

            {#if missing.size}
              <p class="rounded-md bg-amber-50 px-3 py-1.5 text-xs text-amber-800">Faltan campos obligatorios para este tipo: {[...missing].map(label).join(', ')}.</p>
            {/if}

            <fieldset class="flex flex-col gap-3">
              <legend class="mb-2 text-[11px] font-semibold tracking-wide text-slate-500 uppercase">Datos principales</legend>
              {#each info.required as n (n)}{@render fieldRow(n, true)}{/each}
            </fieldset>
            <fieldset class="grid gap-3 @lg:grid-cols-2">
              <legend class="mb-2 text-[11px] font-semibold tracking-wide text-slate-500 uppercase">Opcionales</legend>
              {#each typeOptional as n (n)}
                <div class={NAME_FIELDS.has(n) || LONG_FIELDS.has(n) ? '@lg:col-span-2' : ''}>{@render fieldRow(n)}</div>
              {/each}
              {#each [...visibleCommon, ...others] as n (n)}
                <div class={NAME_FIELDS.has(n) || LONG_FIELDS.has(n) ? '@lg:col-span-2' : ''}>{@render fieldRow(n)}</div>
              {/each}
            </fieldset>

            <div class="flex flex-wrap items-center gap-2">
              <select class="input !h-8 !w-auto !text-[13px]" value="" onchange={(e) => (addField(e.currentTarget.value), (e.currentTarget.value = ''))} aria-label="Añadir campo">
                <option value="" disabled>+ Añadir campo…</option>
                {#each addable as n}<option value={n}>{label(n)} ({n})</option>{/each}
              </select>
              <input class="input !h-8 !w-40 font-mono !text-[13px]" placeholder="otro campo" bind:value={newField} onkeydown={(e) => e.key === 'Enter' && (e.preventDefault(), addField(newField))} aria-label="Nombre de otro campo" />
              <button type="button" class="btn-secondary btn-sm !h-8" disabled={!newField.trim()} onclick={() => addField(newField)}>Añadir</button>
            </div>

            <div class="flex flex-wrap items-center gap-2 border-t border-slate-200 pt-3">
              {#if metaFields.length}
                <span class="text-xs text-slate-500">{metaFields.map((n) => `${label(n)}: ${draft.fields[n].replace('T', ' ')}`).join(' · ')}</span>
              {/if}
              <span class="flex-1"></span>
              <button type="button" class="btn-secondary btn-sm !h-8" onclick={() => onshowcode?.(session.view.state.doc.lineAt(selected.from).number)}>Ver en el código</button>
              <button type="button" class="btn-secondary btn-sm !h-8" onclick={duplicate}>Duplicar</button>
              <button type="button" class="btn-secondary btn-sm !h-8 !border-red-200 !text-red-700 hover:!bg-red-50" onclick={remove}>Eliminar</button>
            </div>
          </div>
        {:else}
          <p class="flex h-full items-center justify-center p-6 text-center text-sm text-slate-500">
            {items.length ? 'Elige una referencia de la lista.' : 'Sin referencias todavía.'}
          </p>
        {/if}
      </div>
    </div>
  {/if}
</div>

<Modal open={importOpen} title="Importar referencias" wide onclose={() => !importBusy && (importOpen = false)}>
  <form class="flex flex-col gap-4" onsubmit={doImport}>
    <div class="flex gap-1 border-b border-slate-200" role="tablist">
      {#each [['paste', 'Pegar BibTeX'], ['doi', 'Desde un DOI']] as [k, l]}
        <button
          type="button"
          role="tab"
          aria-selected={importTab === k}
          class="-mb-px cursor-pointer border-b-2 px-3 py-2 text-sm font-medium {importTab === k ? 'border-primary text-primary' : 'border-transparent text-slate-500 hover:text-slate-900'}"
          onclick={() => ((importTab = k), (importError = ''))}>{l}</button
        >
      {/each}
    </div>
    {#if importError}<div class="alert-error">{importError}</div>{/if}
    {#if importTab === 'paste'}
      <label class="flex flex-col gap-1.5">
        <span class="label">BibTeX (de Google Scholar, Zotero, Mendeley, la web de la revista…)</span>
        <textarea class="input !h-56 py-2 font-mono !text-xs" bind:value={importText} placeholder={'@article{clave,\n  author = {Apellido, Nombre},\n  title  = {Título},\n  year   = {2024},\n}'} spellcheck="false"></textarea>
      </label>
      {#if importPreview}
        <p class="text-xs text-slate-500">
          {importPreview.count === 1 ? 'Se añadirá 1 referencia' : `Se añadirán ${importPreview.count} referencias`}{importPreview.count ? `: ${importPreview.keys.slice(0, 6).join(', ')}${importPreview.count > 6 ? '…' : ''}` : ''}.
          {#if importPreview.errors.length}<span class="text-amber-700">{importPreview.errors.length} fragmento(s) no se reconocieron.</span>{/if}
        </p>
      {/if}
    {:else}
      <label class="flex flex-col gap-1.5">
        <span class="label">DOI</span>
        <input class="input font-mono" bind:value={importDoi} placeholder="10.1038/nature12373 o https://doi.org/…" required />
        <span class="text-xs text-slate-500">Se consulta doi.org (o Crossref) desde tu navegador.</span>
      </label>
    {/if}
    <div class="flex justify-end gap-2">
      <button type="button" class="btn-secondary" onclick={() => (importOpen = false)} disabled={importBusy}>Cancelar</button>
      <button class="btn-primary" disabled={importBusy || (importTab === 'paste' ? !importPreview?.count : !importDoi.trim())}>{importBusy ? 'Consultando…' : 'Añadir al archivo'}</button>
    </div>
  </form>
</Modal>
