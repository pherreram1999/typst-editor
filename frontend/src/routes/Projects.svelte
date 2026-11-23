<script>
  import { onMount, onDestroy } from 'svelte'
  import { flip } from 'svelte/animate'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import { d } from '../lib/motion.js'
  import Modal from '../lib/Modal.svelte'
  import RepoPicker from '../lib/RepoPicker.svelte'
  import Thumb from '../lib/Thumb.svelte'
  import ImportPicker from '../lib/ImportPicker.svelte'

  // mode: 'github' (cargar un repositorio) | 'empty' (proyecto nuevo con plantilla)
  // mode 'template': copiar una plantilla
  const empty = (mode) => ({ name: '', mode, account: null, git_remote: '', git_branch: 'main', nameTouched: false, template: null, upload: null })
  let importProgress = $state(null) // 0…1 mientras se sube un proyecto importado
  let templates = $state([])
  let templateCategory = $state('') // filtro de la galería ('' = todas)
  let accounts = $state([])

  let projects = $state([])
  let folders = $state([])
  let loading = $state(true)
  let loadError = $state('')
  let actionError = $state('')
  let modalOpen = $state(false)
  let form = $state(empty())
  let saving = $state(false)
  let error = $state('')
  let search = $state('')

  // ---------- paletas ----------
  const FOLDER_COLORS = {
    blue: { fg: '#1D4ED8', tint: '#DBEAFE', label: 'Azul' },
    green: { fg: '#047857', tint: '#D1FAE5', label: 'Verde' },
    amber: { fg: '#B45309', tint: '#FEF3C7', label: 'Ámbar' },
    violet: { fg: '#6D28D9', tint: '#EDE9FE', label: 'Violeta' },
    pink: { fg: '#BE185D', tint: '#FCE7F3', label: 'Rosa' },
    cyan: { fg: '#0E7490', tint: '#CFFAFE', label: 'Cian' },
    orange: { fg: '#C2410C', tint: '#FFEDD5', label: 'Naranja' },
    slate: { fg: '#334155', tint: '#E2E8F0', label: 'Gris' },
  }
  const colorKeys = Object.keys(FOLDER_COLORS)
  const fc = (key) => FOLDER_COLORS[key] ?? FOLDER_COLORS.blue
  const TILE = ['#1E40AF', '#0E7490', '#047857', '#7C3AED', '#BE185D', '#C2410C', '#4D7C0F', '#B45309']
  const AVATAR = ['#7C3AED', '#0E7490', '#C2410C', '#047857', '#BE185D']
  const tileColor = (p) => TILE[p.id % TILE.length]
  const initials = (n) =>
    (n ?? '').split(/\s+/).filter(Boolean).map((w) => w[0]).slice(0, 2).join('').toUpperCase() || '?'

  // ---------- preferencias de la vista (por navegador) ----------
  function pref(key, def) {
    try {
      return localStorage.getItem(key) || def
    } catch {
      return def
    }
  }
  function savePref(key, v) {
    try {
      localStorage.setItem(key, v)
    } catch {}
  }
  let view = $state(pref('typsteditor.projectsView', 'all')) // all | starred | own | shared | other | none | folder:<id>
  let sort = $state(pref('typsteditor.projectsSort', 'recent')) // recent | name
  let layout = $state(pref('typsteditor.projectsLayout', 'grid')) // grid | list
  const setView = (v) => savePref('typsteditor.projectsView', (view = v))
  $effect(() => savePref('typsteditor.projectsSort', sort))
  $effect(() => savePref('typsteditor.projectsLayout', layout))

  const filters = {
    all: () => true,
    starred: (p) => p.starred,
    own: (p) => p.access === 'owner',
    shared: (p) => p.access === 'member',
    other: (p) => p.access === 'admin',
    none: (p) => p.folder_id == null,
  }
  const filterFor = (v) =>
    v.startsWith('folder:') ? (p) => p.folder_id === Number(v.slice(7)) : (filters[v] ?? filters.all)
  const count = (v) => (v === 'templates' ? templates.length : projects.filter(filterFor(v)).length)

  const ICONS = {
    grid: 'M3 3h7v7H3zM14 3h7v7h-7zM14 14h7v7h-7zM3 14h7v7H3z',
    star: 'm12 2 3.1 6.3 6.9 1-5 4.9 1.2 6.8L12 17.8 5.8 21l1.2-6.8-5-4.9 6.9-1z',
    user: 'M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8',
    users: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75',
    shield: 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10',
    template: 'M4 3h16a1 1 0 0 1 1 1v5H3V4a1 1 0 0 1 1-1M3 13h8v8H4a1 1 0 0 1-1-1zM15 13h6v7a1 1 0 0 1-1 1h-5z',
    copy: 'M9 9h11v11H9zM5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1',
    inbox: 'M22 12h-6l-2 3h-4l-2-3H2M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11',
    plus: 'M12 5v14M5 12h14',
    list: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
    search: 'M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14M20 20l-3.5-3.5',
    move: 'M5 9l-3 3 3 3M9 5l3-3 3 3M15 19l-3 3-3-3M19 9l3 3-3 3M2 12h20M12 2v20',
    branch: 'M6 3.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5M6 15.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5M18 5.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5M6 8.5v7M18 10.5c0 4-6 3-11 5.5',
  }
  const FOLDER_PATH = 'M3 6.5A2.5 2.5 0 0 1 5.5 4h4.1l2 2.5h6.9A2.5 2.5 0 0 1 21 9v8.5a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17.5z'
  const GITHUB_PATH =
    'M12 .5a11.5 11.5 0 0 0-3.64 22.41c.58.1.79-.25.79-.56v-2c-3.2.7-3.88-1.37-3.88-1.37-.53-1.33-1.28-1.69-1.28-1.69-1.05-.71.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.68 0-1.26.45-2.28 1.19-3.09-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.19 1.83 1.19 3.09 0 4.41-2.69 5.38-5.26 5.67.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 12 .5Z'

  const mainViews = $derived([
    { key: 'all', label: 'Todos', short: 'Todos', icon: 'grid' },
    { key: 'starred', label: 'Destacados', short: 'Destacados', icon: 'star' },
    { key: 'own', label: 'Mis proyectos', short: 'Míos', icon: 'user' },
    { key: 'shared', label: 'Compartidos conmigo', short: 'Compartidos', icon: 'users' },
    ...(session.user.role === 'admin' ? [{ key: 'other', label: 'Otros (admin)', short: 'Otros', icon: 'shield' }] : []),
  ])
  const folderById = $derived(new Map(folders.map((f) => [f.id, f])))
  const viewLabel = $derived(
    view.startsWith('folder:')
      ? (folderById.get(Number(view.slice(7)))?.name ?? 'Carpeta')
      : view === 'none'
        ? 'Sin carpeta'
        : view === 'templates'
          ? 'Plantillas'
        : view === 'all'
          ? 'Todos los proyectos'
          : (mainViews.find((v) => v.key === view)?.label ?? 'Todos los proyectos'),
  )

  const collator = new Intl.Collator('es', { sensitivity: 'base', numeric: true })
  // El servidor ya ordena por destacado y actividad; "Nombre" reordena dentro de cada grupo.
  const visible = $derived.by(() => {
    const terms = search.toLowerCase().split(/\s+/).filter(Boolean)
    const list = projects.filter(filterFor(view)).filter((p) => {
      const hay = `${p.name} ${p.owner_name} ${p.git_remote}`.toLowerCase()
      return terms.every((t) => hay.includes(t))
    })
    if (sort === 'name') list.sort((a, b) => b.starred - a.starred || collator.compare(a.name, b.name))
    return list
  })
  const starredList = $derived(visible.filter((p) => p.starred))
  const restList = $derived(visible.filter((p) => !p.starred))

  $effect(() => {
    if (!loading && view.startsWith('folder:') && !folderById.has(Number(view.slice(7)))) setView('all')
  })

  async function load(quiet = false) {
    if (!quiet) loading = true
    loadError = ''
    try {
      const [ps, fs, ts] = await Promise.all([api.listProjects(), api.listFolders(), api.listTemplates()])
      projects = ps
      folders = fs
      templates = ts
    } catch (e) {
      if (!quiet) loadError = e.message
    } finally {
      loading = false
    }
  }

  // Refrescar en segundo plano quién está editando (y cambios de otros).
  let poll
  onMount(() => {
    load()
    poll = setInterval(() => {
      if (document.visibilityState === 'visible' && !dragging && menuFor === null) load(true)
    }, 30000)
  })
  onDestroy(() => clearInterval(poll))

  // ---------- destacar y mover ----------
  const sortProjects = (list) =>
    [...list].sort(
      (a, b) =>
        b.starred - a.starred ||
        (b.starred && a.starred ? (b._starredAt ?? 0) - (a._starredAt ?? 0) : 0) ||
        new Date(b.updated_at) - new Date(a.updated_at),
    )

  async function setPrefs(p, prefs) {
    actionError = ''
    const before = { starred: p.starred, folder_id: p.folder_id }
    const next = { ...p, ...prefs }
    if (prefs.folder_id === 0) next.folder_id = null
    if (prefs.starred && !p.starred) next._starredAt = Date.now()
    projects = sortProjects(projects.map((x) => (x.id === p.id ? next : x))) // optimista
    try {
      await api.setProjectPrefs(p.id, prefs)
    } catch (e) {
      actionError = e.message
      projects = sortProjects(projects.map((x) => (x.id === p.id ? { ...x, ...before } : x)))
    }
  }

  const toggleStar = (p) => setPrefs(p, { starred: !p.starred })
  const moveTo = (p, folderId) => {
    menuFor = null
    if ((p.folder_id ?? 0) !== folderId) setPrefs(p, { folder_id: folderId })
  }

  // ---------- menú de cada proyecto ----------
  let menuFor = $state(null)
  let menuNewFolder = $state('')

  function onWindowClick(e) {
    if (menuFor !== null && !e.target.closest?.('[data-menu]')) menuFor = null
    if (editingFolder !== null && !e.target.closest?.('[data-folder-edit]')) editingFolder = null
  }
  function onWindowKey(e) {
    if (e.key === 'Escape') {
      menuFor = null
      editingFolder = null
      creatingFolder = false
    }
  }

  const nextColor = () => colorKeys[folders.length % colorKeys.length]
  const addFolder = (f) => (folders = [...folders, f].sort((a, b) => collator.compare(a.name, b.name)))

  async function newFolderAndMove(p) {
    const name = menuNewFolder.trim()
    if (!name) return
    try {
      const f = await api.createFolder(name, nextColor())
      addFolder(f)
      menuNewFolder = ''
      moveTo(p, f.id)
    } catch (e) {
      actionError = e.message
    }
  }

  // ---------- carpetas ----------
  let creatingFolder = $state(false)
  let newFolder = $state({ name: '', color: 'blue' })
  let editingFolder = $state(null)
  let editFolder = $state({ name: '', color: 'blue' })

  function startCreateFolder() {
    newFolder = { name: '', color: nextColor() }
    creatingFolder = true
  }

  async function createFolder(e) {
    e.preventDefault()
    actionError = ''
    try {
      const f = await api.createFolder(newFolder.name, newFolder.color)
      addFolder(f)
      creatingFolder = false
      setView(`folder:${f.id}`)
    } catch (err) {
      actionError = err.message
    }
  }

  function startEditFolder(f) {
    editFolder = { name: f.name, color: f.color }
    editingFolder = f.id
  }

  async function saveFolder(e) {
    e.preventDefault()
    actionError = ''
    try {
      const f = await api.updateFolder(editingFolder, editFolder)
      folders = folders.map((x) => (x.id === f.id ? { ...x, ...f, count: x.count } : x)).sort((a, b) => collator.compare(a.name, b.name))
      editingFolder = null
    } catch (err) {
      actionError = err.message
    }
  }

  async function deleteFolder(f) {
    if (!confirm(`¿Eliminar la carpeta «${f.name}»? Sus proyectos no se borran: quedarán sin carpeta.`)) return
    try {
      await api.deleteFolder(f.id)
      folders = folders.filter((x) => x.id !== f.id)
      projects = projects.map((p) => (p.folder_id === f.id ? { ...p, folder_id: null } : p))
      if (view === `folder:${f.id}`) setView('all')
      editingFolder = null
    } catch (err) {
      actionError = err.message
    }
  }

  // ---------- arrastrar y soltar sobre la barra lateral ----------
  let dragging = $state(null)
  let dropTarget = $state(null)
  function onDragStart(e, p) {
    dragging = p
    menuFor = null
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(p.id))
  }
  function dropProps(target) {
    return {
      ondragover: (e) => {
        if (!dragging) return
        e.preventDefault()
        dropTarget = target
      },
      ondragleave: () => dropTarget === target && (dropTarget = null),
      ondrop: (e) => {
        e.preventDefault()
        const p = dragging
        dragging = dropTarget = null
        if (!p) return
        if (target === 'starred') !p.starred && setPrefs(p, { starred: true })
        else moveTo(p, target === 'none' ? 0 : target)
      },
    }
  }

  // ---------- crear proyecto ----------
  async function openCreate() {
    error = ''
    try {
      accounts = await api.listGitHubAccounts()
    } catch {
      accounts = []
    }
    form = empty(templates.length ? 'template' : accounts.length ? 'github' : 'empty')
    form.account = accounts[0]?.id ?? null
    modalOpen = true
  }

  function onPick(r) {
    if (!form.nameTouched) form.name = r.name
  }

  async function submit(e) {
    e.preventDefault()
    error = ''
    saving = true
    try {
      if (form.mode === 'template') {
        if (!form.template) {
          error = 'Elige una plantilla.'
          return
        }
        const p = await doClone(form.template, form.name, 0)
        modalOpen = false
        location.hash = `#/p/${p.id}`
        return
      }
      if (form.mode === 'import') {
        if (!form.upload) {
          error = 'Elige un .zip o una carpeta.'
          return
        }
        importProgress = 0
        const p = await api.importProject(form.name.trim(), form.upload, (x) => (importProgress = x))
        modalOpen = false
        setView('own')
        location.hash = `#/p/${p.id}`
        return
      }
      const body = { name: form.name }
      if (form.mode === 'github') {
        if (!form.git_remote) {
          error = 'Elige un repositorio de la lista.'
          return
        }
        body.github_account_id = form.account
        body.git_remote = form.git_remote
        body.git_branch = form.git_branch
      }
      const p = await api.createProject(body)
      modalOpen = false
      setView('own')
      location.hash = `#/p/${p.id}`
    } catch (e) {
      error = e.message
    } finally {
      saving = false
      importProgress = null
    }
  }

  // ---------- plantillas y copias ----------
  const canManage = (p) => p.access === 'owner' || session.user.role === 'admin'
  const templateCategories = $derived(
    [...new Set(templates.map((t) => t.template_category).filter(Boolean))].sort((a, b) => collator.compare(a, b)),
  )
  const visibleTemplates = $derived.by(() => {
    const terms = search.toLowerCase().split(/\s+/).filter(Boolean)
    return templates.filter(
      (t) =>
        (!templateCategory || t.template_category === templateCategory) &&
        terms.every((x) => `${t.name} ${t.template_category} ${t.template_description}`.toLowerCase().includes(x)),
    )
  })

  // Duplicar un proyecto o crear uno a partir de una plantilla.
  let cloneOpen = $state(false)
  let cloneForm = $state({ source: null, name: '', folder: 0, fromTemplate: false })
  let cloning = $state(false)
  let cloneError = $state('')

  function openClone(p, fromTemplate = false) {
    menuFor = null
    cloneError = ''
    cloneForm = {
      source: p,
      name: fromTemplate ? '' : `Copia de ${p.name}`.slice(0, 128),
      folder: view.startsWith('folder:') ? Number(view.slice(7)) : 0,
      fromTemplate,
    }
    cloneOpen = true
  }

  async function doClone(source, name, folder) {
    const p = await api.cloneProject(source.id, name)
    if (folder) await api.setProjectPrefs(p.id, { folder_id: folder }).catch(() => {})
    return p
  }

  async function submitClone(e) {
    e.preventDefault()
    cloneError = ''
    cloning = true
    try {
      const p = await doClone(cloneForm.source, cloneForm.name, cloneForm.folder)
      cloneOpen = false
      location.hash = `#/p/${p.id}`
    } catch (err) {
      cloneError = err.message
    } finally {
      cloning = false
    }
  }

  // Marcar o desmarcar un proyecto como plantilla.
  let tplOpen = $state(false)
  let tplForm = $state({ project: null, is_template: true, template_category: '', template_description: '' })
  let tplSaving = $state(false)
  let tplError = $state('')

  function openTemplateSettings(p) {
    menuFor = null
    tplError = ''
    tplForm = {
      project: p,
      is_template: true,
      template_category: p.template_category,
      template_description: p.template_description,
    }
    tplOpen = true
  }

  async function submitTemplate(e) {
    e.preventDefault()
    tplError = ''
    tplSaving = true
    try {
      const { project, ...body } = tplForm
      const updated = await api.updateProject(project.id, body)
      projects = projects.map((x) => (x.id === updated.id ? { ...x, ...updated, editors: x.editors } : x))
      templates = await api.listTemplates()
      tplOpen = false
    } catch (err) {
      tplError = err.message
    } finally {
      tplSaving = false
    }
  }

  // ---------- formato ----------
  const rtf = new Intl.RelativeTimeFormat('es', { numeric: 'auto' })
  const dateFmt = new Intl.DateTimeFormat('es', { dateStyle: 'long', timeStyle: 'short' })
  function ago(iso) {
    const s = (new Date(iso) - Date.now()) / 1000
    for (const [u, n] of [['year', 31536000], ['month', 2592000], ['week', 604800], ['day', 86400], ['hour', 3600], ['minute', 60]]) {
      if (Math.abs(s) >= n) return rtf.format(Math.round(s / n), u)
    }
    return 'ahora mismo'
  }
  const repoName = (url) => url.replace(/^https:\/\/(www\.)?github\.com\//, '').replace(/\.git$/, '')
  const editingText = (names) =>
    names.length === 1 ? `${names[0].split(' ')[0]} está editando` : `${names.length} personas editando`
</script>

<svelte:window onclick={onWindowClick} onkeydown={onWindowKey} />

{#snippet icon(name, size = 16, fill = 'none')}
  <svg width={size} height={size} viewBox="0 0 24 24" {fill} stroke="currentColor" stroke-width="2" stroke-linecap="round"
    stroke-linejoin="round" aria-hidden="true"><path d={ICONS[name]} /></svg>
{/snippet}

{#snippet folderIcon(color, size = 26)}
  <span class="flex shrink-0 items-center justify-center rounded-[7px]" style="width:{size}px;height:{size}px;background:color-mix(in srgb, {fc(color).fg} 16%, var(--color-surface));color:color-mix(in srgb, {fc(color).fg}, var(--color-slate-900) var(--fg-lift))">
    <svg width={size * 0.58} height={size * 0.58} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d={FOLDER_PATH} /></svg>
  </span>
{/snippet}

{#snippet countPill(n, active)}
  <span
    class="flex h-[22px] min-w-6 items-center justify-center rounded-full px-1.5 text-xs font-semibold tabular-nums {active
      ? 'bg-primary text-white'
      : 'bg-slate-100 text-slate-600'}">{n}</span
  >
{/snippet}

{#snippet navItem(key, label, iconName, target = null)}
  {@const active = view === key}
  <button
    type="button"
    class="flex h-10 w-full cursor-pointer items-center gap-2.5 rounded-[9px] pr-2.5 pl-3 text-left text-sm transition-colors {active
      ? 'bg-primary-tint font-semibold text-primary'
      : 'font-medium text-slate-700 hover:bg-slate-100'} {target !== null && dropTarget === target ? 'ring-2 ring-primary ring-inset' : ''}"
    onclick={() => setView(key)}
    aria-current={active ? 'page' : undefined}
    {...target !== null ? dropProps(target) : {}}
  >
    <span class="flex {key === 'starred' ? 'text-amber-500' : active ? 'text-primary' : 'text-slate-500'}"
      >{@render icon(iconName, 17, key === 'starred' ? 'currentColor' : 'none')}</span
    >
    <span class="min-w-0 flex-1 truncate">{label}</span>
    {#if !loading}{@render countPill(count(key), active)}{/if}
  </button>
{/snippet}

{#snippet starButton(p, size = 36)}
  <button
    type="button"
    class="flex cursor-pointer items-center justify-center rounded-[9px] transition-colors {p.starred
      ? 'text-amber-500 hover:bg-amber-100'
      : 'text-slate-300 hover:bg-slate-100 hover:text-amber-500'}"
    style="width:{size}px;height:{size}px"
    onclick={() => toggleStar(p)}
    aria-pressed={p.starred}
    aria-label={p.starred ? `Quitar ${p.name} de destacados` : `Destacar ${p.name}`}
    title={p.starred ? 'Quitar de destacados' : 'Destacar'}
  >
    <svg width="18" height="18" viewBox="0 0 24 24" fill={p.starred ? 'currentColor' : 'none'} stroke="currentColor"
      stroke-width="2" stroke-linejoin="round" aria-hidden="true"><path d={ICONS.star} /></svg>
  </button>
{/snippet}

{#snippet projectMenu(p, size = 36)}
  <div class="relative" data-menu>
    <button
      type="button"
      class="flex cursor-pointer items-center justify-center rounded-[9px] text-slate-400 hover:bg-slate-100 hover:text-slate-900"
      style="width:{size}px;height:{size}px"
      onclick={() => {
        menuNewFolder = ''
        menuFor = menuFor === p.id ? null : p.id
      }}
      aria-haspopup="menu"
      aria-expanded={menuFor === p.id}
      aria-label="Mover {p.name} a una carpeta"
      title="Mover a carpeta"
    >
      <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"
        ><circle cx="12" cy="5" r="1.7" /><circle cx="12" cy="12" r="1.7" /><circle cx="12" cy="19" r="1.7" /></svg
      >
    </button>
    {#if menuFor === p.id}
      <div class="absolute top-10 right-0 z-30 w-64 rounded-xl border border-slate-200 bg-surface py-1.5 text-sm shadow-xl" role="menu">
        <p class="px-3 pt-1 pb-1.5 text-[11px] font-bold tracking-[0.08em] text-slate-500 uppercase">Mover a carpeta</p>
        <button type="button" role="menuitemradio" aria-checked={p.folder_id == null}
          class="flex w-full cursor-pointer items-center gap-2.5 px-3 py-2 text-left hover:bg-slate-50"
          onclick={() => moveTo(p, 0)}>
          <span class="flex size-[22px] items-center justify-center rounded-md border-[1.5px] border-dashed border-slate-300 text-slate-400"
            >{@render icon('inbox', 11)}</span
          >
          <span class="flex-1">Sin carpeta</span>
          {#if p.folder_id == null}<span class="text-primary">✓</span>{/if}
        </button>
        {#each folders as f (f.id)}
          <button type="button" role="menuitemradio" aria-checked={p.folder_id === f.id}
            class="flex w-full cursor-pointer items-center gap-2.5 px-3 py-2 text-left hover:bg-slate-50"
            onclick={() => moveTo(p, f.id)}>
            {@render folderIcon(f.color, 22)}
            <span class="flex-1 truncate">{f.name}</span>
            {#if p.folder_id === f.id}<span class="text-primary">✓</span>{/if}
          </button>
        {/each}
        <form class="mt-1 flex gap-1.5 border-t border-slate-100 px-2.5 pt-2" onsubmit={(e) => (e.preventDefault(), newFolderAndMove(p))}>
          <input class="input !h-9 !text-[13px]" bind:value={menuNewFolder} maxlength="64" placeholder="Nueva carpeta…" aria-label="Nueva carpeta" />
          <button class="btn-primary btn-sm !h-9 !px-2.5" disabled={!menuNewFolder.trim()} aria-label="Crear carpeta y mover">{@render icon('plus', 14)}</button>
        </form>
        <div class="mt-2 border-t border-slate-100 pt-1.5">
          <button type="button" role="menuitem" class="flex w-full cursor-pointer items-center gap-2.5 px-3 py-2 text-left hover:bg-slate-50"
            onclick={() => openClone(p)}>
            <span class="text-slate-500">{@render icon('copy', 16)}</span> Duplicar proyecto…
          </button>
          {#if canManage(p)}
            <button type="button" role="menuitem" class="flex w-full cursor-pointer items-center gap-2.5 px-3 py-2 text-left hover:bg-slate-50"
              onclick={() => openTemplateSettings(p)}>
              <span class="text-violet-600">{@render icon('template', 16)}</span>
              {p.is_template ? 'Ajustes de plantilla…' : 'Usar como plantilla…'}
            </button>
          {/if}
        </div>
      </div>
    {/if}
  </div>
{/snippet}

{#snippet avatars(names, ring)}
  {#if names.length}
    <div class="flex" title={names.join(', ')}>
      {#each names.slice(0, 4) as n, i}
        <span
          class="flex size-[26px] items-center justify-center rounded-full border-2 text-[10.5px] font-bold text-white {ring}"
          style="background:{AVATAR[i % AVATAR.length]};margin-left:{i ? '-7px' : '0'}">{initials(n)}</span
        >
      {/each}
      {#if names.length > 4}
        <span class="-ml-[7px] flex size-[26px] items-center justify-center rounded-full border-2 bg-slate-200 text-[10px] font-bold text-slate-600 {ring}"
          >+{names.length - 4}</span
        >
      {/if}
    </div>
  {/if}
{/snippet}

{#snippet live(p)}
  {#if p.editors?.length}
    <span class="inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap text-emerald-700" title={p.editors.join(', ')}>
      <span class="size-[7px] rounded-full bg-emerald-500 ring-[3px] ring-emerald-100"></span>
      {editingText(p.editors)}
    </span>
  {/if}
{/snippet}

{#snippet folderChip(p)}
  {#if p.folder_id != null && folderById.has(p.folder_id) && !view.startsWith('folder:')}
    {@const f = folderById.get(p.folder_id)}
    <span class="inline-flex h-6 items-center gap-1.5 rounded-md px-2 text-xs font-semibold" style="background:color-mix(in srgb, {fc(f.color).fg} 16%, var(--color-surface));color:color-mix(in srgb, {fc(f.color).fg}, var(--color-slate-900) var(--fg-lift))">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d={FOLDER_PATH} /></svg>
      {f.name}
    </span>
  {/if}
{/snippet}

{#snippet repoLine(p)}
  {#if p.git_remote}
    <p class="flex min-w-0 items-center gap-[7px] text-[13px] text-slate-700" title={p.git_remote}>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" class="shrink-0" aria-hidden="true"><path d={GITHUB_PATH} /></svg>
      <span class="truncate">{repoName(p.git_remote)}</span>
      <span class="inline-flex h-[22px] shrink-0 items-center gap-1 rounded-full bg-slate-100 px-2 text-xs font-medium text-slate-600">
        {@render icon('branch', 11)}{p.git_branch}
      </span>
    </p>
  {:else}
    <p class="text-[13px] text-slate-400">Sin repositorio remoto</p>
  {/if}
{/snippet}

{#snippet card(p)}
  <article
    class="group relative flex h-full min-h-[214px] flex-col gap-3 rounded-[14px] border px-[18px] pt-[18px] pb-3.5 shadow-sm transition {p.starred
      ? 'border-amber-300 bg-amber-50 hover:border-amber-400'
      : 'border-slate-200 bg-surface hover:border-primary'} hover:shadow-md {dragging?.id === p.id ? 'opacity-50' : ''}"
    draggable="true"
    ondragstart={(e) => onDragStart(e, p)}
    ondragend={() => (dragging = dropTarget = null)}
  >
    <div class="flex items-start gap-3">
      <span class="flex size-11 shrink-0 items-center justify-center rounded-[11px] text-[15px] font-bold tracking-wide text-white"
        style="background:{tileColor(p)}" aria-hidden="true">{initials(p.name)}</span>
      <div class="flex min-w-0 flex-1 flex-col gap-0.5 pt-0.5">
        <h3 class="truncate text-base leading-snug font-semibold">
          <!-- Enlace "estirado": toda la tarjeta abre el proyecto; los botones quedan por encima. -->
          <a href="#/p/{p.id}" class="text-slate-900 no-underline after:absolute after:inset-0 after:rounded-[14px] group-hover:text-primary">{p.name}</a>
        </h3>
        <p class="text-[12.5px] text-slate-500">{p.access === 'owner' ? 'Tuyo' : `De ${p.owner_name}`}</p>
      </div>
      <div class="relative z-10 -mt-1 -mr-1.5 flex gap-0.5">
        {@render starButton(p)}
        {@render projectMenu(p)}
      </div>
    </div>
    {@render repoLine(p)}
    <div class="flex flex-wrap gap-1.5">
      <span class="inline-flex h-6 items-center rounded-md border border-slate-200 bg-slate-50 px-2 font-mono text-xs text-slate-600">{p.main_file}</span>
      {#if p.is_template}
        <span class="inline-flex h-6 items-center gap-1 rounded-md bg-violet-100 px-2 text-xs font-semibold text-violet-800">
          {@render icon('template', 12)} Plantilla{p.template_category ? ` · ${p.template_category}` : ''}
        </span>
      {/if}
      {@render folderChip(p)}
    </div>
    <div class="mt-auto flex min-h-7 items-center gap-2.5 border-t pt-3 {p.starred ? 'border-amber-200' : 'border-slate-100'}">
      {@render avatars(p.member_names ?? [], p.starred ? 'border-amber-50' : 'border-white')}
      {@render live(p)}
      <time class="ml-auto text-xs whitespace-nowrap text-slate-500" datetime={p.updated_at} title={dateFmt.format(new Date(p.updated_at))}
        >{ago(p.updated_at)}</time
      >
    </div>
  </article>
{/snippet}

{#snippet row(p)}
  <article
    class="group relative flex items-center gap-3 px-4 py-3 transition-colors hover:bg-slate-50 {p.starred ? 'bg-amber-50/60' : ''} {dragging?.id === p.id ? 'opacity-50' : ''}"
    draggable="true"
    ondragstart={(e) => onDragStart(e, p)}
    ondragend={() => (dragging = dropTarget = null)}
  >
    <span class="flex size-9 shrink-0 items-center justify-center rounded-[9px] text-[13px] font-bold text-white" style="background:{tileColor(p)}"
      aria-hidden="true">{initials(p.name)}</span>
    <div class="flex min-w-0 flex-[2] flex-col">
      <a href="#/p/{p.id}" class="truncate text-[15px] font-semibold text-slate-900 no-underline after:absolute after:inset-0 group-hover:text-primary">{p.name}</a>
      <span class="truncate text-xs text-slate-500">{p.access === 'owner' ? 'Tuyo' : `De ${p.owner_name}`}</span>
    </div>
    <div class="hidden min-w-0 flex-[2] md:block">{@render repoLine(p)}</div>
    <div class="hidden min-w-0 flex-1 xl:block">{@render folderChip(p)}</div>
    <div class="hidden items-center gap-2.5 lg:flex">
      {@render avatars(p.member_names ?? [], p.starred ? 'border-amber-50' : 'border-white')}
      {@render live(p)}
    </div>
    <time class="hidden w-28 text-right text-xs text-slate-500 sm:block" datetime={p.updated_at}>{ago(p.updated_at)}</time>
    <div class="relative z-10 flex gap-0.5">
      {@render starButton(p, 34)}
      {@render projectMenu(p, 34)}
    </div>
  </article>
{/snippet}

{#snippet collection(list)}
  {#if layout === 'list'}
    <div class="divide-y divide-slate-100 overflow-visible rounded-[14px] border border-slate-200 bg-surface shadow-sm">
      {#each list as p (p.id)}
        <div animate:flip={{ duration: d(220) }}>{@render row(p)}</div>
      {/each}
    </div>
  {:else}
    <div class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,320px),1fr))] gap-4">
      {#each list as p (p.id)}
        <div animate:flip={{ duration: d(220) }}>{@render card(p)}</div>
      {/each}
    </div>
  {/if}
{/snippet}

{#snippet gallery()}
  <div class="flex flex-col gap-5">
    <p class="max-w-3xl text-sm text-slate-600">
      Plantillas compartidas por todo el equipo: carátulas, informes, oficios… Al usar una se crea un proyecto tuyo con una
      copia de sus archivos; la plantilla original no cambia. Para publicar una, abre el menú ⋮ de tu proyecto y elige
      «Usar como plantilla».
    </p>
    {#if templateCategories.length}
      <div class="flex flex-wrap gap-2" role="group" aria-label="Categorías">
        {#each ['', ...templateCategories] as c}
          <button
            type="button"
            class="h-9 cursor-pointer rounded-full px-3.5 text-sm font-medium {templateCategory === c
              ? 'bg-violet-700 text-white'
              : 'border border-slate-200 bg-surface text-slate-700 hover:border-violet-300'}"
            aria-pressed={templateCategory === c}
            onclick={() => (templateCategory = c)}>{c || 'Todas'}</button
          >
        {/each}
      </div>
    {/if}
    {#if !visibleTemplates.length}
      <div class="flex flex-col items-center gap-3 rounded-[14px] border border-dashed border-slate-300 bg-surface px-6 py-14 text-center">
        <span class="text-violet-600">{@render icon('template', 28)}</span>
        <p class="text-[15px] font-medium">{templates.length ? 'Ninguna plantilla coincide' : 'Todavía no hay plantillas'}</p>
        <p class="max-w-md text-sm text-slate-600">
          Prepara un proyecto (por ejemplo una carátula) y márcalo con «Usar como plantilla» desde su menú ⋮ para que todos puedan partir de él.
        </p>
      </div>
    {:else}
      <div class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,240px),1fr))] gap-5">
        {#each visibleTemplates as t (t.id)}
          <article class="group flex flex-col overflow-hidden rounded-[14px] border border-slate-200 bg-surface shadow-sm transition hover:border-violet-400 hover:shadow-md">
            <button type="button" class="block cursor-pointer border-b border-slate-200" onclick={() => openClone(t, true)}
              aria-label="Usar la plantilla {t.name}">
              <Thumb projectId={t.id} alt="Primera página de {t.name}" class="aspect-[17/15] w-full transition group-hover:opacity-90" />
            </button>
            <div class="flex flex-1 flex-col gap-2 p-4">
              <div class="flex items-start justify-between gap-2">
                <h3 class="text-[15px] leading-snug font-semibold">{t.name}</h3>
                {#if t.template_category}
                  <span class="badge shrink-0 !py-0.5 bg-violet-100 text-violet-800">{t.template_category}</span>
                {/if}
              </div>
              {#if t.template_description}
                <p class="line-clamp-3 text-[13px] leading-relaxed text-slate-600">{t.template_description}</p>
              {/if}
              <p class="mt-auto pt-1 text-xs text-slate-500">{t.access === 'owner' ? 'Tuya' : `De ${t.owner_name}`}</p>
              <div class="flex gap-2 pt-1">
                <button type="button" class="btn-primary btn-sm flex-1 !bg-violet-700 hover:!bg-violet-800" onclick={() => openClone(t, true)}>
                  Usar plantilla
                </button>
                {#if t.access !== 'template'}
                  <a href="#/p/{t.id}" class="btn-secondary btn-sm no-underline">Editar</a>
                {/if}
              </div>
            </div>
          </article>
        {/each}
      </div>
    {/if}
  </div>
{/snippet}

<div class="flex min-h-[calc(100dvh-3.5rem)] items-stretch">
  <!-- Barra lateral (escritorio) -->
  <aside class="hidden w-[272px] shrink-0 border-r border-slate-200 bg-surface lg:block" aria-label="Organización de proyectos">
    <div class="sticky top-0 flex max-h-dvh min-h-[calc(100dvh-3.5rem)] flex-col gap-[22px] overflow-y-auto px-3.5 py-5">
      <button type="button" class="btn-primary !h-11 !rounded-[10px] shadow-sm" onclick={openCreate}>
        {@render icon('plus', 16)} Nuevo proyecto
      </button>

      <nav aria-label="Vistas" class="flex flex-col gap-0.5">
        {#each mainViews as v (v.key)}
          {@render navItem(v.key, v.label, v.icon, v.key === 'starred' ? 'starred' : null)}
        {/each}
        {@render navItem('templates', 'Plantillas', 'template')}
      </nav>

      <div class="mx-1.5 h-px bg-slate-100"></div>

      <section aria-labelledby="carpetas-t" class="flex flex-col gap-0.5">
        <div class="flex items-center justify-between pr-1.5 pb-1.5 pl-3">
          <h2 id="carpetas-t" class="text-[11px] font-bold tracking-[0.08em] text-slate-500 uppercase">Carpetas</h2>
          <button
            type="button"
            class="flex size-7 cursor-pointer items-center justify-center rounded-[7px] bg-slate-100 text-slate-700 hover:bg-slate-200"
            onclick={startCreateFolder}
            aria-label="Nueva carpeta"
            title="Nueva carpeta">{@render icon('plus', 14)}</button
          >
        </div>

        {#if creatingFolder}
          <form class="mb-1 flex flex-col gap-2 rounded-xl border border-slate-200 bg-slate-50 p-2.5" onsubmit={createFolder}>
            <!-- svelte-ignore a11y_autofocus -->
            <input class="input !h-9" bind:value={newFolder.name} maxlength="64" placeholder="Nombre de la carpeta" autofocus aria-label="Nombre de la carpeta" />
            <div class="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Color">
              {#each colorKeys as c}
                <button type="button" role="radio" aria-checked={newFolder.color === c} aria-label={FOLDER_COLORS[c].label}
                  class="size-6 cursor-pointer rounded-full ring-offset-2 {newFolder.color === c ? 'ring-2 ring-slate-900' : ''}"
                  style="background:{FOLDER_COLORS[c].fg}" onclick={() => (newFolder.color = c)}></button>
              {/each}
            </div>
            <div class="flex justify-end gap-1.5">
              <button type="button" class="btn-secondary btn-sm !h-8" onclick={() => (creatingFolder = false)}>Cancelar</button>
              <button class="btn-primary btn-sm !h-8" disabled={!newFolder.name.trim()}>Crear</button>
            </div>
          </form>
        {/if}

        {#each folders as f (f.id)}
          {@const key = `folder:${f.id}`}
          {@const active = view === key}
          {#if editingFolder === f.id}
            <form class="mb-1 flex flex-col gap-2 rounded-xl border border-slate-200 bg-slate-50 p-2.5" onsubmit={saveFolder} data-folder-edit>
              <!-- svelte-ignore a11y_autofocus -->
              <input class="input !h-9" bind:value={editFolder.name} maxlength="64" autofocus aria-label="Nombre de la carpeta" />
              <div class="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Color">
                {#each colorKeys as c}
                  <button type="button" role="radio" aria-checked={editFolder.color === c} aria-label={FOLDER_COLORS[c].label}
                    class="size-6 cursor-pointer rounded-full ring-offset-2 {editFolder.color === c ? 'ring-2 ring-slate-900' : ''}"
                    style="background:{FOLDER_COLORS[c].fg}" onclick={() => (editFolder.color = c)}></button>
                {/each}
              </div>
              <div class="flex items-center gap-1.5">
                <button type="button" class="cursor-pointer text-xs font-medium text-red-700 hover:underline" onclick={() => deleteFolder(f)}>Eliminar</button>
                <button type="button" class="btn-secondary btn-sm ml-auto !h-8" onclick={() => (editingFolder = null)}>Cancelar</button>
                <button class="btn-primary btn-sm !h-8" disabled={!editFolder.name.trim()}>Guardar</button>
              </div>
            </form>
          {:else}
            <div class="group/folder relative">
              <button
                type="button"
                class="flex h-10 w-full cursor-pointer items-center gap-2.5 rounded-[9px] pr-2.5 pl-3 text-left text-sm transition-colors {active
                  ? 'bg-primary-tint font-semibold text-primary'
                  : 'font-medium text-slate-700 hover:bg-slate-100'} {dropTarget === f.id ? 'ring-2 ring-primary ring-inset' : ''}"
                onclick={() => setView(key)}
                aria-current={active ? 'page' : undefined}
                {...dropProps(f.id)}
              >
                {@render folderIcon(f.color)}
                <span class="min-w-0 flex-1 truncate">{f.name}</span>
                <span class="text-xs text-slate-500 tabular-nums group-hover/folder:invisible">{count(key)}</span>
              </button>
              <button
                type="button"
                class="absolute top-1/2 right-1.5 hidden size-7 -translate-y-1/2 cursor-pointer items-center justify-center rounded-md text-slate-500 group-hover/folder:flex hover:bg-surface hover:text-slate-900 focus-visible:flex"
                onclick={(e) => (e.stopPropagation(), startEditFolder(f))}
                aria-label="Editar carpeta {f.name}"
                title="Renombrar, cambiar color o eliminar"
                data-folder-edit
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" /></svg>
              </button>
            </div>
          {/if}
        {/each}

        <button
          type="button"
          class="flex h-10 w-full cursor-pointer items-center gap-2.5 rounded-[9px] pr-2.5 pl-3 text-left text-sm transition-colors {view === 'none'
            ? 'bg-primary-tint font-semibold text-primary'
            : 'font-medium text-slate-600 hover:bg-slate-100'} {dropTarget === 'none' ? 'ring-2 ring-primary ring-inset' : ''}"
          onclick={() => setView('none')}
          aria-current={view === 'none' ? 'page' : undefined}
          {...dropProps('none')}
        >
          <span class="flex size-[26px] items-center justify-center rounded-[7px] border-[1.5px] border-dashed border-slate-300 text-slate-400">{@render icon('inbox', 13)}</span>
          <span class="flex-1">Sin carpeta</span>
          <span class="text-xs text-slate-500 tabular-nums">{count('none')}</span>
        </button>
      </section>

      <div
        class="mt-auto flex items-start gap-2.5 rounded-xl border-[1.5px] border-dashed p-3.5 transition-colors {dragging
          ? 'border-primary bg-primary-soft'
          : 'border-blue-200 bg-primary-tint'}"
      >
        <span class="mt-px shrink-0 text-primary">{@render icon('move', 18)}</span>
        <p class="text-[12.5px] leading-normal text-blue-900">
          Arrastra un proyecto hasta una carpeta o hasta <strong>Destacados</strong>. Solo tú ves esta organización.
        </p>
      </div>
    </div>
  </aside>

  <!-- Contenido a todo el ancho -->
  <main class="flex min-w-0 flex-1 flex-col gap-6 px-4 pt-5 pb-12 sm:px-8 sm:pt-7 lg:gap-7">
    <div class="flex flex-wrap items-end justify-between gap-4">
      <div class="flex min-w-0 flex-col gap-1">
        <p class="text-[13px] text-slate-500">Proyectos</p>
        <h1 class="text-2xl leading-tight font-bold tracking-tight sm:text-[28px]">
          {viewLabel}
          {#if !loading}<span class="ml-1 text-base font-medium tracking-normal text-slate-400">{view === 'templates' ? visibleTemplates.length : visible.length}</span>{/if}
        </h1>
      </div>
      <button type="button" class="btn-primary lg:hidden" onclick={openCreate}>{@render icon('plus', 16)} Nuevo</button>
      <div class="flex w-full flex-wrap items-center gap-2.5 lg:w-auto">
        <label class="relative block w-full min-w-0 sm:w-auto sm:flex-1 lg:w-[340px] lg:flex-none">
          <span class="sr-only">Buscar proyectos</span>
          <span class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-slate-400">{@render icon('search', 16)}</span>
          <input class="input !h-10 !rounded-[10px] !pl-9" type="search" bind:value={search} placeholder="Buscar por nombre, autor o repositorio…" />
        </label>
        {#if view !== 'templates'}
        <label class="flex h-10 items-center gap-2 rounded-[10px] border border-slate-300 bg-surface px-3 text-[13px] text-slate-600">
          Ordenar
          <select class="cursor-pointer bg-transparent text-[13px] font-semibold text-slate-900 focus:outline-none" bind:value={sort}>
            <option value="recent">Actividad reciente</option>
            <option value="name">Nombre</option>
          </select>
        </label>
        <div class="flex gap-0.5 rounded-[10px] bg-slate-200 p-[3px]" role="group" aria-label="Vista">
          {#each [['grid', 'Cuadrícula'], ['list', 'Lista']] as [k, label]}
            <button
              type="button"
              class="flex size-[34px] cursor-pointer items-center justify-center rounded-lg {layout === k
                ? 'bg-surface text-primary shadow-sm'
                : 'text-slate-500 hover:text-slate-900'}"
              aria-pressed={layout === k}
              aria-label={label}
              title={label}
              onclick={() => (layout = k)}>{@render icon(k, 16)}</button
            >
          {/each}
        </div>
        {/if}
      </div>
    </div>

    <!-- Vistas y carpetas en móvil: una fila desplazable -->
    <nav class="-mx-4 -mt-2 flex gap-2 overflow-x-auto px-4 pb-1 [scrollbar-width:none] sm:-mx-8 sm:px-8 lg:hidden" aria-label="Vistas y carpetas">
      {#each [...mainViews.map((v) => ({ key: v.key, label: v.short, color: v.key === 'starred' ? '#F59E0B' : '' })), { key: 'templates', label: 'Plantillas', color: '#7C3AED' }, ...folders.map((f) => ({ key: `folder:${f.id}`, label: f.name, color: fc(f.color).fg })), { key: 'none', label: 'Sin carpeta', color: '' }] as c (c.key)}
        {@const active = view === c.key}
        <button
          type="button"
          class="inline-flex h-10 shrink-0 cursor-pointer items-center gap-2 rounded-full px-3 text-sm font-semibold whitespace-nowrap {active
            ? 'bg-primary text-white'
            : 'border border-slate-200 bg-surface text-slate-700'}"
          onclick={() => setView(c.key)}
          aria-current={active ? 'page' : undefined}
        >
          {#if c.color}<span class="size-2 rounded-[3px]" style="background:{c.color}"></span>{/if}
          {c.label}
          {#if !loading}<span class="text-xs {active ? 'text-blue-200' : 'text-slate-400'}">{count(c.key)}</span>{/if}
        </button>
      {/each}
    </nav>

    {#if actionError}<div class="alert-error">{actionError}</div>{/if}

    {#if loadError}
      <div class="alert-error">{loadError}</div>
    {:else if loading}
      <p class="text-sm text-slate-500">Cargando…</p>
    {:else if view === 'templates'}
      {@render gallery()}
    {:else if visible.length === 0}
      <div class="flex flex-col items-center gap-3 rounded-[14px] border border-dashed border-slate-300 bg-surface px-6 py-14 text-center">
        {#if search.trim()}
          <p class="text-[15px] font-medium">Ningún proyecto coincide con «{search.trim()}»</p>
        {:else if view === 'starred'}
          <span class="text-amber-500">{@render icon('star', 28)}</span>
          <p class="text-[15px] font-medium">No tienes proyectos destacados</p>
          <p class="max-w-sm text-sm text-slate-600">Pulsa la estrella de un proyecto para que aparezca siempre al principio.</p>
        {:else if view === 'shared'}
          <p class="text-[15px] font-medium">Nadie ha compartido proyectos contigo todavía</p>
          <p class="max-w-sm text-sm text-slate-600">Cuando otro usuario te invite a uno de sus proyectos aparecerá aquí.</p>
        {:else if view.startsWith('folder:')}
          <p class="text-[15px] font-medium">Esta carpeta está vacía</p>
          <p class="max-w-sm text-sm text-slate-600">Arrastra proyectos hasta ella o usa el menú ⋮ de cada proyecto.</p>
        {:else if view === 'own' || view === 'all'}
          <p class="text-[15px] font-medium">Todavía no tienes proyectos</p>
          <p class="max-w-sm text-sm text-slate-600">Crea uno desde cero o cárgalo desde un repositorio de GitHub.</p>
          <button type="button" class="btn-primary mt-2" onclick={openCreate}>{@render icon('plus', 16)} Nuevo proyecto</button>
        {:else}
          <p class="text-[15px] font-medium">No hay proyectos aquí</p>
        {/if}
      </div>
    {:else if starredList.length && restList.length && view !== 'starred'}
      <section aria-labelledby="dest-t" class="flex flex-col gap-3.5">
        <h2 id="dest-t" class="flex items-center gap-2 text-[13px] font-bold tracking-[0.06em] text-amber-700 uppercase">
          <span class="text-amber-500">{@render icon('star', 15, 'currentColor')}</span> Destacados
        </h2>
        {@render collection(starredList)}
      </section>
      <section aria-labelledby="resto-t" class="flex flex-col gap-3.5">
        <h2 id="resto-t" class="text-[13px] font-bold tracking-[0.06em] text-slate-600 uppercase">Resto de proyectos</h2>
        {@render collection(restList)}
      </section>
    {:else}
      {@render collection(visible)}
    {/if}
  </main>
</div>

<Modal open={modalOpen} title="Nuevo proyecto" wide onclose={() => !saving && (modalOpen = false)}>
  <form class="flex flex-col gap-4" onsubmit={submit}>
    {#if error}<div class="alert-error">{error}</div>{/if}

    <div class="grid grid-cols-2 gap-1 rounded-lg bg-slate-100 p-1 sm:grid-cols-4" role="tablist">
      {#each [['template', 'Desde plantilla'], ['github', 'Desde GitHub'], ['import', 'Desde mi equipo'], ['empty', 'Vacío (carta)']] as [k, label]}
        <button
          type="button"
          role="tab"
          aria-selected={form.mode === k}
          class="h-9 cursor-pointer rounded-md text-sm font-medium {form.mode === k ? 'bg-surface text-primary shadow-sm' : 'text-slate-600 hover:text-slate-900'}"
          disabled={saving}
          onclick={() => (form.mode = k)}>{label}</button
        >
      {/each}
    </div>

    {#if form.mode === 'template'}
      {#if templates.length}
        <div class="grid max-h-80 grid-cols-[repeat(auto-fill,minmax(130px,1fr))] gap-3 overflow-y-auto p-0.5" role="radiogroup" aria-label="Plantillas">
          {#each templates as t (t.id)}
            {@const sel = form.template?.id === t.id}
            <button
              type="button"
              role="radio"
              aria-checked={sel}
              class="flex cursor-pointer flex-col overflow-hidden rounded-lg border-2 bg-surface text-left transition {sel
                ? 'border-violet-600 ring-2 ring-violet-200'
                : 'border-slate-200 hover:border-violet-300'}"
              onclick={() => {
                form.template = t
                if (!form.nameTouched) form.name = ''
              }}
            >
              <Thumb projectId={t.id} alt="" class="aspect-[17/15] w-full border-b border-slate-200" />
              <span class="truncate px-2 pt-1.5 text-[13px] font-semibold">{t.name}</span>
              <span class="truncate px-2 pb-2 text-[11px] text-slate-500">{t.template_category || 'Sin categoría'}</span>
            </button>
          {/each}
        </div>
      {:else}
        <div class="alert-info">
          Todavía no hay plantillas. Marca un proyecto como plantilla desde su menú ⋮ («Usar como plantilla»).
        </div>
      {/if}
    {/if}

    {#if form.mode === 'github'}
      <RepoPicker
        {accounts}
        bind:accountId={form.account}
        bind:remote={form.git_remote}
        bind:branch={form.git_branch}
        onpick={onPick}
      />
    {/if}

    {#if form.mode === 'import'}
      <ImportPicker bind:selection={form.upload} disabled={saving} onname={(n) => !form.nameTouched && (form.name = n)} />
    {/if}

    <label class="flex flex-col gap-1.5">
      <span class="label">Nombre del proyecto</span>
      <input
        class="input"
        bind:value={form.name}
        oninput={() => (form.nameTouched = true)}
        required
        maxlength="128"
        placeholder={form.mode === 'github' ? 'Se rellena al elegir el repositorio' : form.mode === 'import' ? 'Se rellena al elegir el .zip o la carpeta' : form.mode === 'template' ? 'Ej. Carátula — Informe final' : 'Mi informe'}
      />
    </label>

    {#if importProgress != null}
      <div class="flex flex-col gap-1" role="status">
        <div class="h-2 overflow-hidden rounded-full bg-slate-200">
          <div class="h-full bg-violet-600 transition-[width]" style="width: {Math.round(importProgress * 100)}%"></div>
        </div>
        <p class="text-xs text-slate-500">{importProgress < 1 ? `Subiendo… ${Math.round(importProgress * 100)} %` : 'Creando el proyecto…'}</p>
      </div>
    {/if}

    <div class="flex justify-end gap-2 pt-2">
      <button type="button" class="btn-secondary" onclick={() => (modalOpen = false)} disabled={saving}>Cancelar</button>
      <button class="btn-primary" disabled={saving || (form.mode === 'github' && !accounts.length) || (form.mode === 'template' && !form.template) || (form.mode === 'import' && !form.upload)}>
        {saving ? (form.mode === 'github' ? 'Cargando repositorio…' : form.mode === 'import' ? 'Importando…' : 'Creando…') : form.mode === 'github' ? 'Cargar proyecto' : form.mode === 'import' ? 'Importar' : 'Crear'}
      </button>
    </div>
  </form>
</Modal>

<Modal open={cloneOpen} title={cloneForm.fromTemplate ? 'Nuevo proyecto desde plantilla' : 'Duplicar proyecto'} onclose={() => !cloning && (cloneOpen = false)}>
  <form class="flex flex-col gap-4" onsubmit={submitClone}>
    {#if cloneError}<div class="alert-error">{cloneError}</div>{/if}
    {#if cloneForm.source}
      <div class="flex items-center gap-3 rounded-lg border border-slate-200 bg-slate-50 p-2.5">
        <Thumb projectId={cloneForm.source.id} alt="" class="h-16 w-14 shrink-0 rounded border border-slate-200" />
        <div class="min-w-0">
          <p class="truncate text-sm font-semibold">{cloneForm.source.name}</p>
          <p class="text-xs text-slate-500">
            {cloneForm.fromTemplate ? 'Se copiarán los archivos de la plantilla a un proyecto nuevo tuyo.' : 'Se copiarán todos los archivos a un proyecto nuevo tuyo, sin repositorio remoto ni colaboradores.'}
          </p>
        </div>
      </div>
    {/if}
    <label class="flex flex-col gap-1.5">
      <span class="label">Nombre del nuevo proyecto</span>
      <!-- svelte-ignore a11y_autofocus -->
      <input class="input" bind:value={cloneForm.name} required maxlength="128" autofocus placeholder="Ej. Carátula — Informe final" />
    </label>
    {#if folders.length}
      <label class="flex flex-col gap-1.5">
        <span class="label">Carpeta</span>
        <select class="input" bind:value={cloneForm.folder}>
          <option value={0}>Sin carpeta</option>
          {#each folders as f (f.id)}<option value={f.id}>{f.name}</option>{/each}
        </select>
      </label>
    {/if}
    <div class="flex justify-end gap-2 pt-2">
      <button type="button" class="btn-secondary" onclick={() => (cloneOpen = false)} disabled={cloning}>Cancelar</button>
      <button class="btn-primary" disabled={cloning || !cloneForm.name.trim()}>{cloning ? 'Copiando…' : 'Crear proyecto'}</button>
    </div>
  </form>
</Modal>

<Modal open={tplOpen} title="Plantilla" onclose={() => !tplSaving && (tplOpen = false)}>
  <form class="flex flex-col gap-4" onsubmit={submitTemplate}>
    {#if tplError}<div class="alert-error">{tplError}</div>{/if}
    <label class="flex items-start gap-3 rounded-lg border border-slate-200 p-3">
      <input type="checkbox" bind:checked={tplForm.is_template} class="mt-0.5 size-4 accent-violet-700" />
      <span class="text-sm">
        <span class="font-semibold">Publicar «{tplForm.project?.name}» como plantilla</span>
        <span class="block text-slate-600">Todos los usuarios la verán en «Plantillas» y podrán crear proyectos a partir de ella. Solo podrán editarla quienes ya tengan acceso.</span>
      </span>
    </label>
    {#if tplForm.is_template}
      <label class="flex flex-col gap-1.5">
        <span class="label">Categoría</span>
        <input class="input" bind:value={tplForm.template_category} maxlength="64" list="tpl-categories" placeholder="Carátulas, Informes, Oficios…" />
        <datalist id="tpl-categories">{#each templateCategories as c}<option value={c}></option>{/each}</datalist>
      </label>
      <label class="flex flex-col gap-1.5">
        <span class="label">Descripción (opcional)</span>
        <textarea class="input !h-20 py-2" bind:value={tplForm.template_description} maxlength="255" placeholder="Para qué sirve y qué hay que cambiar al usarla"></textarea>
      </label>
    {/if}
    <div class="flex justify-end gap-2 pt-2">
      <button type="button" class="btn-secondary" onclick={() => (tplOpen = false)} disabled={tplSaving}>Cancelar</button>
      <button class="btn-primary" disabled={tplSaving}>{tplSaving ? 'Guardando…' : 'Guardar'}</button>
    </div>
  </form>
</Modal>
