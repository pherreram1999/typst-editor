<script>
  import { onMount, onDestroy } from 'svelte'
  import { fly } from 'svelte/transition'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import { d } from '../lib/motion.js'
  import Modal from '../lib/Modal.svelte'
  import { CollabSession } from '../lib/editor/collab-client.js'
  import { relativePath, figureLabel, mediaSnippet, resolvePath, escapeString } from '../lib/editor/snippets.js'
  import EditorToolbar from '../lib/editor/EditorToolbar.svelte'
  import * as cmd from '../lib/editor/commands.js'
  import Preview from '../lib/workspace/Preview.svelte'
  import PdfView from '../lib/workspace/PdfView.svelte'
  import ProblemsPanel from '../lib/workspace/ProblemsPanel.svelte'
  import ShareModal from '../lib/workspace/ShareModal.svelte'
  import FileTree from '../lib/workspace/FileTree.svelte'
  import OptimizeModal from '../lib/workspace/OptimizeModal.svelte'
  import ImageModal from '../lib/workspace/ImageModal.svelte'
  import { imageURL } from '../lib/workspace/imageCache.js'
  import RepoPicker from '../lib/RepoPicker.svelte'

  let { id } = $props()

  let project = $state(null)
  let files = $state([])
  let loadError = $state('')
  let currentPath = $state('')
  let status = $state('connecting') // connecting | online | offline | closed
  let users = $state([])
  let refreshKey = $state(0)
  let tab = $state('preview') // preview | pdf
  let pane = $state('editor') // en pantallas pequeñas: editor | output
  let showFiles = $state(true)
  let preview = $state() // componente de la vista previa (reveal)
  let compileResult = $state(null) // última compilación de la vista previa (diagnósticos)
  let rightWidth = $state(45) // % del ancho para la vista previa
  // En pantallas grandes (lg) el panel de salida siempre está a la vista; en
  // las pequeñas solo cuando se elige. Oculto, no se compila la vista previa.
  const lgQuery = matchMedia('(min-width: 64rem)')
  let wide = $state(lgQuery.matches)
  const onLg = (e) => (wide = e.matches)
  lgQuery.addEventListener('change', onLg)
  const outputVisible = $derived(wide || pane === 'output')
  let host = $state()
  let collab = null
  let pendingGoto = null
  let shareOpen = $state(false)
  let membersKey = $state(0)

  // ---------- avisos ----------
  let toasts = $state([])
  let toastSeq = 0
  function toast(msg, kind = 'info') {
    const t = { id: ++toastSeq, msg, kind }
    toasts = [...toasts, t]
    setTimeout(() => (toasts = toasts.filter((x) => x.id !== t.id)), kind === 'error' ? 8000 : 4500)
  }

  // ---------- carga ----------
  async function loadProject() {
    project = await api.getProject(id)
  }
  async function loadFiles() {
    const r = await api.listFiles(id)
    files = r.files
    if (currentPath && !files.some((f) => f.path === currentPath)) currentPath = ''
    if (!currentPath) currentPath = files.some((f) => f.path === project.main_file) ? project.main_file : (files.find((f) => f.editable)?.path ?? '')
  }

  onMount(async () => {
    try {
      await loadProject()
      await loadFiles()
    } catch (e) {
      loadError = e.message
    }
  })

  // ---------- sesión colaborativa del archivo abierto ----------
  $effect(() => {
    const path = currentPath
    if (!host || !path) return
    host.replaceChildren()
    status = 'connecting'
    const s = new CollabSession({
      projectId: id,
      path,
      parent: host,
      onState: (st) => (fmt = cmd.activeFormats(st)),
      onImage: () => insertImage(),
      // Tarjeta sobre image("…"): miniatura y acciones para cambiarla.
      imageHandlers: {
        resolve: (rel) => {
          const p = resolvePath(path, rel)
          const f = files.find((x) => x.path === p)
          return { path: p, exists: !!f, url: f ? imageURL(id, p, f.size) : null }
        },
        onEdit: ({ from, to, rel }, how) => {
          const target = { mode: 'replace', path, from: s.track(from), to: s.track(to), current: resolvePath(path, rel) }
          if (how === 'upload') uploadImage(target)
          else openImageDialog(target)
        },
      },
      // Clic en el código: llevar la vista previa a ese punto.
      // Archivos soltados en el código: se pregunta la carpeta.
      onFilesDrop: (list, at) => {
        editorDrag = 0
        if (!uploader) return s.untrack(at)
        uploader.addFiles(list, { folder: suggestFolder(list), insert: { path, at } })
      },
      onCursorClick: ({ line }) => {
        if (tab === 'preview' && outputVisible) preview?.reveal(path, line)
      },
      onStatus: (st, detail, revoked) => {
        if (st === 'closed' && revoked) {
          toast(detail || 'Ya no tienes acceso a este proyecto', 'error')
          setTimeout(() => (location.hash = '#/'), 1500)
          return
        }
        status = st
        if (st === 'online' && pendingGoto) {
          s.goto(pendingGoto.line, pendingGoto.column)
          pendingGoto = null
        }
        if (st === 'closed') {
          toast(detail || 'El archivo se cerró', 'error')
          loadFiles().catch(() => {})
        }
      },
      onMessage: (msg) => {
        if (msg.type === 'presence') users = msg.users
        else if (msg.type === 'members') {
          membersKey++
          loadProject().catch(() => {})
        }
        else if (msg.type === 'rev') {
          refreshKey++
          if (msg.files) {
            loadFiles().catch(() => {})
            loadProject().catch(() => {})
          }
        }
      },
      onWarning: (m) => toast(m, 'error'),
    })
    collab = s
    return () => {
      s.destroy()
      if (collab === s) collab = null
    }
  })

  // Usuarios conectados (uno por usuario; se indica el archivo que editan).
  const people = $derived.by(() => {
    const m = new Map()
    for (const u of users) {
      const prev = m.get(u.user_id)
      if (!prev) m.set(u.user_id, { ...u, paths: [u.path] })
      else if (!prev.paths.includes(u.path)) prev.paths.push(u.path)
    }
    return [...m.values()]
  })
  const initials = (n) => n.split(/\s+/).map((w) => w[0]).slice(0, 2).join('').toUpperCase()

  const current = $derived(files.find((f) => f.path === currentPath))
  const canManage = $derived(project && (session.user.role === 'admin' || session.user.id === project.owner_id))
  const pdfName = $derived((project?.main_file ?? 'documento.typ').split('/').pop().replace(/\.typ$/, '') + '.pdf')

  function openFile(f) {
    if (f.editable) {
      currentPath = f.path
      pane = 'editor'
    } else openRaw(f)
  }

  async function openRaw(f) {
    try {
      const { blob } = await api.rawFile(id, f.path)
      const url = URL.createObjectURL(blob)
      window.open(url, '_blank', 'noopener')
      setTimeout(() => URL.revokeObjectURL(url), 60000)
    } catch (e) {
      toast(e.message, 'error')
    }
  }

  function onDiagnostic(dg) {
    if (dg.file === currentPath) {
      collab?.goto(dg.line, dg.column)
      pane = 'editor'
    } else if (files.some((f) => f.path === dg.file && f.editable)) {
      pendingGoto = dg
      currentPath = dg.file
      pane = 'editor'
    }
  }

  // ---------- archivos ----------
  let newFileOpen = $state(false)
  let newFilePath = $state('')
  let fileError = $state('')

  async function createFile(e) {
    e.preventDefault()
    fileError = ''
    try {
      let p = newFilePath.trim()
      if (p && !p.includes('.')) p += '.typ'
      const f = await api.createFile(id, p)
      newFileOpen = false
      await loadFiles()
      currentPath = f.path
    } catch (e) {
      fileError = e.message
    }
  }

  let uploader = $state() // UploadModal (openUpload / openReplace)
  // Uppy pesa ~280 KB: se carga aparte, después de abrir el proyecto.
  const uploadModal = import('../lib/workspace/UploadModal.svelte')
  let editorDrag = $state(0) // >0 mientras se arrastran archivos sobre el editor
  let fmt = $state({}) // formato bajo el cursor (barra de herramientas)

  // ---------- imágenes: insertar (barra, Ctrl+Alt+I) o cambiar (tarjeta sobre image("…")) ----------
  let imageDialog = $state() // ImageModal
  let imageTarget = null // { mode: 'insert', path, at } | { mode: 'replace', path, from, to, current }

  function releaseTarget(t) {
    if (!t || !collab) return
    for (const k of ['at', 'from', 'to']) if (t[k]) collab.untrack(t[k])
  }
  function openImageDialog(target) {
    releaseTarget(imageTarget)
    imageTarget = target
    imageDialog?.openFor(target)
  }
  function insertImage() {
    const s = collab
    if (!s?.view) return
    openImageDialog({ mode: 'insert', path: s.path, at: s.track(s.cursor) })
  }
  // Pasar a la subida (UploadModal), que al terminar llama a insertMedia.
  function uploadImage(target) {
    imageTarget = null // ahora la posición es de la subida (se libera con onrelease)
    const ctx = target.mode === 'replace' ? { path: target.path, replace: { from: target.from, to: target.to } } : { path: target.path, at: target.at }
    const folder = target.mode === 'replace' && target.current.includes('/') ? target.current.slice(0, target.current.lastIndexOf('/')) : suggestFolder([{ type: 'image/png' }])
    if (!uploader) return releaseTarget(target)
    uploader.openInsertImage({ folder, insert: ctx })
  }
  // Imagen elegida del proyecto en el diálogo.
  function chooseImage({ path: chosen, caption }) {
    const t = imageTarget
    imageTarget = null
    if (!t) return
    if (t.mode === 'replace') insertMedia({ ctx: { path: t.path, replace: { from: t.from, to: t.to } }, items: [{ path: chosen }] })
    else insertMedia({ ctx: { path: t.path, at: t.at }, items: [{ path: chosen, caption }] })
  }
  function onTool(id, arg) {
    const s = collab
    const v = s?.view
    if (!v) return
    switch (id) {
      case 'undo': return cmd.runUndo(v)
      case 'redo': return cmd.runRedo(v)
      case 'bold': return cmd.toggleMark(v, '*')
      case 'italic': return cmd.toggleMark(v, '_')
      case 'code': return cmd.toggleMark(v, '`')
      case 'underline': return cmd.toggleFunc(v, 'underline')
      case 'heading': return cmd.setHeading(v, arg ?? 1)
      case 'bullets':
      case 'numbered': return cmd.toggleList(v, id)
      case 'image': return insertImage()
      case 'figure': return s.insertBlock(s.cursor, cmd.FIGURE.text.replace('fig-nueva', figureLabel('nueva', s.text)), cmd.FIGURE.select)
      case 'table': return s.insertBlock(s.cursor, cmd.TABLE.text, cmd.TABLE.select)
      case 'math': return cmd.insertMath(v)
      case 'link': return cmd.insertLink(v)
      case 'ref': return cmd.insertRef(v, arg)
      case 'search': return cmd.runSearch(v)
    }
  }

  // Carpeta propuesta para archivos soltados en el editor: para imágenes,
  // una carpeta de imágenes que ya exista; si no, la del archivo abierto.
  function suggestFolder(list) {
    const dirs = new Set(files.map((f) => (f.path.includes('/') ? f.path.slice(0, f.path.lastIndexOf('/')) : '')))
    if (list.every((f) => f.type.startsWith('image/'))) {
      const known = ['media', 'imagenes', 'imágenes', 'images', 'img', 'figuras', 'figures', 'assets']
      const hit = [...dirs].filter((d) => known.includes(d.split('/').pop().toLowerCase())).sort((a, b) => a.length - b.length)[0]
      if (hit) return hit
    }
    return currentPath.includes('/') ? currentPath.slice(0, currentPath.lastIndexOf('/')) : ''
  }
  // Medios soltados en el editor y ya subidos: insertarlos donde se soltaron.
  // ctx: donde se soltaron en el editor; sin ctx, en el cursor del archivo abierto.
  function insertMedia({ ctx, items }) {
    const s = collab
    const path = ctx?.path ?? s?.path
    if (!s || s.path !== path || !path.endsWith('.typ')) {
      toast('No hay un archivo .typ abierto donde insertar la imagen: inserta la referencia a mano', 'error')
      return
    }
    // Cambiar la ruta de un image("…") existente.
    if (ctx?.replace) {
      const { from, to } = ctx.replace
      s.replaceRange(from.pos, to.pos, escapeString(relativePath(path, items[0].path)))
      s.untrack(from)
      s.untrack(to)
      toast(`Imagen cambiada por ${items[0].path}`)
      return
    }
    let text = s.text
    const parts = items.map((it) => {
      const label = figureLabel(it.path, text)
      text += `<${label}>` // etiquetas distintas entre las de esta misma inserción
      return mediaSnippet(relativePath(path, it.path), it.caption, label)
    })
    const joined = parts.map((x) => x.text).join('\n\n')
    s.insertBlock(ctx ? ctx.at.pos : s.cursor, joined, [joined.length, joined.length])
    if (ctx) s.untrack(ctx.at)
    pane = 'editor'
  }

  const dragHasFiles = (e) => [...(e.dataTransfer?.types ?? [])].includes('Files')

  // Soltar archivos fuera de las zonas previstas no debe abrirlos en el navegador.
  $effect(() => {
    const stop = (e) => dragHasFiles(e) && e.preventDefault()
    const reset = () => (editorDrag = 0)
    window.addEventListener('dragover', stop)
    window.addEventListener('drop', stop)
    window.addEventListener('drop', reset, true)
    window.addEventListener('dragend', reset)
    return () => {
      window.removeEventListener('dragover', stop)
      window.removeEventListener('drop', stop)
      window.removeEventListener('drop', reset, true)
      window.removeEventListener('dragend', reset)
    }
  })
  let optimizeOpen = $state(false)

  async function removeFile(f) {
    if (!confirm(`¿Eliminar «${f.path}»? Se borrará para todos los usuarios.`)) return
    try {
      await api.deleteFile(id, f.path)
      if (currentPath === f.path) currentPath = ''
      await loadFiles()
    } catch (e) {
      toast(e.message, 'error')
    }
  }


  // ---------- git ----------
  let gitBusy = $state('') // '' | pull | push
  let pushOpen = $state(false)
  let pushMessage = $state('')
  let gitStatus = $state(null)
  let pushError = $state('')

  async function pull() {
    if (!project.git_remote) {
      openSettings()
      toast('Configura primero el repositorio remoto', 'error')
      return
    }
    gitBusy = 'pull'
    try {
      const r = await api.gitPull(id)
      toast(r.message)
    } catch (e) {
      toast(e.message, 'error')
    } finally {
      gitBusy = ''
    }
  }

  async function openPush() {
    if (!project.git_remote) {
      openSettings()
      toast('Configura primero el repositorio remoto', 'error')
      return
    }
    pushError = ''
    pushMessage = ''
    gitStatus = null
    pushOpen = true
    try {
      gitStatus = await api.gitStatus(id)
    } catch (e) {
      pushError = e.message
    }
  }

  async function push(e) {
    e.preventDefault()
    pushError = ''
    gitBusy = 'push'
    try {
      const r = await api.gitPush(id, pushMessage)
      pushOpen = false
      toast(r.message)
    } catch (e) {
      pushError = e.message
    } finally {
      gitBusy = ''
    }
  }

  // ---------- ajustes ----------
  let settingsOpen = $state(false)
  let settings = $state({})
  let settingsError = $state('')
  let savingSettings = $state(false)
  let myAccounts = $state([])
  let manualRemote = $state(false)

  async function openSettings() {
    settings = {
      name: project.name,
      main_file: project.main_file,
      git_remote: project.git_remote,
      git_branch: project.git_branch,
      github_account_id: project.github_account_id ?? 0,
      is_template: project.is_template,
      template_category: project.template_category,
      template_description: project.template_description,
    }
    settingsError = ''
    manualRemote = false
    settingsOpen = true
    try {
      myAccounts = await api.listGitHubAccounts()
    } catch {
      myAccounts = []
    }
  }

  // La cuenta elegida es del usuario actual: se pueden listar sus repositorios.
  const settingsAccountMine = $derived(myAccounts.some((a) => a.id === settings.github_account_id))

  async function saveSettings(e) {
    e.preventDefault()
    settingsError = ''
    savingSettings = true
    try {
      const body = { ...settings }
      project = await api.updateProject(id, body)
      settingsOpen = false
      toast('Ajustes guardados')
    } catch (e) {
      settingsError = e.message
    } finally {
      savingSettings = false
    }
  }

  async function deleteProject() {
    if (!confirm(`¿Eliminar el proyecto «${project.name}» y todos sus archivos? Esta acción no se puede deshacer (el repositorio remoto no se toca).`)) return
    try {
      await api.deleteProject(id)
      location.hash = '#/'
    } catch (e) {
      settingsError = e.message
    }
  }

  // ---------- divisor redimensionable ----------
  let split = $state()
  function startResize(e) {
    e.preventDefault()
    const rect = split.getBoundingClientRect()
    const move = (ev) => {
      rightWidth = Math.min(75, Math.max(20, ((rect.right - ev.clientX) / rect.width) * 100))
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  onDestroy(() => {
    collab?.destroy()
    lgQuery.removeEventListener('change', onLg)
  })

  const statusLabel = { connecting: 'Conectando…', online: 'Sincronizado', offline: 'Sin conexión, reintentando…', closed: 'Cerrado' }
  const statusColor = { connecting: 'bg-amber-500', online: 'bg-emerald-500', offline: 'bg-red-500', closed: 'bg-slate-400' }
</script>

{#if loadError}
  <div class="mx-auto max-w-6xl p-6"><div class="alert-error">{loadError}</div></div>
{:else if !project}
  <p class="p-6 text-center text-sm text-slate-500">Cargando proyecto…</p>
{:else}
  <div class="flex h-[calc(100dvh-3.5rem)] flex-col">
    <!-- Barra superior -->
    <header class="flex min-h-12 shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b border-slate-200 bg-surface px-3 py-1.5">
      <div class="flex min-w-0 items-center gap-2">
        <button
          type="button"
          class="flex size-8 cursor-pointer items-center justify-center rounded-md text-slate-500 hover:bg-slate-100 hover:text-slate-900"
          onclick={() => (showFiles = !showFiles)}
          aria-label="Mostrar u ocultar archivos"
          aria-pressed={showFiles}
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M3 6h18M3 12h18M3 18h18" /></svg>
        </button>
        <a href="#/" class="text-[13px] text-slate-500 no-underline hover:text-primary">Proyectos</a>
        <span class="text-slate-300">/</span>
        <h1 class="truncate text-[15px] font-semibold">{project.name}</h1>
        <span class="flex items-center gap-1.5 whitespace-nowrap text-xs text-slate-500" title={statusLabel[status]}>
          <span class="size-2 rounded-full {statusColor[status]}"></span>
          <span class="hidden sm:inline">{statusLabel[status]}</span>
        </span>
      </div>

      <div class="ml-auto flex items-center gap-3">
        <div class="flex -space-x-1.5" aria-label="Usuarios conectados">
          {#each people as p (p.user_id)}
            <span
              class="flex size-7 items-center justify-center rounded-full text-[11px] font-semibold text-white ring-2 ring-surface"
              style="background-color: {p.color}"
              title="{p.name} — {p.paths.join(', ')}"
              transition:fly={{ y: -4, duration: d(150) }}>{initials(p.name)}</span
            >
          {/each}
        </div>
        <div class="flex items-center gap-1.5">
          <button type="button" class="btn-secondary btn-sm" onclick={() => (shareOpen = true)} title="Invitar usuarios y ver quién tiene acceso">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M19 8v6M22 11h-6" /></svg>
            Compartir{#if project.members > 0}<span class="badge !bg-primary-soft !px-1.5 !py-0 text-primary">{project.members}</span>{/if}
          </button>
          <button type="button" class="btn-secondary btn-sm" onclick={pull} disabled={!!gitBusy} title="Traer cambios del repositorio remoto">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 4v12M6 10l6 6 6-6M5 20h14" /></svg>
            {gitBusy === 'pull' ? 'Pull…' : 'Pull'}
          </button>
          <button type="button" class="btn-primary btn-sm" onclick={openPush} disabled={!!gitBusy} title="Confirmar y enviar al repositorio remoto">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20V8M6 14l6-6 6 6M5 4h14" /></svg>
            Push
          </button>
          <button
            type="button"
            class="flex size-9 cursor-pointer items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100 hover:text-slate-900"
            onclick={openSettings}
            aria-label="Ajustes del proyecto"
            title="Ajustes del proyecto"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" /></svg>
          </button>
        </div>
      </div>
    </header>

    <!-- Selector de panel en pantallas pequeñas -->
    <div class="flex shrink-0 border-b border-slate-200 bg-surface lg:hidden" role="tablist">
      {#each [['editor', 'Editor'], ['output', 'Vista previa / PDF']] as [k, label]}
        <button
          type="button"
          role="tab"
          aria-selected={pane === k}
          class="flex-1 cursor-pointer border-b-2 py-2 text-[13px] font-medium {pane === k ? 'border-primary text-primary' : 'border-transparent text-slate-500'}"
          onclick={() => (pane = k)}>{label}</button
        >
      {/each}
    </div>

    <div class="flex min-h-0 flex-1" bind:this={split}>
      <!-- Archivos -->
      {#if showFiles}
        <aside class="flex w-60 shrink-0 flex-col border-r border-slate-200 bg-slate-50 {pane === 'editor' ? '' : 'max-lg:hidden'}">
          <div class="flex items-center justify-between px-3 pt-3 pb-2">
            <span class="text-xs font-semibold tracking-wide text-slate-500 uppercase">Archivos</span>
            <div class="flex gap-1">
              <button
                type="button"
                class="flex size-7 cursor-pointer items-center justify-center rounded-md text-slate-500 hover:bg-slate-200 hover:text-slate-900"
                onclick={() => {
                  newFilePath = ''
                  fileError = ''
                  newFileOpen = true
                }}
                title="Nuevo archivo"
                aria-label="Nuevo archivo"
              >
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
              </button>
              <button
                type="button"
                class="flex size-7 cursor-pointer items-center justify-center rounded-md text-slate-500 hover:bg-slate-200 hover:text-slate-900"
                onclick={() => uploader?.openUpload()}
                title="Subir archivos (imágenes, fuentes, .bib…); también puedes soltarlos aquí"
                aria-label="Subir archivos"
              >
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12" /></svg>
              </button>
              <button
                type="button"
                class="flex size-7 cursor-pointer items-center justify-center rounded-md text-slate-500 hover:bg-slate-200 hover:text-slate-900"
                onclick={() => (optimizeOpen = true)}
                title="Optimizar imágenes sin pérdida"
                aria-label="Optimizar imágenes"
              >
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m12 3 1.9 4.6L18.5 9.5l-4.6 1.9L12 16l-1.9-4.6L5.5 9.5l4.6-1.9zM19 15l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8zM5 3l.6 1.4L7 5l-1.4.6L5 7l-.6-1.4L3 5l1.4-.6z" /></svg>
              </button>
            </div>
          </div>
          <FileTree
            projectId={id}
            {files}
            {currentPath}
            mainFile={project.main_file}
            peopleIn={(path) => people.filter((p) => p.paths.includes(path) && p.user_id !== session.user.id)}
            onopen={openFile}
            onremove={removeFile}
            onreplace={(f) => uploader?.openReplace(f.path)}
            ondropfiles={(list, folder) => uploader?.addFiles(list, { folder, autostart: true })}
          />
        </aside>
      {/if}

      <!-- Editor -->
      <section
        class="relative flex min-w-0 flex-1 flex-col bg-surface {pane === 'editor' ? '' : 'max-lg:hidden'}"
        ondragenter={(e) => dragHasFiles(e) && editorDrag++}
        ondragleave={(e) => dragHasFiles(e) && (editorDrag = Math.max(0, editorDrag - 1))}
        role="presentation"
      >
        {#if editorDrag > 0}
          <div class="pointer-events-none absolute inset-2 z-20 flex items-center justify-center rounded-xl border-2 border-dashed border-primary bg-primary-tint/70">
            <div class="rounded-lg bg-surface px-4 py-3 text-center text-sm shadow-lg">
              <p class="font-semibold text-primary">Suelta para subir los archivos</p>
              <p class="text-xs text-slate-500">Te preguntaré en qué carpeta guardarlos</p>
            </div>
          </div>
        {/if}
        <EditorToolbar
          file={current && !current.editable ? `${currentPath} (no editable)` : currentPath}
          active={fmt}
          disabled={!current?.editable || status !== 'online'}
          labels={() => cmd.labelsIn(collab?.text ?? '')}
          onaction={onTool}
        />
        <div class="relative min-h-0 flex-1">
          <div bind:this={host} class="absolute inset-0"></div>
          {#if !currentPath}
            <p class="absolute inset-0 flex items-center justify-center text-sm text-slate-500">Selecciona un archivo para editarlo.</p>
          {/if}
        </div>
      </section>

      <!-- Divisor -->
      <div
        class="hidden w-1.5 shrink-0 cursor-col-resize bg-slate-200 transition-colors hover:bg-primary/40 lg:block"
        onpointerdown={startResize}
        role="separator"
        aria-orientation="vertical"
        aria-label="Redimensionar"
      ></div>

      <!-- Vista previa / PDF -->
      <section
        class="flex min-w-0 flex-col max-lg:flex-1 {pane === 'output' ? '' : 'max-lg:hidden'}"
        style="--w: {rightWidth}%"
      >
        <div class="flex h-10 shrink-0 items-end gap-1 border-b border-slate-200 bg-slate-50 px-2" role="tablist">
          {#each [['preview', 'Vista previa'], ['pdf', 'PDF']] as [k, label]}
            <button
              type="button"
              role="tab"
              aria-selected={tab === k}
              class="cursor-pointer rounded-t-md border border-b-0 px-3 py-1.5 text-[13px] font-medium {tab === k
                ? 'border-slate-200 bg-surface text-primary'
                : 'border-transparent text-slate-500 hover:text-slate-900'}"
              onclick={() => (tab = k)}>{label}</button
            >
          {/each}
        </div>
        <div class="min-h-0 flex-1">
          <div class="h-full {tab === 'preview' ? '' : 'hidden'}">
            <Preview bind:this={preview} projectId={id} {refreshKey} active={outputVisible} onresult={(r) => (compileResult = r)} onjump={onDiagnostic} />
          </div>
          <div class="h-full {tab === 'pdf' ? '' : 'hidden'}">
            <PdfView projectId={id} {refreshKey} active={tab === 'pdf' && outputVisible} filename={pdfName} />
          </div>
        </div>
        <ProblemsPanel result={compileResult} ondiagnostic={onDiagnostic} />
      </section>
    </div>
  </div>

  <!-- Avisos -->
  <div class="pointer-events-none fixed right-4 bottom-4 z-40 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" aria-live="polite">
    {#each toasts as t (t.id)}
      <div
        class="pointer-events-auto rounded-lg border px-4 py-3 text-sm shadow-lg {t.kind === 'error'
          ? 'border-red-200 bg-red-50 text-red-800'
          : 'border-slate-200 bg-surface text-slate-800'}"
        transition:fly={{ y: 12, duration: d(180) }}
      >
        {t.msg}
      </div>
    {/each}
  </div>

  {#await uploadModal then { default: UploadModal }}
    <UploadModal
      bind:this={uploader}
      projectId={id}
      {files}
      onuploaded={() => loadFiles().catch(() => {})}
      ontoast={(m, kind) => toast(m, kind)}
      oninsert={insertMedia}
      editingFile={currentPath.endsWith('.typ') ? currentPath : null}
      onrelease={(ctx) => {
        if (ctx.at) collab?.untrack(ctx.at)
        if (ctx.replace) releaseTarget(ctx.replace)
      }}
    />
  {/await}
  <ImageModal
    bind:this={imageDialog}
    projectId={id}
    {files}
    onchoose={chooseImage}
    onupload={() => imageTarget && uploadImage(imageTarget)}
    onclose={() => {
      releaseTarget(imageTarget)
      imageTarget = null
    }}
  />
  <OptimizeModal
    projectId={id}
    {files}
    open={optimizeOpen}
    onclose={() => (optimizeOpen = false)}
    ondone={() => loadFiles().catch(() => {})}
  />
  <ShareModal open={shareOpen} {project} {canManage} refreshKey={membersKey} onclose={() => (shareOpen = false)} />

  <Modal open={newFileOpen} title="Nuevo archivo" onclose={() => (newFileOpen = false)}>
    <form class="flex flex-col gap-4" onsubmit={createFile}>
      {#if fileError}<div class="alert-error">{fileError}</div>{/if}
      <label class="flex flex-col gap-1.5">
        <span class="label">Ruta</span>
        <input class="input font-mono" bind:value={newFilePath} required placeholder="capitulos/intro.typ" />
        <span class="text-xs text-slate-500">Texto: .typ, .bib, .yml, .toml, .csv, .json… Usa «/» para carpetas.</span>
      </label>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn-secondary" onclick={() => (newFileOpen = false)}>Cancelar</button>
        <button class="btn-primary">Crear</button>
      </div>
    </form>
  </Modal>

  <Modal open={pushOpen} title="Push a GitHub" onclose={() => gitBusy !== 'push' && (pushOpen = false)}>
    <form class="flex flex-col gap-4" onsubmit={push}>
      {#if pushError}<div class="alert-error">{pushError}</div>{/if}
      <p class="text-[13px] text-slate-600">
        Se hará commit de todos los cambios y se enviarán a
        <strong class="font-mono">{project.git_branch}</strong> en <span class="break-all">{project.git_remote}</span>.
      </p>
      {#if gitStatus}
        <div class="rounded-lg border border-slate-200 bg-slate-50 p-3 text-[13px]">
          {#if gitStatus.changes.length}
            <p class="mb-1 font-medium">{gitStatus.changes.length} {gitStatus.changes.length === 1 ? 'archivo modificado' : 'archivos modificados'}</p>
            <ul class="max-h-32 overflow-y-auto font-mono text-xs text-slate-600">
              {#each gitStatus.changes as c}<li>{c}</li>{/each}
            </ul>
          {:else}
            <p class="text-slate-600">No hay cambios sin confirmar.</p>
          {/if}
          {#if gitStatus.tracking && (gitStatus.ahead || gitStatus.behind)}
            <p class="mt-2 text-slate-600">
              {gitStatus.ahead} commit(s) por enviar · {gitStatus.behind} por traer
            </p>
          {/if}
          {#if gitStatus.last_commit}
            <p class="mt-2 truncate text-xs text-slate-500" title={gitStatus.last_commit}>Último: {gitStatus.last_commit}</p>
          {/if}
        </div>
      {:else if !pushError}
        <p class="text-sm text-slate-500">Leyendo estado…</p>
      {/if}
      <label class="flex flex-col gap-1.5">
        <span class="label">Mensaje del commit</span>
        <textarea class="input !h-20 py-2" bind:value={pushMessage} maxlength="2000" placeholder="Actualización desde Typst Editor"></textarea>
      </label>
      {#if !project.has_token}
        <p class="text-xs text-amber-700">El proyecto no tiene cuenta de GitHub asignada: el push fallará. Asígnale una en los ajustes.</p>
      {:else}
        <p class="text-xs text-slate-500">Se usarán las credenciales de la cuenta de GitHub <strong>{project.github_login}</strong>.</p>
      {/if}
      <div class="flex justify-end gap-2">
        <button type="button" class="btn-secondary" onclick={() => (pushOpen = false)} disabled={gitBusy === 'push'}>Cancelar</button>
        <button class="btn-primary" disabled={gitBusy === 'push'}>{gitBusy === 'push' ? 'Enviando…' : 'Commit y push'}</button>
      </div>
    </form>
  </Modal>

  <Modal open={settingsOpen} title="Ajustes del proyecto" wide onclose={() => !savingSettings && (settingsOpen = false)}>
    <form class="flex flex-col gap-4" onsubmit={saveSettings}>
      {#if settingsError}<div class="alert-error">{settingsError}</div>{/if}
      {#if !canManage}
        <div class="alert-info">Solo el propietario ({project.owner_name}) o un administrador puede cambiar los ajustes.</div>
      {/if}
      <fieldset class="flex flex-col gap-4" disabled={!canManage}>
        <label class="flex flex-col gap-1.5">
          <span class="label">Nombre</span>
          <input class="input" bind:value={settings.name} required maxlength="128" />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="label">Archivo principal</span>
          <select class="input" bind:value={settings.main_file}>
            {#each files.filter((f) => f.path.endsWith('.typ')) as f}
              <option value={f.path}>{f.path}</option>
            {/each}
          </select>
        </label>
        <div class="border-t border-slate-200 pt-4">
          <label class="flex items-start gap-3">
            <input type="checkbox" bind:checked={settings.is_template} class="mt-0.5 size-4 accent-violet-700" />
            <span class="text-sm">
              <span class="font-semibold text-slate-800">Usar como plantilla</span>
              <span class="block text-slate-600">Aparece en «Plantillas» para todos los usuarios, que pueden crear proyectos a partir de una copia.</span>
            </span>
          </label>
          {#if settings.is_template}
            <div class="mt-3 grid gap-3 sm:grid-cols-[1fr_2fr]">
              <label class="flex flex-col gap-1.5">
                <span class="label">Categoría</span>
                <input class="input" bind:value={settings.template_category} maxlength="64" placeholder="Carátulas" />
              </label>
              <label class="flex flex-col gap-1.5">
                <span class="label">Descripción</span>
                <input class="input" bind:value={settings.template_description} maxlength="255" placeholder="Para qué sirve" />
              </label>
            </div>
          {/if}
        </div>
        <div class="border-t border-slate-200 pt-4">
          <p class="mb-3 text-[13px] font-semibold text-slate-800">Sincronización con GitHub</p>
          <div class="flex flex-col gap-4">
            <label class="flex flex-col gap-1.5">
              <span class="label">Cuenta de GitHub (credenciales para pull/push)</span>
              <select class="input" bind:value={settings.github_account_id}>
                <option value={0}>Sin cuenta</option>
                {#each myAccounts as a (a.id)}
                  <option value={a.id}>{a.login}{a.label ? ` — ${a.label}` : ''}</option>
                {/each}
                {#if project.github_account_id && !project.github_account_mine}
                  <option value={project.github_account_id}>{project.github_login} (de otro usuario)</option>
                {/if}
              </select>
              {#if !myAccounts.length}
                <span class="text-xs text-slate-500">Registra tus cuentas en <a href="#/github">GitHub</a>.</span>
              {/if}
            </label>
            {#if settingsAccountMine && !manualRemote}
              <RepoPicker
                accounts={myAccounts}
                showAccount={false}
                bind:accountId={settings.github_account_id}
                bind:remote={settings.git_remote}
                bind:branch={settings.git_branch}
              />
              <button type="button" class="self-start text-xs text-primary underline" onclick={() => (manualRemote = true)}>
                Escribir la URL a mano
              </button>
            {:else}
              <label class="flex flex-col gap-1.5">
                <span class="label">URL HTTPS del repositorio</span>
                <input class="input" bind:value={settings.git_remote} placeholder="https://github.com/usuario/repo.git" />
              </label>
              <label class="flex flex-col gap-1.5">
                <span class="label">Rama</span>
                <input class="input" bind:value={settings.git_branch} required />
              </label>
              {#if settingsAccountMine}
                <button type="button" class="self-start text-xs text-primary underline" onclick={() => (manualRemote = false)}>
                  Elegir de mis repositorios
                </button>
              {/if}
            {/if}
          </div>
        </div>
      </fieldset>
      <div class="flex flex-wrap items-center justify-between gap-2 pt-2">
        {#if canManage}
          <button type="button" class="btn-secondary !border-red-200 !text-red-700 hover:!bg-red-50" onclick={deleteProject}>Eliminar proyecto</button>
        {:else}
          <span></span>
        {/if}
        <div class="flex gap-2">
          <button type="button" class="btn-secondary" onclick={() => (settingsOpen = false)}>Cerrar</button>
          {#if canManage}
            <button class="btn-primary" disabled={savingSettings}>{savingSettings ? 'Guardando…' : 'Guardar'}</button>
          {/if}
        </div>
      </div>
    </form>
  </Modal>
{/if}

<style>
  @media (min-width: 64rem) {
    section[style*='--w'] {
      width: var(--w);
      flex: none;
    }
  }
</style>
