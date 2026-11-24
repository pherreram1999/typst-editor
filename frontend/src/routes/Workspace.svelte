<script>
  import { onMount, onDestroy, flushSync } from 'svelte'
  import { fly } from 'svelte/transition'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import { d } from '../lib/motion.js'
  import Modal from '../lib/Modal.svelte'
  import Drawer from '../lib/Drawer.svelte'
  import Sheet from '../lib/Sheet.svelte'
  import { downloadPdf, downloadZip, sharePdf, canSharePdf, pdfError } from '../lib/workspace/exporting.js'
  import { CollabSession } from '../lib/editor/collab-client.js'
  import { relativePath, figureLabel, mediaSnippet, resolvePath, escapeString } from '../lib/editor/snippets.js'
  import EditorToolbar from '../lib/editor/EditorToolbar.svelte'
  import { savedMargin, saveMargin } from '../lib/editor/marginGuide.js'
  import { spellEnabled, saveSpellEnabled } from '../lib/spell/speller.js'
  import { spellable } from '../lib/editor/spellcheck.js'
  import * as cmd from '../lib/editor/commands.js'
  import Preview from '../lib/workspace/Preview.svelte'
  import PdfView from '../lib/workspace/PdfView.svelte'
  import ProblemsPanel from '../lib/workspace/ProblemsPanel.svelte'
  import ShareModal from '../lib/workspace/ShareModal.svelte'
  import FileTree from '../lib/workspace/FileTree.svelte'
  import OptimizeModal from '../lib/workspace/OptimizeModal.svelte'
  import ImageModal from '../lib/workspace/ImageModal.svelte'
  import ImageLightbox from '../lib/workspace/ImageLightbox.svelte'
  import TableModal from '../lib/workspace/TableModal.svelte'
  import ConflictsDialog from '../lib/git/ConflictsDialog.svelte'
  import { tableAt } from '../lib/editor/tablecode.js'
  import { imageURL } from '../lib/workspace/imageCache.js'
  import RepoPicker from '../lib/RepoPicker.svelte'
  import BibEditor from '../lib/bib/BibEditor.svelte'
  import CiteModal from '../lib/bib/CiteModal.svelte'
  import { citeCompletion } from '../lib/bib/complete.js'
  import { loadLibrary, invalidateText } from '../lib/bib/library.js'

  let { id } = $props()

  let project = $state(null)
  let files = $state([])
  let loadError = $state('')
  let currentPath = $state('')
  let status = $state('connecting') // connecting | online | offline | closed
  let users = $state([])
  let refreshKey = $state(0)
  // Vista previa en el navegador: versión del texto del archivo abierto y
  // cambios en otros archivos (de otros usuarios, subidas, git…).
  let liveVersion = $state(0)
  let remoteKey = $state(0)
  let tab = $state('preview') // preview | pdf
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
  // Pantallas pequeñas (celular, tableta vertical): como Google Docs, el
  // proyecto se abre en modo lectura (la vista previa) y el lápiz pasa a
  // editar; los menús son hojas inferiores.
  const mobile = $derived(!wide)
  let pane = $state(lgQuery.matches ? 'editor' : 'output') // en pantallas pequeñas: editor | output
  const outputVisible = $derived(wide || pane === 'output')
  // En un celular (pantalla táctil y estrecha) la vista previa compila en el
  // servidor: el compilador del navegador pesa ~11 MB y puede usar >1 GB.
  const browserCompile = !(matchMedia('(pointer: coarse)').matches && !lgQuery.matches)
  // Zona visible real: con el teclado abierto es más baja que la ventana (y
  // en iOS además se desplaza); el editor se ajusta a ella para que la barra
  // de formato quede justo encima del teclado.
  let vv = $state({ top: 0, height: window.innerHeight })
  $effect(() => {
    const v = window.visualViewport
    if (!mobile || !v) return
    const upd = () => (vv = { top: v.offsetTop, height: v.height })
    upd()
    v.addEventListener('resize', upd)
    v.addEventListener('scroll', upd)
    return () => {
      v.removeEventListener('resize', upd)
      v.removeEventListener('scroll', upd)
    }
  })
  let menuOpen = $state(false) // hojas inferiores del celular
  let filesOpen = $state(false)
  let problemsOpen = $state(false)
  let host = $state()
  let collab = null
  let live = $state.raw(null) // la misma sesión, reactiva (editor visual de .bib)
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
      readOnly: project?.read_only,
      onState: (st) => (fmt = cmd.activeFormats(st)),
      onDocChange: () => liveVersion++,
      onImage: () => insertImage(),
      onCite: () => openCite(),
      // «@» en el código: claves de la bibliografía, las más recientes primero.
      completeCite: citeCompletion(() => libraryItems()),
      // Tarjeta sobre image("…"): miniatura y acciones para cambiarla.
      imageHandlers: project?.read_only ? null : {
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
      // Tarjeta sobre #table(…): abrir el editor visual de tablas.
      tableHandlers: project?.read_only ? null : { onEdit: ({ from, to }) => editTable(s, from, to) },
      // Clic en el código: llevar la vista previa a ese punto.
      // Archivos soltados en el código: se pregunta la carpeta.
      onFilesDrop: project?.read_only ? null : (list, at) => {
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
          // Invitado: vuelve a la entrada del enlace (Invite.svelte), que lo explica.
          setTimeout(() => (guest ? (session.user = null) : (location.hash = '#/')), 1500)
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
          // Textos guardados para la bibliografía (citas, autocompletado).
          if (msg.files || !msg.path) invalidateText(id)
          else invalidateText(id, msg.path)
          if (msg.files || /\.(bib|typ)$/i.test(msg.path ?? '')) libCache = null
          // Los cambios en el archivo abierto ya llegan por el editor.
          if (msg.files || msg.path !== path) remoteKey++
          if (msg.files) {
            loadFiles().catch(() => {})
            loadProject().catch(() => {})
          }
        }
      },
      onWarning: (m) => toast(m, 'error'),
    })
    collab = s
    live = s
    libCache = null
    return () => {
      s.destroy()
      if (collab === s) collab = null
      if (live === s) live = null
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
  // Invitado de un enlace (#/i/…): sin ajustes, miembros, GitHub ni .zip; con
  // un enlace de solo lectura, además, sin editar, crear ni subir archivos.
  const guest = session.user.role === 'guest'
  const readOnly = $derived(!!project?.read_only)
  // Pull y push: solo el propietario (usan las credenciales de GitHub del proyecto).
  const canGit = $derived(project && session.user.id === project.owner_id)
  const gitDenied = 'Solo el propietario del proyecto puede hacer pull y push'
  const pdfName = $derived((project?.main_file ?? 'documento.typ').split('/').pop().replace(/\.typ$/, '') + '.pdf')

  // ---------- bibliografía ----------
  const isBib = $derived(/\.bib$/i.test(currentPath))
  let bibMode = $state(localStorage.getItem('typsteditor.bibmode') === 'code' ? 'code' : 'visual') // visual | code
  let bibFocus = $state('') // clave a mostrar al abrir el editor visual
  let bibEditor = $state()
  // Editor visual del .bib (no en solo lectura: escribe en el documento).
  const bibVisual = $derived(isBib && bibMode === 'visual' && !readOnly)
  function setBibMode(m) {
    bibMode = m
    try {
      localStorage.setItem('typsteditor.bibmode', m)
    } catch {
      /* sin almacenamiento */
    }
  }
  // Del editor visual al código, en la línea indicada.
  function showBibCode(line) {
    setBibMode('code')
    setTimeout(() => line && collab?.goto(line))
  }

  // Entradas de todos los .bib, para el autocompletado (se recargan con «rev»).
  let libCache = null
  function libraryItems() {
    libCache ??= loadLibrary(id, files, collab).then((r) => r.items)
    libCache.catch(() => (libCache = null))
    return libCache
  }

  let citeDialog = $state()
  let citeTarget = null // { path, at }
  function releaseCite() {
    if (citeTarget) collab?.untrack(citeTarget.at)
    citeTarget = null
  }
  function openCite() {
    const s = collab
    if (!s?.view || isBib) return
    releaseCite()
    citeTarget = { path: s.path, at: s.track(s.cursor) }
    citeDialog?.openFor()
  }
  function insertCite(code) {
    const s = collab
    const t = citeTarget
    citeTarget = null
    if (!s?.view || !t || t.path !== s.path) {
      toast('El archivo cambió: vuelve a insertar la cita', 'error')
      return
    }
    cmd.insertCitation(s.view, t.at.pos, code)
    s.untrack(t.at)
    pane = 'editor'
  }
  // Abrir un .bib en el editor visual (sin ruta: crear referencias.bib).
  async function manageBib(path, key = '') {
    releaseCite()
    try {
      if (!path) {
        const f = await api.createFile(id, 'referencias.bib')
        await loadFiles()
        path = f.path
      }
    } catch (e) {
      toast(e.message, 'error')
      return
    }
    setBibMode('visual')
    pane = 'editor'
    if (currentPath === path && bibEditor) {
      if (key) bibEditor.reveal(key)
      return
    }
    bibFocus = key
    currentPath = path
  }
  // Enlazar un .bib al final del archivo principal abierto.
  function addBibliography(bibPath) {
    const s = collab
    if (!s?.view) return false
    const doc = s.view.state.doc
    const tail = doc.toString().match(/\s*$/)[0]
    const text = `${doc.length ? (tail.includes('\n\n') ? '' : tail.includes('\n') ? '\n' : '\n\n') : ''}#bibliography("${escapeString(relativePath(s.path, bibPath))}")\n`
    s.view.dispatch({ changes: { from: doc.length, insert: text }, userEvent: 'input' })
    toast(`Se añadió #bibliography al final de ${s.path}`)
    return true
  }

  // Imágenes que el navegador sabe mostrar: se abren en el visor (spotlight),
  // recorriéndolas en el orden del explorador.
  const isImage = (p) => /\.(png|jpe?g|gif|svg|webp|avif|bmp)$/i.test(p)
  const imageFiles = $derived(files.filter((f) => isImage(f.path)).sort((a, b) => a.path.localeCompare(b.path)))
  let lightbox = $state()

  function openFile(f) {
    if (f.editable) {
      currentPath = f.path
      pane = 'editor'
      filesOpen = false
    } else if (isImage(f.path)) lightbox?.open(f.path)
    else openRaw(f)
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
    problemsOpen = false
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
  let marginCol = $state(savedMargin()) // guía de margen derecho del editor (0: sin guía)
  let spellOn = $state(spellEnabled()) // revisión ortográfica (español de México)
  function setSpell(on) {
    spellOn = on
    saveSpellEnabled(on)
    collab?.setSpell(on)
  }

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
  // ---------- tablas: insertar (barra) o editar (tarjeta sobre #table, o barra con el cursor dentro) ----------
  let tableDialog = $state() // TableModal
  let tableTarget = null // { mode: 'insert', path, at } | { mode: 'edit', path, from, to, src }

  function releaseTable() {
    releaseTarget(tableTarget)
    tableTarget = null
  }
  function openTableTool(s) {
    const t = s.path.endsWith('.typ') ? tableAt(s.text, s.cursor) : null
    if (t) return editTable(s, t.from, t.to)
    releaseTable()
    tableTarget = { mode: 'insert', path: s.path, at: s.track(s.cursor) }
    tableDialog?.openInsert()
  }
  function editTable(s, from, to) {
    const doc = s.view.state.doc
    const src = doc.sliceString(from, to)
    const line = doc.lineAt(from)
    const indent = /^\s*/.exec(line.text)[0]
    const err = tableDialog?.openEdit(src, indent)
    if (err) return toast(`No se puede editar esta tabla visualmente: ${err}`, 'error')
    releaseTable()
    tableTarget = { mode: 'edit', path: s.path, from: s.track(from), to: s.track(to), src }
  }
  function applyTable({ code }) {
    const t = tableTarget
    tableTarget = null
    const s = collab
    if (!t) return
    if (!s?.view || s.path !== t.path) {
      releaseTarget(t)
      return toast('El archivo de la tabla ya no está abierto: no se aplicaron los cambios', 'error')
    }
    if (t.mode === 'insert') s.insertBlock(t.at.pos, '#' + code, [code.length + 1, code.length + 1])
    else {
      // Otra persona (o tú) pudo editar la tabla mientras el diálogo estaba abierto.
      const now = s.view.state.doc.sliceString(t.from.pos, t.to.pos)
      if (now !== t.src && !confirm('La tabla cambió en el código mientras la editabas. ¿Reemplazarla con tu versión?')) return releaseTarget(t)
      s.replaceRange(t.from.pos, t.to.pos, code)
    }
    releaseTarget(t)
    pane = 'editor'
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
      case 'table': return openTableTool(s)
      case 'math': return cmd.insertMath(v)
      case 'link': return cmd.insertLink(v)
      case 'ref': return cmd.insertRef(v, arg)
      case 'cite': return openCite()
      case 'search': return cmd.runSearch(v)
      case 'margin':
        marginCol = arg
        saveMargin(arg)
        return s.setMargin(arg)
      case 'spell':
        return setSpell(arg)
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


  // ---------- celular: lectura ↔ edición ----------
  // Lápiz: editar en lo que se está leyendo. El foco se da en el mismo toque
  // (si no, iOS no abre el teclado); la posición llega después.
  function startEditing() {
    pane = 'editor'
    flushSync()
    collab?.view?.focus()
    preview?.sourceAtView().then((src) => {
      if (!src || pane !== 'editor') return
      if (src.file === currentPath) collab?.goto(src.line, src.column)
      else if (files.some((f) => f.path === src.file && f.editable)) {
        pendingGoto = src
        currentPath = src.file
      }
    })
  }
  // ✓: volver a la lectura (se cierra el teclado) mostrando lo que se editaba.
  function doneEditing() {
    const v = collab?.view
    const line = v ? v.state.doc.lineAt(v.state.selection.main.head).number : 0
    v?.contentDOM.blur()
    pane = 'output'
    if (line) setTimeout(() => preview?.reveal(currentPath, line), 400)
  }

  let exporting = $state('') // '' | pdf | share | zip
  async function runExport(kind) {
    exporting = kind
    try {
      if (kind === 'pdf') await downloadPdf(project)
      else if (kind === 'share') await sharePdf(project)
      else {
        await downloadZip(project)
        toast('La descarga del proyecto comenzó')
      }
      menuOpen = false
    } catch (e) {
      toast(kind === 'zip' ? e.message : pdfError(e), 'error')
    } finally {
      exporting = ''
    }
  }
  // Abrir algo desde el menú: primero se cierra la hoja.
  function fromMenu(fn) {
    menuOpen = false
    fn()
  }
  const diagnostics = $derived(compileResult?.diagnostics ?? [])
  const errorCount = $derived(compileResult && !compileResult.ok ? Math.max(1, diagnostics.filter((x) => x.severity === 'error').length) : 0)

  // ---------- git ----------
  let gitBusy = $state('') // '' | pull | push
  let pushOpen = $state(false)
  let pushMessage = $state('')
  let gitStatus = $state(null)
  let pushError = $state('')
  let conflict = $state(null) // pull con conflictos pendiente de resolver (ConflictsDialog)

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
      if (e.status === 409 && e.data?.conflict) conflict = e.data.conflict
      else toast(e.message, 'error')
    } finally {
      gitBusy = ''
    }
  }

  // Resoluciones del diálogo de conflictos: el servidor rehace la fusión y hace el commit.
  async function resolveConflict(files) {
    try {
      const r = await api.gitResolve(id, conflict.theirs, files)
      conflict = null
      toast(r.message)
    } catch (e) {
      // Conflicto distinto (alguien editó mientras tanto): mostrar el nuevo.
      if (e.status === 409 && e.data?.conflict) {
        conflict = e.data.conflict
        toast('Los archivos cambiaron mientras resolvías: revisa los conflictos de nuevo', 'error')
        return
      }
      throw e
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
  <div
    class="flex flex-col {mobile ? 'fixed inset-x-0 z-10 bg-surface' : 'h-[calc(100dvh-3.5rem)]'}"
    style={mobile ? `top: ${vv.top}px; height: ${vv.height}px` : undefined}
  >
    {#if mobile}
      <!-- Barra superior del celular: lectura (← título ⋮) o edición (✓ archivo ↶ ↷ ⋮) -->
      <header class="flex h-14 shrink-0 items-center gap-0.5 border-b border-slate-200 bg-surface px-1" style="padding-top: env(safe-area-inset-top)">
        {#if pane === 'editor'}
          <button type="button" class="flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-full text-slate-600 active:bg-slate-100 disabled:opacity-35 !text-primary" onclick={doneEditing} aria-label="Listo: volver a la vista previa" title="Listo">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5" /></svg>
          </button>
          <button
            type="button"
            class="flex h-11 min-w-0 flex-1 cursor-pointer items-center gap-1 rounded-lg px-1.5 text-left active:bg-slate-100"
            onclick={() => (filesOpen = true)}
            aria-label="Cambiar de archivo (abierto: {currentPath || 'ninguno'})"
          >
            <span class="size-2 shrink-0 rounded-full {statusColor[status]}" title={statusLabel[status]}></span>
            <span class="truncate font-mono text-[13px] text-slate-800">{currentPath || 'Elegir archivo'}</span>
            <svg class="shrink-0 text-slate-500" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
          </button>
          <button type="button" class="flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-full text-slate-600 active:bg-slate-100 disabled:opacity-35" onclick={() => onTool('undo')} disabled={!fmt.canUndo} aria-label="Deshacer">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 14 4 9l5-5" /><path d="M4 9h11a5 5 0 0 1 0 10h-3" /></svg>
          </button>
          <button type="button" class="flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-full text-slate-600 active:bg-slate-100 disabled:opacity-35" onclick={() => onTool('redo')} disabled={!fmt.canRedo} aria-label="Rehacer">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 14 5-5-5-5" /><path d="M20 9H9a5 5 0 0 0 0 10h3" /></svg>
          </button>
        {:else}
          {#if !guest}
          <a href="#/" class="flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-full text-slate-600 active:bg-slate-100 disabled:opacity-35" aria-label="Volver a proyectos">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M19 12H5M12 19l-7-7 7-7" /></svg>
          </a>
          {:else}<span class="w-2 shrink-0"></span>{/if}
          <div class="flex min-w-0 flex-1 flex-col px-1">
            <h1 class="truncate text-base leading-tight font-semibold">{project.name}</h1>
            <span class="flex items-center gap-1.5 text-xs text-slate-500">
              <span class="size-1.5 rounded-full {statusColor[status]}"></span>{statusLabel[status]}
            </span>
          </div>
          {#if people.length}
            <div class="mr-1 flex -space-x-1.5" aria-label="Usuarios conectados">
              {#each people.slice(0, 2) as p (p.user_id)}
                <span class="flex size-7 items-center justify-center rounded-full text-[11px] font-semibold text-white ring-2 ring-surface" style="background-color: {p.color}" title={p.name}>{initials(p.name)}</span>
              {/each}
              {#if people.length > 2}<span class="flex size-7 items-center justify-center rounded-full bg-slate-200 text-[11px] font-semibold text-slate-700 ring-2 ring-surface">+{people.length - 2}</span>{/if}
            </div>
          {/if}
          {#if !guest}
          <button type="button" class="flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-full text-slate-600 active:bg-slate-100 disabled:opacity-35" onclick={() => (shareOpen = true)} aria-label="Compartir">
            <svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M19 8v6M22 11h-6" /></svg>
          </button>
          {/if}
        {/if}
        <button type="button" class="flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-full text-slate-600 active:bg-slate-100 disabled:opacity-35" onclick={() => (menuOpen = true)} aria-label="Más opciones: exportar, archivos, GitHub, ajustes">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="12" cy="5" r="1.8" /><circle cx="12" cy="12" r="1.8" /><circle cx="12" cy="19" r="1.8" /></svg>
        </button>
      </header>
    {:else}
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
        {#if !guest}
          <a href="#/" class="text-[13px] text-slate-500 no-underline hover:text-primary">Proyectos</a>
          <span class="text-slate-300">/</span>
        {/if}
        <h1 class="truncate text-[15px] font-semibold">{project.name}</h1>
        {#if readOnly}<span class="badge bg-amber-100 text-amber-800" title="Tu enlace de invitado no permite editar">Solo lectura</span>{/if}
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
        {#if !guest}
        <div class="flex items-center gap-1.5">
          <button type="button" class="btn-secondary btn-sm" onclick={() => (shareOpen = true)} title="Invitar usuarios, ver quién tiene acceso y exportar en PDF o .zip">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M19 8v6M22 11h-6" /></svg>
            Compartir{#if project.members > 0}<span class="badge !bg-primary-soft !px-1.5 !py-0 text-primary">{project.members}</span>{/if}
          </button>
          <button type="button" class="btn-secondary btn-sm" onclick={pull} disabled={!!gitBusy || !canGit} title={canGit ? 'Traer cambios del repositorio remoto' : gitDenied}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 4v12M6 10l6 6 6-6M5 20h14" /></svg>
            {gitBusy === 'pull' ? 'Pull…' : 'Pull'}
          </button>
          <button type="button" class="btn-primary btn-sm" onclick={openPush} disabled={!!gitBusy || !canGit} title={canGit ? 'Confirmar y enviar al repositorio remoto' : gitDenied}>
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
        {/if}
      </div>
    </header>
    {/if}

    <div class="flex min-h-0 flex-1" bind:this={split}>
      <!-- Archivos -->
      {#if showFiles && !mobile}
        <aside class="flex w-60 shrink-0 flex-col border-r border-slate-200 bg-slate-50">
          <div class="flex items-center justify-between px-3 pt-3 pb-2">
            <span class="text-xs font-semibold tracking-wide text-slate-500 uppercase">Archivos</span>
            <div class="flex gap-1 {readOnly ? 'hidden' : ''}">
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
            onremove={readOnly ? null : removeFile}
            onreplace={readOnly ? null : (f) => uploader?.openReplace(f.path)}
            ondropfiles={readOnly ? null : (list, folder) => uploader?.addFiles(list, { folder, autostart: true })}
          />
        </aside>
      {/if}

      <!-- Editor -->
      <section
        class="relative flex min-w-0 flex-1 flex-col bg-surface {pane === 'editor' ? '' : 'max-lg:hidden'}"
        ondragenter={(e) => !readOnly && dragHasFiles(e) && editorDrag++}
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
        {#if readOnly}
          <div class="flex h-10 shrink-0 items-center gap-2 border-b border-slate-200 bg-surface px-3 max-lg:hidden">
            <span class="min-w-0 flex-1 truncate font-mono text-xs text-slate-600" title={currentPath}>{currentPath}</span>
            <span class="text-xs text-slate-500">Solo lectura</span>
          </div>
        {:else if isBib}
          <div class="flex h-10 shrink-0 items-center gap-2 border-b border-slate-200 bg-surface pr-2 pl-3">
            <span class="min-w-0 flex-1 truncate font-mono text-xs text-slate-600" title={currentPath}>{currentPath}</span>
            <div class="flex rounded-lg bg-slate-100 p-0.5" role="tablist" aria-label="Modo de edición de la bibliografía">
              {#each [['visual', 'Visual', 'Gestionar las referencias con formularios'], ['code', 'Código', 'Editar el BibTeX como texto']] as [k, label, title]}
                <button
                  type="button"
                  role="tab"
                  aria-selected={bibMode === k}
                  {title}
                  class="h-7 cursor-pointer rounded-md px-3 text-[13px] font-medium {bibMode === k ? 'bg-surface text-primary shadow-sm' : 'text-slate-500 hover:text-slate-900'}"
                  onclick={() => setBibMode(k)}>{label}</button
                >
              {/each}
            </div>
          </div>
        {:else if !mobile}
          <EditorToolbar
            file={current && !current.editable ? `${currentPath} (no editable)` : currentPath}
            active={fmt}
            margin={marginCol}
            spell={spellable(currentPath) ? spellOn : null}
            disabled={!current?.editable || status !== 'online'}
            labels={() => cmd.labelsIn(collab?.text ?? '')}
            onaction={onTool}
          />
        {/if}
        <div class="relative min-h-0 flex-1">
          <div bind:this={host} class="absolute inset-0 {bibVisual ? 'invisible' : ''}"></div>
          {#if bibVisual && live}
            <div class="absolute inset-0 z-10">
              {#key live}
                <BibEditor
                  bind:this={bibEditor}
                  projectId={id}
                  path={currentPath}
                  session={live}
                  version={liveVersion}
                  focusKey={bibFocus}
                  onshowcode={showBibCode}
                  ontoast={toast}
                />
              {/key}
            </div>
          {/if}
          {#if !currentPath}
            <p class="absolute inset-0 flex items-center justify-center text-sm text-slate-500">Selecciona un archivo para editarlo.</p>
          {/if}
        </div>
        {#if mobile && !isBib && current?.editable && !readOnly}
          <!-- Barra de formato del celular: abajo, justo sobre el teclado. -->
          <EditorToolbar compact active={fmt} disabled={status !== 'online'} labels={() => cmd.labelsIn(collab?.text ?? '')} onaction={onTool} />
        {/if}
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
        class="relative flex min-w-0 flex-col max-lg:flex-1 {pane === 'output' ? '' : 'max-lg:hidden'}"
        style="--w: {rightWidth}%"
      >
        <div class="flex h-10 shrink-0 items-end gap-1 border-b border-slate-200 bg-slate-50 px-2 max-lg:hidden" role="tablist">
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
          <div class="h-full {tab === 'preview' || mobile ? '' : 'hidden'}">
            <Preview
              bind:this={preview}
              projectId={id}
              {refreshKey}
              {remoteKey}
              livePath={currentPath}
              {liveVersion}
              liveText={() => (collab?.loaded ? collab.text : null)}
              browser={browserCompile}
              compact={mobile}
              active={outputVisible} onresult={(r) => (compileResult = r)} onjump={onDiagnostic} />
          </div>
          {#if !mobile}
            <div class="h-full {tab === 'pdf' ? '' : 'hidden'}">
              <PdfView projectId={id} {refreshKey} active={tab === 'pdf' && outputVisible} filename={pdfName} />
            </div>
          {/if}
        </div>
        {#if mobile}
          <!-- Lectura en el celular: aviso de errores y lápiz para editar. -->
          <div class="pointer-events-none absolute inset-x-0 bottom-0 flex items-end justify-between gap-3 px-4" style="padding-bottom: calc(1.25rem + env(safe-area-inset-bottom))">
            {#if errorCount || diagnostics.length}
              <button
                type="button"
                class="pointer-events-auto flex h-10 cursor-pointer items-center gap-1.5 rounded-full border px-4 text-[13px] font-semibold shadow-md {errorCount
                  ? 'border-red-200 bg-red-50 text-red-800'
                  : 'border-amber-200 bg-amber-50 text-amber-800'}"
                onclick={() => (problemsOpen = true)}
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 8v5M12 16h.01" /></svg>
                {#if errorCount}{errorCount} {errorCount === 1 ? 'error' : 'errores'}{:else}{diagnostics.length} {diagnostics.length === 1 ? 'aviso' : 'avisos'}{/if}
              </button>
            {:else}
              <span></span>
            {/if}
            <button
              type="button"
              class="pointer-events-auto flex size-15 cursor-pointer items-center justify-center rounded-2xl bg-primary text-white shadow-lg shadow-primary/30 active:bg-primary-hover disabled:opacity-50"
              onclick={startEditing}
              disabled={!current?.editable}
              aria-label="Editar"
              title="Editar"
            >
              <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" /></svg>
            </button>
          </div>
        {:else}
          <ProblemsPanel result={compileResult} ondiagnostic={onDiagnostic} />
        {/if}
      </section>
    </div>
  </div>

  <!-- Avisos -->
  <div class="pointer-events-none fixed right-4 bottom-4 z-40 flex max-lg:top-16 max-lg:bottom-auto w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" aria-live="polite">
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
  <ImageLightbox
    bind:this={lightbox}
    projectId={id}
    images={imageFiles}
    oninsert={collab?.path?.endsWith('.typ') ? (path) => insertMedia({ items: [{ path }] }) : null}
  />
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
  {#if conflict}
    <ConflictsDialog {conflict} onresolve={resolveConflict} onclose={() => (conflict = null)} />
  {/if}
  <TableModal bind:this={tableDialog} onapply={applyTable} onclose={releaseTable} />
  <CiteModal
    bind:this={citeDialog}
    projectId={id}
    {files}
    live={() => collab}
    oninsert={insertCite}
    onmanage={manageBib}
    onaddlink={currentPath === project.main_file ? addBibliography : null}
    onclose={releaseCite}
  />
  <OptimizeModal
    projectId={id}
    {files}
    open={optimizeOpen}
    onclose={() => (optimizeOpen = false)}
    ondone={() => loadFiles().catch(() => {})}
  />
  <ShareModal open={shareOpen} {project} {canManage} refreshKey={membersKey} onclose={() => (shareOpen = false)} />

  {#if mobile}
    <!-- Celular: menú ⋮ (exportar y el resto de acciones de la cabecera de escritorio) -->
    <Sheet open={menuOpen} onclose={() => !exporting && (menuOpen = false)}>
      {#snippet header()}
        <div class="border-b border-slate-200 px-5 pt-1 pb-3">
          <h2 class="truncate text-base font-semibold">{project.name}</h2>
          <p class="truncate text-xs text-slate-500">
            {[
              project.main_file,
              compileResult?.pages?.length && `${compileResult.pages.length} ${compileResult.pages.length === 1 ? 'página' : 'páginas'}`,
              project.members > 0 && `${project.members + 1} personas`,
            ]
              .filter(Boolean)
              .join(' · ')}
          </p>
        </div>
      {/snippet}
      <p class="px-5 pt-3 pb-1 text-xs font-semibold tracking-wide text-slate-500 uppercase">Exportar</p>
      <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => runExport('pdf')} disabled={!!exporting}>
        <svg class="shrink-0 text-red-600" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" /><path d="M14 3v5h5" /><path d="M12 12v6m-3-3 3 3 3-3" /></svg>
        <span class="min-w-0"><span class="block text-[15px] font-medium text-slate-900">{exporting === 'pdf' ? 'Compilando PDF…' : 'Descargar PDF'}</span><span class="block text-xs text-slate-500">Con las imágenes originales</span></span>
      </button>
      {#if canSharePdf}
        <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => runExport('share')} disabled={!!exporting}>
          <svg class="shrink-0 text-primary" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 12v7a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-7" /><path d="m16 6-4-4-4 4" /><path d="M12 2v13" /></svg>
          <span class="min-w-0"><span class="block text-[15px] font-medium text-slate-900">{exporting === 'share' ? 'Compilando PDF…' : 'Enviar PDF…'}</span><span class="block text-xs text-slate-500">WhatsApp, correo, Drive… (menú del sistema)</span></span>
        </button>
      {/if}
      {#if !guest}
      <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => runExport('zip')} disabled={!!exporting}>
        <svg class="shrink-0 text-amber-600" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 8v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8" /><path d="M1 3h22v5H1z" /><path d="M10 12h4" /></svg>
        <span class="min-w-0"><span class="block text-[15px] font-medium text-slate-900">{exporting === 'zip' ? 'Preparando…' : 'Descargar proyecto (.zip)'}</span><span class="block text-xs text-slate-500">Fuentes e imágenes, sin el historial de git</span></span>
      </button>
      <div class="my-1.5 h-px bg-slate-200"></div>
      <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => fromMenu(() => (shareOpen = true))}>
        <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M19 8v6M22 11h-6" /></svg>
        <span class="text-[15px] text-slate-900">Compartir y enlace de revisión</span>
      </button>
      {:else}
      <div class="my-1.5 h-px bg-slate-200"></div>
      {/if}
      <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => fromMenu(() => (filesOpen = true))}>
        <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /></svg>
        <span class="text-[15px] text-slate-900">Archivos del proyecto</span>
      </button>
      {#if !readOnly && spellable(currentPath)}
        <button type="button" role="menuitemcheckbox" aria-checked={spellOn} class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100" onclick={() => setSpell(!spellOn)}>
          <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m4 14 4-10 4 10M5.5 10.5h5" /><path d="M3 19c1.5-1.3 3-1.3 4.5 0s3 1.3 4.5 0 3-1.3 4.5 0 3 1.3 4.5 0" /></svg>
          <span class="min-w-0 flex-1"><span class="block text-[15px] text-slate-900">Revisión ortográfica</span><span class="block text-xs text-slate-500">Toca una palabra subrayada para ver sugerencias</span></span>
          <span class="h-6 w-10 shrink-0 rounded-full p-0.5 transition-colors {spellOn ? 'bg-primary' : 'bg-slate-300'}" aria-hidden="true"><span class="block size-5 rounded-full bg-white shadow transition-transform {spellOn ? 'translate-x-4' : ''}"></span></span>
        </button>
      {/if}
      {#if pane === 'editor' && !isBib && current?.editable}
        <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => fromMenu(() => onTool('search'))}>
          <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
          <span class="text-[15px] text-slate-900">Buscar y reemplazar</span>
        </button>
      {/if}
      {#if canGit}
        <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => fromMenu(pull)} disabled={!!gitBusy}>
          <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 4v12M6 10l6 6 6-6M5 20h14" /></svg>
          <span class="text-[15px] text-slate-900">{gitBusy === 'pull' ? 'Trayendo cambios…' : 'Pull: traer cambios de GitHub'}</span>
        </button>
        <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => fromMenu(openPush)} disabled={!!gitBusy}>
          <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20V8M6 14l6-6 6 6M5 4h14" /></svg>
          <span class="text-[15px] text-slate-900">Push: enviar a GitHub</span>
        </button>
      {/if}
      {#if !guest}
      <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45" onclick={() => fromMenu(openSettings)}>
        <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3" /><path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1" /></svg>
        <span class="text-[15px] text-slate-900">Ajustes del proyecto</span>
      </button>
      <a href="#/" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100 disabled:cursor-default disabled:opacity-45 no-underline">
        <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></svg>
        <span class="text-[15px] text-slate-900">Todos los proyectos</span>
      </a>
      {:else}
      <button type="button" class="flex min-h-13 w-full cursor-pointer items-center gap-4 px-5 py-2 text-left active:bg-slate-100" onclick={() => ((menuOpen = false), window.dispatchEvent(new Event('typsteditor:guest-exit')))}>
        <svg class="shrink-0 text-slate-500" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" /><circle cx="12" cy="7" r="4" /></svg>
        <span class="text-[15px] text-slate-900">Salir · cambiar mi nombre de invitado ({session.user.name})</span>
      </button>
      {/if}
      <div class="h-3"></div>
    </Sheet>

    <!-- Celular: archivos -->
    <Sheet open={filesOpen} tall onclose={() => (filesOpen = false)}>
      {#snippet header()}
        <div class="flex h-12 items-center pr-2 pl-5">
          <h2 class="flex-1 text-base font-semibold">Archivos</h2>
          {#if !readOnly}
          <button
            type="button"
            class="flex size-11 cursor-pointer items-center justify-center rounded-full text-slate-600 active:bg-slate-100"
            onclick={() => {
              filesOpen = false
              newFilePath = ''
              fileError = ''
              newFileOpen = true
            }}
            aria-label="Nuevo archivo"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
          </button>
          <button type="button" class="flex size-11 cursor-pointer items-center justify-center rounded-full text-slate-600 active:bg-slate-100" onclick={() => {
              filesOpen = false
              uploader?.openUpload()
            }} aria-label="Subir archivos o fotos">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12" /></svg>
          </button>
          {/if}
        </div>
      {/snippet}
      <div class="flex h-full flex-col px-2">
        <FileTree
          projectId={id}
          {files}
          {currentPath}
          mainFile={project.main_file}
          peopleIn={(path) => people.filter((p) => p.paths.includes(path) && p.user_id !== session.user.id)}
          onopen={openFile}
          onremove={readOnly ? null : removeFile}
          onreplace={readOnly ? null : (f) => ((filesOpen = false), uploader?.openReplace(f.path))}
          ondropfiles={readOnly ? null : (list, folder) => uploader?.addFiles(list, { folder, autostart: true })}
        />
      </div>
    </Sheet>

    <!-- Celular: problemas de la última compilación -->
    <Sheet open={problemsOpen} title="Problemas" onclose={() => (problemsOpen = false)}>
      <ul class="pb-3">
        {#each diagnostics as dg}
          <li>
            <button type="button" class="flex w-full cursor-pointer items-start gap-3 px-5 py-2.5 text-left active:bg-slate-100" onclick={() => onDiagnostic(dg)}>
              <span class="mt-1.5 size-2 shrink-0 rounded-full {dg.severity === 'error' ? 'bg-red-600' : 'bg-amber-500'}"></span>
              <span class="min-w-0">
                <span class="block font-mono text-xs text-slate-500">{dg.file}:{dg.line}</span>
                <span class="block text-sm break-words text-slate-800">{dg.message}</span>
              </span>
            </button>
          </li>
        {:else}
          <li class="px-5 py-2 font-mono text-xs break-words whitespace-pre-wrap text-slate-700">{compileResult?.output || 'Error de compilación'}</li>
        {/each}
      </ul>
    </Sheet>
  {/if}

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
        <textarea class="input !h-20 py-2" bind:value={pushMessage} maxlength="2000" placeholder="Actualización desde Typst Extend"></textarea>
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

  <Drawer open={settingsOpen} title="Ajustes del proyecto" onclose={() => !savingSettings && (settingsOpen = false)}>
    <form id="project-settings" class="flex flex-col gap-4" onsubmit={saveSettings}>
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
      {#if canManage}
        <div class="mt-2 rounded-lg border border-red-200 p-4">
          <p class="text-[13px] font-semibold text-red-700">Zona de peligro</p>
          <p class="mt-1 mb-3 text-sm text-slate-600">Eliminar el proyecto borra sus archivos para todos los miembros. No se puede deshacer.</p>
          <button type="button" class="btn-secondary btn-sm !border-red-200 !text-red-700 hover:!bg-red-50" onclick={deleteProject}>Eliminar proyecto</button>
        </div>
      {/if}
    </form>
    {#snippet footer()}
      <div class="flex justify-end gap-2">
        <button type="button" class="btn-secondary" onclick={() => (settingsOpen = false)}>Cerrar</button>
        {#if canManage}
          <button form="project-settings" class="btn-primary" disabled={savingSettings}>{savingSettings ? 'Guardando…' : 'Guardar'}</button>
        {/if}
      </div>
    {/snippet}
  </Drawer>
{/if}

<style>
  @media (min-width: 64rem) {
    section[style*='--w'] {
      width: var(--w);
      flex: none;
    }
  }
</style>
