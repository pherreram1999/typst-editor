<script>
  // Selector de carpetas reutilizable, al estilo del diálogo de JetBrains:
  // campo con la ruta arriba (editable y sincronizado con el árbol), barra de
  // herramientas (nueva carpeta, desplegar/plegar todo) y árbol jerárquico con
  // la raíz del proyecto.
  //
  //   <FolderPicker folders={['media', 'media/figuras']} bind:value />
  //
  // - folders: rutas de carpetas existentes («/» como separador, sin barra
  //   inicial); se completan los ancestros que falten.
  // - value (bindable): carpeta elegida; '' es la raíz.
  // - Las carpetas nuevas solo existen aquí hasta que se guarda algo en ellas
  //   (oncreate avisa por si el padre quiere recordarlas).
  // Teclado en el árbol: ↑/↓ moverse, → desplegar/entrar, ← plegar/subir,
  // Inicio/Fin, F2 o Insert para nueva carpeta.
  import { tick, untrack } from 'svelte'

  let {
    folders = [],
    value = $bindable(''),
    rootLabel = 'Proyecto',
    allowCreate = true,
    disabled = false,
    height = '16rem',
    oncreate,
  } = $props()

  let created = $state([]) // carpetas nuevas aún vacías
  // Con pocas carpetas el árbol empieza desplegado; si no, solo el primer
  // nivel (y la ruta de la carpeta elegida, ver más abajo).
  let expanded = $state(new Set(['', ...untrack(() => (folders.length <= 25 ? folders : []))]))
  let creating = $state(null) // { parent, name, error }
  let tree = $state()
  const uid = Math.random().toString(36).slice(2, 8)

  const collator = new Intl.Collator('es', { numeric: true, sensitivity: 'base' })
  const clean = (p) =>
    p
      .trim()
      .replace(/\\/g, '/')
      .split('/')
      .map((s) => s.trim())
      .filter(Boolean)
      .join('/')
  const parentOf = (p) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '')
  const nameOf = (p) => p.split('/').pop()

  // Todas las carpetas, con sus ancestros.
  const all = $derived.by(() => {
    const set = new Set()
    for (const f of [...folders, ...created]) {
      const parts = clean(f).split('/').filter(Boolean)
      for (let i = 1; i <= parts.length; i++) set.add(parts.slice(0, i).join('/'))
    }
    return set
  })
  const childrenOf = $derived.by(() => {
    const m = new Map([['', []]])
    for (const p of all) {
      const parent = parentOf(p)
      if (!m.has(parent)) m.set(parent, [])
      m.get(parent).push(p)
    }
    for (const list of m.values()) list.sort((a, b) => collator.compare(nameOf(a), nameOf(b)))
    return m
  })
  const hasChildren = (p) => (childrenOf.get(p)?.length ?? 0) > 0

  // Filas visibles en orden (para el teclado).
  const visible = $derived.by(() => {
    const out = ['']
    const walk = (p) => {
      if (!expanded.has(p)) return
      for (const c of childrenOf.get(p) ?? []) {
        out.push(c)
        walk(c)
      }
    }
    walk('')
    return out
  })

  const exists = $derived(value === '' || all.has(value))

  // Texto del campo de ruta: propio, para poder escribir «media/» sin que se
  // normalice a mitad; se sincroniza cuando la carpeta cambia desde el árbol.
  let pathText = $state(value)
  $effect(() => {
    const v = value
    untrack(() => {
      if (clean(pathText) !== v) pathText = v
    })
  })

  // Mostrar siempre la carpeta elegida (también si se escribió en el campo).
  $effect(() => {
    const v = value
    const anc = v.split('/').slice(0, -1).map((_, i, a) => a.slice(0, i + 1).join('/'))
    untrack(() => {
      if (anc.some((a) => !expanded.has(a))) expanded = new Set([...expanded, ...anc])
    })
    tick().then(() => tree?.querySelector(`[data-path="${CSS.escape(v)}"]`)?.scrollIntoView({ block: 'nearest' }))
  })

  function select(p) {
    if (disabled) return
    value = p
  }
  function toggle(p, open = !expanded.has(p)) {
    const next = new Set(expanded)
    if (open) next.add(p)
    else next.delete(p)
    expanded = next
  }
  function expandAll() {
    expanded = new Set(['', ...all])
  }
  function collapseAll() {
    expanded = new Set([''])
  }

  function onPathInput(e) {
    pathText = e.currentTarget.value
    value = clean(pathText)
  }

  // ---------- nueva carpeta ----------
  async function startCreate() {
    if (disabled || !allowCreate) return
    const parent = value
    toggle(parent, true)
    creating = { parent, name: '', error: '' }
    await tick()
    tree?.querySelector('input[data-new]')?.focus()
  }
  function validate(name, parent) {
    if (!name) return 'Escribe un nombre'
    if (/[\\/]/.test(name)) return 'Sin «/» ni «\\»'
    if (name.startsWith('.')) return 'No puede empezar por «.»'
    if (/[<>:"|?*\x00-\x1f]/.test(name)) return 'Caracter no permitido'
    const path = parent ? `${parent}/${name}` : name
    if (all.has(path)) return 'Ya existe'
    return ''
  }
  function commitCreate() {
    if (!creating) return
    const name = creating.name.trim()
    const error = validate(name, creating.parent)
    if (error) {
      creating.error = error
      return
    }
    const path = creating.parent ? `${creating.parent}/${name}` : name
    created = [...created, path]
    creating = null
    value = path
    oncreate?.(path)
    tick().then(() => tree?.focus())
  }
  function onCreateKey(e) {
    if (e.key === 'Enter') {
      e.preventDefault()
      commitCreate()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation() // no cerrar el modal que lo contiene
      creating = null
      tree?.focus()
    }
  }

  // ---------- teclado ----------
  function onTreeKey(e) {
    if (disabled || e.target !== tree) return
    const i = visible.indexOf(value)
    const go = (p) => {
      e.preventDefault()
      if (p !== undefined) value = p
    }
    switch (e.key) {
      case 'ArrowDown':
        return go(visible[Math.min(visible.length - 1, i + 1)])
      case 'ArrowUp':
        return go(visible[Math.max(0, i - 1)])
      case 'Home':
        return go('')
      case 'End':
        return go(visible.at(-1))
      case 'ArrowRight':
        e.preventDefault()
        if (hasChildren(value) && !expanded.has(value)) toggle(value, true)
        else if (hasChildren(value)) value = childrenOf.get(value)[0]
        return
      case 'ArrowLeft':
        e.preventDefault()
        if (value !== '' && expanded.has(value) && hasChildren(value)) toggle(value, false)
        else if (value !== '') value = parentOf(value)
        return
      case 'F2':
      case 'Insert':
        e.preventDefault()
        startCreate()
    }
  }
</script>

{#snippet folderIcon(open, root)}
  <svg class="size-4 shrink-0 {root ? 'text-primary' : 'text-amber-500'}" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    {#if root}
      <path d="M4 4h6l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zm4 7v6h2v-4h4v4h2v-6l-4-3z" />
    {:else if open}
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v1H7.2a2 2 0 0 0-1.9 1.4L3 18z" opacity=".75" />
      <path d="M5.3 11.4A2 2 0 0 1 7.2 10H21a1 1 0 0 1 .95 1.3l-2.2 6.6A2 2 0 0 1 17.8 19H4a1 1 0 0 1-.95-1.3z" />
    {:else}
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
    {/if}
  </svg>
{/snippet}

{#snippet row(path, depth)}
  {@const isRoot = path === ''}
  {@const open = expanded.has(path)}
  {@const kids = childrenOf.get(path) ?? []}
  {@const isNew = created.includes(path) && !folders.includes(path)}
  <li role="treeitem" aria-selected={value === path} aria-expanded={kids.length || creating?.parent === path ? open : undefined}>
    <div
      data-path={path}
      class="flex h-7 cursor-pointer items-center gap-1.5 rounded-md pr-2 text-[13px] select-none {value === path
        ? 'bg-primary text-white'
        : 'text-slate-700 hover:bg-slate-200/70'}"
      style="padding-left: {depth * 16 + 4}px"
      onclick={() => select(path)}
      ondblclick={() => kids.length && toggle(path)}
      role="presentation"
    >
      <button
        type="button"
        tabindex="-1"
        class="flex size-4 shrink-0 items-center justify-center rounded {kids.length || creating?.parent === path ? 'cursor-pointer' : 'invisible'}"
        onclick={(e) => {
          e.stopPropagation()
          toggle(path)
        }}
        aria-label={open ? 'Plegar' : 'Desplegar'}
      >
        <svg class="size-3 transition-transform {open ? 'rotate-90' : ''} {value === path ? 'text-white/80' : 'text-slate-400'}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 6 6 6-6 6" /></svg>
      </button>
      {@render folderIcon(open && kids.length > 0, isRoot)}
      <span class="truncate {isRoot ? 'font-semibold' : ''}">{isRoot ? rootLabel : nameOf(path)}</span>
      {#if isNew}
        <span class="ml-auto shrink-0 rounded px-1 text-[10px] font-semibold {value === path ? 'bg-white/20' : 'bg-emerald-100 text-emerald-800'}">nueva</span>
      {/if}
    </div>
    {#if open && (kids.length || creating?.parent === path)}
      <ul role="group">
        {#each kids as k (k)}{@render row(k, depth + 1)}{/each}
        {#if creating?.parent === path}
          <li class="py-0.5" style="padding-left: {(depth + 1) * 16 + 24}px">
            <div class="flex items-center gap-1.5">
              {@render folderIcon(false, false)}
              <input
                data-new
                class="h-7 min-w-0 flex-1 rounded-md border px-2 text-[13px] outline-none focus:ring-2 focus:ring-primary/20 {creating.error
                  ? 'border-red-400'
                  : 'border-primary'} bg-surface"
                placeholder="Nombre de la carpeta"
                bind:value={creating.name}
                oninput={() => (creating.error = '')}
                onkeydown={onCreateKey}
                onblur={() => creating && !creating.name.trim() && (creating = null)}
                aria-label="Nombre de la nueva carpeta"
              />
            </div>
            {#if creating.error}<p class="mt-0.5 text-xs text-red-700">{creating.error}</p>{/if}
          </li>
        {/if}
      </ul>
    {/if}
  </li>
{/snippet}

<div class="flex min-w-0 flex-col gap-2">
  <div class="flex items-center gap-1">
    <div class="relative min-w-0 flex-1">
      <span class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 font-mono text-xs text-slate-400">/</span>
      <input
        class="h-8 w-full rounded-md border border-slate-300 bg-surface pr-2 pl-5 font-mono text-[13px] text-slate-900 outline-none focus:border-primary focus:ring-2 focus:ring-primary/20"
        value={pathText}
        oninput={onPathInput}
        placeholder="(raíz)"
        spellcheck="false"
        aria-label="Ruta de la carpeta"
        {disabled}
      />
    </div>
    {#if allowCreate}
      <button
        type="button"
        class="flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-md text-slate-500 hover:bg-slate-200 hover:text-slate-900 disabled:opacity-50"
        onclick={startCreate}
        title="Nueva carpeta dentro de la seleccionada (F2)"
        aria-label="Nueva carpeta"
        {disabled}
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /><path d="M12 11v5M9.5 13.5h5" /></svg>
      </button>
    {/if}
    <button
      type="button"
      class="flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-md text-slate-500 hover:bg-slate-200 hover:text-slate-900"
      onclick={() => (expanded.size > 1 ? collapseAll() : expandAll())}
      title={expanded.size > 1 ? 'Plegar todo' : 'Desplegar todo'}
      aria-label={expanded.size > 1 ? 'Plegar todo' : 'Desplegar todo'}
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        {#if expanded.size > 1}<path d="m7 20 5-5 5 5M7 4l5 5 5-5" />{:else}<path d="m7 15 5 5 5-5M7 9l5-5 5 5" />{/if}
      </svg>
    </button>
  </div>

  <ul
    bind:this={tree}
    class="overflow-auto rounded-lg border border-slate-200 bg-surface p-1 outline-none focus-visible:ring-2 focus-visible:ring-primary/30 {disabled ? 'opacity-60' : ''}"
    style="height: {height}"
    role="tree"
    aria-label="Carpetas"
    tabindex="0"
    onkeydown={onTreeKey}
    id="folder-tree-{uid}"
  >
    {@render row('', 0)}
  </ul>

  <p class="text-xs text-slate-500">
    {#if !exists}
      Se creará <code class="font-mono">{value}/</code> al guardar.
    {:else}
      Destino: <code class="font-mono">{value ? `${value}/` : `${rootLabel} (raíz)`}</code>
    {/if}
  </p>
</div>
