<script>
  // Explorador de archivos en árbol: carpetas plegables (el estado se guarda
  // por proyecto en localStorage), búsqueda por nombre o ruta (sin distinguir
  // mayúsculas ni acentos; Enter abre el primer resultado, Esc limpia) e
  // iconos según el tipo de archivo.
  let { projectId, files, currentPath, mainFile, peopleIn = () => [], onopen, onremove, onreplace, ondropfiles } = $props()

  const KEY = `typsteditor.tree.${projectId}`
  function loadCollapsed() {
    try {
      return new Set(JSON.parse(localStorage.getItem(KEY)) ?? [])
    } catch {
      return new Set()
    }
  }
  let collapsed = $state(loadCollapsed()) // rutas de carpetas plegadas
  let query = $state('')

  function save() {
    try {
      localStorage.setItem(KEY, JSON.stringify([...collapsed]))
    } catch {}
  }
  function toggle(path) {
    const next = new Set(collapsed)
    if (next.has(path)) next.delete(path)
    else next.add(path)
    collapsed = next
    save()
  }

  // Comparación sin mayúsculas ni acentos que conserva la longitud (para resaltar).
  const fold = (s) => [...s].map((c) => c.normalize('NFD')[0].toLowerCase()).join('')
  const q = $derived(fold(query.trim()))
  const matches = $derived(q ? files.filter((f) => fold(f.path).includes(q)) : files)

  // Árbol a partir de las rutas: carpetas primero y orden alfabético.
  const collator = new Intl.Collator('es', { numeric: true, sensitivity: 'base' })
  const tree = $derived.by(() => {
    const root = { name: '', path: '', dirs: new Map(), files: [] }
    for (const f of matches) {
      const parts = f.path.split('/')
      let node = root
      for (let i = 0; i < parts.length - 1; i++) {
        const path = parts.slice(0, i + 1).join('/')
        if (!node.dirs.has(parts[i])) node.dirs.set(parts[i], { name: parts[i], path, dirs: new Map(), files: [] })
        node = node.dirs.get(parts[i])
      }
      node.files.push({ ...f, name: parts.at(-1) })
    }
    const sort = (n) => ({
      ...n,
      dirs: [...n.dirs.values()].sort((a, b) => collator.compare(a.name, b.name)).map(sort),
      files: n.files.sort((a, b) => collator.compare(a.name, b.name)),
    })
    return sort(root)
  })
  const dirCount = $derived.by(() => {
    const dirs = new Set()
    for (const f of files) {
      const parts = f.path.split('/')
      for (let i = 1; i < parts.length; i++) dirs.add(parts.slice(0, i).join('/'))
    }
    return dirs.size
  })
  const isOpen = (path) => !!q || !collapsed.has(path)

  // Al abrir un archivo (p. ej. desde un diagnóstico) se despliegan sus carpetas.
  $effect(() => {
    const parts = (currentPath ?? '').split('/')
    const anc = parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join('/'))
    if (anc.some((a) => collapsed.has(a))) {
      collapsed = new Set([...collapsed].filter((c) => !anc.includes(c)))
      save()
    }
  })

  function collapseAll() {
    const all = new Set()
    for (const f of files) {
      const parts = f.path.split('/')
      for (let i = 1; i < parts.length; i++) all.add(parts.slice(0, i).join('/'))
    }
    collapsed = all
    save()
  }
  function expandAll() {
    collapsed = new Set()
    save()
  }

  // Divide un nombre en [antes, coincidencia, después] para resaltar la búsqueda.
  function highlight(name) {
    if (!q) return [name, '', '']
    const i = fold(name).indexOf(q)
    if (i < 0) return [name, '', '']
    return [name.slice(0, i), name.slice(i, i + q.length), name.slice(i + q.length)]
  }

  function onKey(e) {
    if (e.key === 'Escape') query = ''
    else if (e.key === 'Enter' && matches.length) onopen(matches.find((f) => f.editable) ?? matches[0])
  }

  // ---------- soltar archivos ----------
  // La carpeta de destino es la del elemento bajo el cursor (data-dir): una
  // carpeta, la carpeta de un archivo o la raíz. Se resalta mientras se
  // arrastra; las carpetas plegadas se abren al mantener el cursor encima.
  let dropDir = $state(null)
  let expandTimer
  let expandFor = null
  const hasFiles = (e) => [...(e.dataTransfer?.types ?? [])].includes('Files')

  function onDragOver(e) {
    if (!ondropfiles || !hasFiles(e)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'copy'
    const el = e.target.closest?.('[data-dir]')
    const d = el ? el.dataset.dir : ''
    dropDir = d
    const folder = el?.dataset.folder
    if (folder !== expandFor) {
      clearTimeout(expandTimer)
      expandFor = folder
      if (folder && !isOpen(folder)) expandTimer = setTimeout(() => !isOpen(folder) && toggle(folder), 700)
    }
  }
  function endDrag() {
    dropDir = null
    expandFor = null
    clearTimeout(expandTimer)
  }
  function onDragLeave(e) {
    if (!e.currentTarget.contains(e.relatedTarget)) endDrag()
  }
  function onDrop(e) {
    if (!ondropfiles || !hasFiles(e)) return
    e.preventDefault()
    const d = dropDir ?? ''
    endDrag()
    const list = [...e.dataTransfer.files]
    if (list.length) ondropfiles(list, d)
  }
  const parentOf = (p) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '')

  const fmtSize = (n) => (n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(0)} KB` : `${(n / 1048576).toFixed(1)} MB`)

  function kind(name) {
    const ext = name.includes('.') ? name.split('.').pop().toLowerCase() : ''
    if (ext === 'typ') return 'typ'
    if (ext === 'bib') return 'bib'
    if (['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp'].includes(ext)) return 'image'
    if (['ttf', 'otf', 'woff', 'woff2'].includes(ext)) return 'font'
    if (['csv', 'json', 'yaml', 'yml', 'toml', 'xml', 'txt', 'md'].includes(ext)) return 'data'
    return 'file'
  }
</script>

{#snippet fileIcon(name)}
  {@const k = kind(name)}
  <svg class="size-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    {#if k === 'image'}
      <rect x="3" y="4" width="18" height="16" rx="2" class="text-emerald-600" stroke="currentColor" />
      <circle cx="9" cy="10" r="1.6" class="text-emerald-600" stroke="currentColor" />
      <path d="m21 16-5-5-9 9" class="text-emerald-600" stroke="currentColor" />
    {:else}
      <path
        d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"
        class={k === 'typ' ? 'text-sky-700' : k === 'bib' ? 'text-violet-600' : k === 'font' ? 'text-rose-600' : 'text-slate-400'}
        stroke="currentColor"
      />
      <path d="M14 3v5h5" class="text-slate-400" stroke="currentColor" />
      {#if k === 'typ'}
        <path d="M9 12h6M12 12v6" class="text-sky-700" stroke="currentColor" stroke-width="2" />
      {:else if k === 'bib'}
        <path d="M9 12h6M9 15h6M9 18h3" class="text-violet-600" stroke="currentColor" />
      {:else if k === 'data'}
        <path d="M9 13h6M9 17h6" class="text-slate-400" stroke="currentColor" />
      {/if}
    {/if}
  </svg>
{/snippet}

{#snippet folderIcon(open)}
  <svg class="size-4 shrink-0 text-amber-500" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    {#if open}
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v1H7.2a2 2 0 0 0-1.9 1.4L3 18z" opacity=".75" />
      <path d="M5.3 11.4A2 2 0 0 1 7.2 10H21a1 1 0 0 1 .95 1.3l-2.2 6.6A2 2 0 0 1 17.8 19H4a1 1 0 0 1-.95-1.3z" />
    {:else}
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
    {/if}
  </svg>
{/snippet}

{#snippet branch(node, depth)}
  {#each node.dirs as d (d.path)}
    {@const open = isOpen(d.path)}
    {@const [da, dm, db] = highlight(d.name)}
    <li data-dir={d.path} data-folder={d.path} class="rounded-md {dropDir === d.path ? 'bg-primary-tint ring-2 ring-primary ring-inset' : ''}">
      <button
        type="button"
        class="flex w-full cursor-pointer items-center gap-1.5 rounded-md py-1 pr-2 text-left text-[13px] text-slate-700 hover:bg-slate-200/70"
        style="padding-left: {depth * 12 + 4}px"
        onclick={() => toggle(d.path)}
        aria-expanded={open}
        title={d.path}
      >
        <svg class="size-3 shrink-0 text-slate-400 transition-transform {open ? 'rotate-90' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 6 6 6-6 6" /></svg>
        {@render folderIcon(open)}
        <span class="truncate">{da}<mark class="rounded-sm bg-amber-200 text-inherit">{dm}</mark>{db}</span>
      </button>
      {#if open}
        <ul>{@render branch(d, depth + 1)}</ul>
      {/if}
    </li>
  {/each}
  {#each node.files as f (f.path)}
    {@const [a, m, b] = highlight(f.name)}
    <li class="group relative" data-dir={parentOf(f.path)}>
      <button
        type="button"
        class="flex w-full cursor-pointer items-center gap-1.5 rounded-md py-1 {onreplace && !f.editable ? 'pr-14' : 'pr-8'} text-left text-[13px] {f.path === currentPath
          ? 'bg-primary-soft font-semibold text-primary'
          : 'text-slate-700 hover:bg-slate-200/70'}"
        style="padding-left: {depth * 12 + 22}px"
        onclick={() => onopen(f)}
        title={f.editable ? f.path : `${f.path} (${fmtSize(f.size)}) — abrir`}
      >
        {@render fileIcon(f.name)}
        <span class="truncate">{a}<mark class="rounded-sm bg-amber-200 text-inherit">{m}</mark>{b}</span>
        {#if f.path === mainFile}
          <span class="ml-auto shrink-0 rounded bg-primary px-1 text-[10px] font-semibold text-white">main</span>
        {/if}
        {#each peopleIn(f.path) as p (p.user_id)}
          <span class="size-2 shrink-0 rounded-full" style="background-color: {p.color}" title={p.name}></span>
        {/each}
      </button>
      <div class="absolute top-1/2 right-1 hidden -translate-y-1/2 items-center gap-0.5 group-hover:flex">
        {#if onreplace && !f.editable}
          <button
            type="button"
            class="flex size-6 cursor-pointer items-center justify-center rounded text-slate-400 hover:bg-slate-200 hover:text-slate-900"
            onclick={() => onreplace(f)}
            aria-label="Reemplazar {f.path}"
            title="Reemplazar"
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M17 2l4 4-4 4" /><path d="M3 11V9a3 3 0 0 1 3-3h15M7 22l-4-4 4-4" /><path d="M21 13v2a3 3 0 0 1-3 3H3" /></svg>
          </button>
        {/if}
        {#if f.path !== mainFile}
          <button
            type="button"
            class="flex size-6 cursor-pointer items-center justify-center rounded text-slate-400 hover:bg-red-100 hover:text-red-700"
            onclick={() => onremove(f)}
            aria-label="Eliminar {f.path}"
            title="Eliminar"
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" /></svg>
          </button>
        {/if}
      </div>
    </li>
  {/each}
{/snippet}

<div
  class="relative flex min-h-0 flex-1 flex-col"
  ondragenter={onDragOver}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={onDrop}
  role="presentation"
>
  <div class="flex items-center gap-1 px-2 pb-2">
    <div class="relative min-w-0 flex-1">
      <svg class="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-slate-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
      <input
        type="search"
        bind:value={query}
        onkeydown={onKey}
        placeholder="Buscar archivo…"
        aria-label="Buscar archivo"
        class="h-7 w-full rounded-md border border-slate-200 bg-surface pr-2 pl-7 text-[13px] outline-none placeholder:text-slate-400 focus:border-primary focus:ring-2 focus:ring-primary/20"
      />
    </div>
    {#if dirCount && !q}
      <button
        type="button"
        class="flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-slate-500 hover:bg-slate-200 hover:text-slate-900"
        onclick={() => (collapsed.size ? expandAll() : collapseAll())}
        title={collapsed.size ? 'Desplegar todo' : 'Plegar todo'}
        aria-label={collapsed.size ? 'Desplegar todo' : 'Plegar todo'}
      >
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          {#if collapsed.size}<path d="m7 15 5 5 5-5M7 9l5-5 5 5" />{:else}<path d="m7 20 5-5 5 5M7 4l5 5 5-5" />{/if}
        </svg>
      </button>
    {/if}
  </div>
  <ul
    data-dir=""
    class="min-h-0 flex-1 overflow-y-auto rounded-md px-1.5 pb-3 {dropDir === '' ? 'bg-primary-tint/60 ring-2 ring-primary ring-inset' : ''}"
  >
    {@render branch(tree, 0)}
    {#if q && !matches.length}
      <li class="px-3 py-2 text-[13px] text-slate-500">Sin resultados para «{query.trim()}».</li>
    {/if}
  </ul>
  {#if dropDir !== null}
    <div class="pointer-events-none absolute inset-x-2 bottom-2 flex items-center gap-2 rounded-lg bg-primary px-3 py-2 text-xs font-medium text-white shadow-lg">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12" /></svg>
      <span class="truncate">{dropDir ? `Subir a ${dropDir}/` : 'Subir a la raíz del proyecto'}</span>
    </div>
  {/if}
</div>
