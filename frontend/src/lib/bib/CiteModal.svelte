<script>
  // Diálogo «Insertar cita»: entradas de todos los .bib del proyecto (las
  // añadidas más recientemente primero), búsqueda, varias a la vez, forma de
  // la cita (#cite(form: …)) y localizador (p. 12). Avisa si el .bib no está
  // enlazado en ningún #bibliography(…).
  //
  //   <CiteModal bind:this={m} {projectId} {files} live={() => collab} oninsert={(code) => …} onmanage={(path) => …} />
  //   m.openFor()
  import Modal from '../Modal.svelte'
  import { loadLibrary, linkedBibs, sortItems, filterItems, citeCode, CITE_FORMS } from './library.js'
  import { ENTRY_TYPES } from './bibtex.js'

  let { projectId, files = [], live = () => null, oninsert, onmanage, onaddlink, onclose } = $props()

  let open = $state(false)
  let loading = $state(false)
  let error = $state('')
  let lib = $state({ files: [], items: [], problems: [] })
  let links = $state({ found: true, linked: new Set() })
  let query = $state('')
  let fileFilter = $state('') // '' = todos
  let sort = $state('recent')
  let selected = $state([]) // claves, en el orden en que se marcaron
  let focusKey = $state('')
  let form = $state('normal')
  let supplement = $state('')
  let list = $state()

  export async function openFor() {
    query = ''
    selected = []
    supplement = ''
    form = 'normal'
    error = ''
    open = true
    loading = true
    try {
      const session = live()
      ;[lib, links] = await Promise.all([loadLibrary(projectId, files, session), linkedBibs(projectId, files, session)])
      if (fileFilter && !lib.files.includes(fileFilter)) fileFilter = ''
      focusKey = shown[0]?.key ?? ''
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  const shown = $derived(filterItems(sortItems(projectId, lib.items.filter((x) => !fileFilter || x.file === fileFilter), sort), query))
  const focused = $derived(lib.items.find((x) => x.key === focusKey) ?? null)
  // Claves que se van a citar: las marcadas o, si no hay, la enfocada.
  const keys = $derived(selected.length ? selected : focused ? [focused.key] : [])
  const code = $derived(keys.length ? citeCode(keys, form, keys.length === 1 ? supplement : '') : '')
  const dupKeys = $derived.by(() => {
    const seen = new Map()
    for (const x of lib.items) seen.set(x.key, (seen.get(x.key) ?? 0) + 1)
    return new Set([...seen].filter(([, n]) => n > 1).map(([k]) => k))
  })
  // .bib de las entradas elegidas que no enlaza ningún #bibliography.
  const unlinked = $derived([...new Set(keys.map((k) => lib.items.find((x) => x.key === k)?.file).filter(Boolean))].filter((f) => !links.linked.has(f)))

  function toggle(key) {
    selected = selected.includes(key) ? selected.filter((k) => k !== key) : [...selected, key]
  }
  function insert() {
    if (!code) return
    open = false
    oninsert?.(code)
  }
  function cancel() {
    open = false
    onclose?.()
  }

  function onListKey(e) {
    const i = shown.findIndex((x) => x.key === focusKey)
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const n = Math.min(shown.length - 1, Math.max(0, i + (e.key === 'ArrowDown' ? 1 : -1)))
      if (shown[n]) {
        focusKey = shown[n].key
        list?.querySelector(`[data-key="${CSS.escape(focusKey)}"]`)?.scrollIntoView({ block: 'nearest' })
      }
    } else if (e.key === ' ' && e.target === list) {
      e.preventDefault()
      if (focusKey) toggle(focusKey)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      insert()
    }
  }
  function onSearchKey(e) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter') onListKey(e)
  }
  // Al filtrar, el foco pasa a la primera coincidencia si la enfocada desaparece.
  $effect(() => {
    if (open && shown.length && !shown.some((x) => x.key === focusKey)) focusKey = shown[0].key
  })

  const typeLabel = (t) => ENTRY_TYPES[t]?.label ?? t
  const fmtAdded = (t) => (t ? new Date(t).toLocaleDateString('es', { day: 'numeric', month: 'short', year: 'numeric' }) : '')
</script>

<Modal {open} title="Insertar cita" wide="xl" onclose={cancel}>
  <div class="flex flex-col gap-4">
    {#if error}<div class="alert-error">{error}</div>{/if}
    {#if loading}
      <p class="py-10 text-center text-sm text-slate-500">Leyendo la bibliografía…</p>
    {:else if !lib.files.length}
      <div class="flex flex-col items-center gap-3 rounded-lg border-2 border-dashed border-slate-300 px-6 py-10 text-center">
        <p class="font-semibold">El proyecto no tiene archivos .bib</p>
        <p class="max-w-md text-sm text-slate-500">Crea uno (por ejemplo <code class="font-mono">referencias.bib</code>) desde «Nuevo archivo» o súbelo, y gestiona sus referencias con el editor visual.</p>
        <button type="button" class="btn-primary btn-sm" onclick={() => ((open = false), onmanage?.(null))}>Crear referencias.bib</button>
      </div>
    {:else}
      <div class="flex flex-wrap items-center gap-2">
        <input
          class="input !h-9 min-w-48 flex-1"
          type="search"
          placeholder="Buscar por clave, autor, título, año…"
          bind:value={query}
          onkeydown={onSearchKey}
          aria-label="Buscar referencias"
        />
        <select class="input !h-9 !w-auto" bind:value={sort} aria-label="Ordenar">
          <option value="recent">Añadidas recientemente</option>
          <option value="author">Autor</option>
          <option value="year">Año (más nuevo)</option>
          <option value="title">Título</option>
          <option value="key">Clave</option>
        </select>
      </div>
      {#if lib.files.length > 1}
        <div class="flex flex-wrap gap-1.5" role="group" aria-label="Archivo">
          {#each ['', ...lib.files] as f}
            <button
              type="button"
              class="cursor-pointer rounded-full border px-2.5 py-0.5 font-mono text-xs {fileFilter === f
                ? 'border-primary bg-primary-soft text-primary'
                : 'border-slate-200 text-slate-600 hover:bg-slate-100'}"
              aria-pressed={fileFilter === f}
              onclick={() => (fileFilter = f)}>{f || 'Todos'}</button
            >
          {/each}
        </div>
      {/if}
      {#each lib.problems as p}
        <p class="text-xs text-amber-700"><code class="font-mono">{p.file}</code>: {p.message}</p>
      {/each}

      <div class="grid gap-4 md:grid-cols-[minmax(0,1fr)_17rem]">
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <ul
          bind:this={list}
          class="h-80 overflow-y-auto rounded-lg border border-slate-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
          role="listbox"
          aria-multiselectable="true"
          aria-label="Referencias"
          aria-activedescendant={focusKey ? `cite-${focusKey}` : undefined}
          tabindex="0"
          onkeydown={onListKey}
        >
          {#each shown as x (x.file + '\u0000' + x.key)}
            {@const on = selected.includes(x.key)}
            <!-- svelte-ignore a11y_click_events_have_key_events (el teclado lo maneja la lista: ↑/↓, Espacio, Enter) -->
            <li
              id="cite-{x.key}"
              data-key={x.key}
              role="option"
              aria-selected={on}
              class="flex cursor-pointer items-start gap-2.5 border-b border-slate-100 px-3 py-2 last:border-b-0 {focusKey === x.key ? 'bg-primary-tint' : 'hover:bg-slate-50'}"
              onclick={() => (focusKey = x.key)}
              ondblclick={() => ((focusKey = x.key), (selected = []), insert())}
            >
              <input
                type="checkbox"
                class="mt-1 size-4 shrink-0 accent-violet-700"
                checked={on}
                tabindex="-1"
                aria-label="Marcar {x.key}"
                onclick={(e) => (e.stopPropagation(), toggle(x.key), (focusKey = x.key))}
              />
              <div class="min-w-0 flex-1">
                <p class="truncate text-[13px] font-medium text-slate-900">{x.title || '(sin título)'}</p>
                <p class="truncate text-xs text-slate-500">
                  {x.authors || '—'}{x.year ? ` · ${x.year}` : ''}{x.venue ? ` · ${x.venue}` : ''}
                </p>
              </div>
              <span class="mt-0.5 shrink-0 font-mono text-[11px] {dupKeys.has(x.key) ? 'text-red-700' : 'text-slate-400'}" title={dupKeys.has(x.key) ? 'Clave repetida' : x.file}>@{x.key}</span>
            </li>
          {:else}
            <li class="px-3 py-8 text-center text-sm text-slate-500">{query ? 'Ninguna referencia coincide.' : 'Los .bib no tienen entradas todavía.'}</li>
          {/each}
        </ul>

        <div class="flex min-w-0 flex-col gap-3 text-[13px]">
          {#if focused}
            <div class="rounded-lg border border-slate-200 bg-slate-50 p-3">
              <p class="mb-1 text-[11px] font-semibold tracking-wide text-slate-500 uppercase">{typeLabel(focused.type)}</p>
              <p class="font-semibold text-slate-900">{focused.title || '(sin título)'}</p>
              {#if focused.authors}<p class="text-slate-600">{focused.authors}</p>{/if}
              <p class="text-slate-500">{[focused.venue, focused.year].filter(Boolean).join(' · ')}</p>
              <p class="mt-2 flex flex-wrap gap-x-2 text-xs text-slate-500">
                <code class="font-mono">{focused.file}</code>
                {#if focused.added}<span>añadida el {fmtAdded(focused.added)}</span>{/if}
              </p>
              <button type="button" class="mt-2 text-xs text-primary underline" onclick={() => ((open = false), onmanage?.(focused.file, focused.key))}>Editar en la bibliografía</button>
            </div>
          {/if}
          <label class="flex flex-col gap-1">
            <span class="label">Forma</span>
            <select class="input !h-9" bind:value={form}>
              {#each CITE_FORMS as [v, label, example]}
                <option value={v}>{label} — {example}</option>
              {/each}
            </select>
          </label>
          {#if keys.length <= 1}
            <label class="flex flex-col gap-1">
              <span class="label">Localizador (opcional)</span>
              <input class="input !h-9" bind:value={supplement} placeholder="p. 12, cap. 3…" onkeydown={(e) => e.key === 'Enter' && (e.preventDefault(), insert())} />
            </label>
          {:else}
            <p class="text-xs text-slate-500">{keys.length} referencias marcadas: se citarán juntas.</p>
          {/if}
        </div>
      </div>

      {#if unlinked.length && !loading}
        <div class="alert-info flex flex-wrap items-center gap-2 text-[13px]">
          {#if !links.found}
            <span class="flex-1">Ningún archivo del proyecto llama a <code class="font-mono">#bibliography(…)</code>: la cita dará error al compilar.</span>
            {#if onaddlink}
              <button type="button" class="btn-secondary btn-sm" onclick={() => onaddlink(unlinked[0]) && (links = { found: true, linked: new Set([...links.linked, unlinked[0]]) })}>
                Añadir #bibliography("{unlinked[0].split('/').pop()}") al final
              </button>
            {:else}
              <span class="w-full text-xs">Añádelo en el archivo principal: <code class="font-mono">#bibliography("{unlinked[0]}")</code></span>
            {/if}
          {:else}
            <span class="flex-1"><code class="font-mono">{unlinked.join(', ')}</code> no está en el <code class="font-mono">#bibliography(…)</code> del documento: añádelo (puede recibir una lista de archivos) o la cita dará error.</span>
          {/if}
        </div>
      {/if}

      <div class="flex flex-wrap items-center justify-between gap-2">
        <code class="min-w-0 truncate rounded bg-slate-100 px-2 py-1 font-mono text-xs text-slate-700" title={code}>{code || 'Elige una referencia'}</code>
        <div class="flex gap-2">
          <button type="button" class="btn-secondary btn-sm" onclick={() => ((open = false), onmanage?.(fileFilter || focused?.file || lib.files[0]))}>Gestionar bibliografía</button>
          <button type="button" class="btn-secondary btn-sm" onclick={cancel}>Cancelar</button>
          <button type="button" class="btn-primary btn-sm" disabled={!code} onclick={insert}>Insertar</button>
        </div>
      </div>
      <p class="-mt-2 text-xs text-slate-500">↑/↓ para moverte, Espacio marca varias, Enter inserta. También puedes escribir <code class="font-mono">@</code> en el editor para autocompletar.</p>
    {/if}
  </div>
</Modal>
