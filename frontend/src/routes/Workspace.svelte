<script>
  import { onMount, onDestroy } from 'svelte'
  import { fly } from 'svelte/transition'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import { d } from '../lib/motion.js'
  import Modal from '../lib/Modal.svelte'
  import { CollabSession } from '../lib/editor/collab-client.js'
  import Preview from '../lib/workspace/Preview.svelte'
  import PdfView from '../lib/workspace/PdfView.svelte'
  import ShareModal from '../lib/workspace/ShareModal.svelte'
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
  let rightWidth = $state(45) // % del ancho para la vista previa
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
  let uploadInput = $state()

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

  async function upload(e) {
    const list = [...e.target.files]
    e.target.value = ''
    for (const file of list) {
      try {
        await api.uploadFile(id, file)
        toast(`«${file.name}» subido`)
      } catch (err) {
        toast(`${file.name}: ${err.message}`, 'error')
      }
    }
    loadFiles().catch(() => {})
  }

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

  const fmtSize = (n) => (n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(0)} KB` : `${(n / 1048576).toFixed(1)} MB`)

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

  onDestroy(() => collab?.destroy())

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
    <header class="flex min-h-12 shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b border-slate-200 bg-white px-3 py-1.5">
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
              class="flex size-7 items-center justify-center rounded-full text-[11px] font-semibold text-white ring-2 ring-white"
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
    <div class="flex shrink-0 border-b border-slate-200 bg-white lg:hidden" role="tablist">
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
        <aside class="flex w-56 shrink-0 flex-col border-r border-slate-200 bg-slate-50 {pane === 'editor' ? '' : 'max-lg:hidden'}">
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
                onclick={() => uploadInput.click()}
                title="Subir archivos (imágenes, fuentes, .bib…)"
                aria-label="Subir archivos"
              >
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12" /></svg>
              </button>
              <input type="file" multiple class="hidden" bind:this={uploadInput} onchange={upload} />
            </div>
          </div>
          <ul class="min-h-0 flex-1 overflow-y-auto px-1.5 pb-3">
            {#each files as f (f.path)}
              <li class="group relative">
                <button
                  type="button"
                  class="flex w-full cursor-pointer items-center gap-2 rounded-md py-1.5 pr-8 pl-2 text-left text-[13px] {f.path === currentPath
                    ? 'bg-primary-soft font-semibold text-primary'
                    : 'text-slate-700 hover:bg-slate-200/70'}"
                  onclick={() => openFile(f)}
                  title={f.editable ? f.path : `${f.path} (${fmtSize(f.size)}) — abrir`}
                >
                  <span class="shrink-0 text-slate-400" aria-hidden="true">{f.editable ? '¶' : '◫'}</span>
                  <span class="truncate">{f.path}</span>
                  {#if f.path === project.main_file}
                    <span class="ml-auto shrink-0 rounded bg-primary px-1 text-[10px] font-semibold text-white">main</span>
                  {/if}
                  {#each people.filter((p) => p.paths.includes(f.path) && p.user_id !== session.user.id) as p (p.user_id)}
                    <span class="size-2 shrink-0 rounded-full" style="background-color: {p.color}" title={p.name}></span>
                  {/each}
                </button>
                {#if f.path !== project.main_file}
                  <button
                    type="button"
                    class="absolute top-1/2 right-1 hidden size-6 -translate-y-1/2 cursor-pointer items-center justify-center rounded text-slate-400 group-hover:flex hover:bg-red-100 hover:text-red-700"
                    onclick={() => removeFile(f)}
                    aria-label="Eliminar {f.path}"
                    title="Eliminar"
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" /></svg>
                  </button>
                {/if}
              </li>
            {/each}
          </ul>
        </aside>
      {/if}

      <!-- Editor -->
      <section class="flex min-w-0 flex-1 flex-col bg-white {pane === 'editor' ? '' : 'max-lg:hidden'}">
        <div class="flex h-10 shrink-0 items-center gap-2 border-b border-slate-200 px-3 text-[13px] text-slate-600">
          <span class="truncate font-mono">{currentPath || '—'}</span>
          {#if current && !current.editable}<span class="text-slate-400">(no editable)</span>{/if}
        </div>
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
                ? 'border-slate-200 bg-white text-primary'
                : 'border-transparent text-slate-500 hover:text-slate-900'}"
              onclick={() => (tab = k)}>{label}</button
            >
          {/each}
        </div>
        <div class="min-h-0 flex-1">
          <div class="h-full {tab === 'preview' ? '' : 'hidden'}">
            <Preview projectId={id} {refreshKey} ondiagnostic={onDiagnostic} />
          </div>
          <div class="h-full {tab === 'pdf' ? '' : 'hidden'}">
            <PdfView projectId={id} {refreshKey} active={tab === 'pdf'} filename={pdfName} />
          </div>
        </div>
      </section>
    </div>
  </div>

  <!-- Avisos -->
  <div class="pointer-events-none fixed right-4 bottom-4 z-40 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" aria-live="polite">
    {#each toasts as t (t.id)}
      <div
        class="pointer-events-auto rounded-lg border px-4 py-3 text-sm shadow-lg {t.kind === 'error'
          ? 'border-red-200 bg-red-50 text-red-800'
          : 'border-slate-200 bg-white text-slate-800'}"
        transition:fly={{ y: 12, duration: d(180) }}
      >
        {t.msg}
      </div>
    {/each}
  </div>

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
