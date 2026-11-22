<script>
  // Miembros del proyecto: el propietario (o un admin) invita a usuarios
  // registrados y puede quitarlos; un miembro puede abandonar el proyecto.
  import { api } from '../api.js'
  import { session } from '../session.svelte.js'
  import Modal from '../Modal.svelte'

  let { open, project, canManage, onclose, refreshKey = 0 } = $props()

  let members = $state([])
  let loading = $state(false)
  let error = $state('')
  let notice = $state('')
  let query = $state('')
  let results = $state([])
  let highlighted = $state(0)
  let busy = $state(false)
  let searchTimer
  let searchSeq = 0

  async function load() {
    loading = true
    try {
      members = await api.listMembers(project.id)
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  // Recargar al abrir y cuando otro usuario cambie la lista.
  $effect(() => {
    refreshKey
    if (open) load()
  })
  $effect(() => {
    if (!open) {
      query = ''
      results = []
      error = ''
      notice = ''
    }
  })

  const memberIds = $derived(new Set(members.map((m) => m.id)))

  function onInput() {
    clearTimeout(searchTimer)
    const q = query.trim()
    if (q.length < 2) {
      results = []
      return
    }
    const seq = ++searchSeq
    searchTimer = setTimeout(async () => {
      try {
        const r = await api.searchUsers(q)
        if (seq === searchSeq) {
          results = r
          highlighted = 0
        }
      } catch {
        /* la búsqueda es solo una ayuda */
      }
    }, 200)
  }

  async function invite(username) {
    error = ''
    notice = ''
    busy = true
    try {
      await api.addMember(project.id, username)
      notice = `«${username}» ahora puede editar este proyecto.`
      query = ''
      results = []
      await load()
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  function submit(e) {
    e.preventDefault()
    const pick = results.filter((u) => !memberIds.has(u.id))[highlighted]
    const username = pick?.username ?? query.trim()
    if (username) invite(username)
  }

  function onKeydown(e) {
    const list = results.filter((u) => !memberIds.has(u.id))
    if (!list.length) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      highlighted = (highlighted + 1) % list.length
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      highlighted = (highlighted - 1 + list.length) % list.length
    }
  }

  async function remove(m) {
    const self = m.id === session.user.id
    const msg = self
      ? `¿Abandonar «${project.name}»? Dejarás de tener acceso hasta que te vuelvan a invitar.`
      : `¿Quitar a ${m.name} (${m.username}) del proyecto?`
    if (!confirm(msg)) return
    error = ''
    try {
      await api.removeMember(project.id, m.id)
      if (self && session.user.role !== 'admin') {
        location.hash = '#/'
        return
      }
      await load()
    } catch (e) {
      error = e.message
    }
  }

  // ---------- exportar ----------
  let exporting = $state('') // '' | 'pdf' | 'zip'

  // Descarga un archivo con el gestor de descargas del navegador.
  function download(href, filename) {
    const a = document.createElement('a')
    a.href = href
    if (filename) a.download = filename
    document.body.append(a)
    a.click()
    a.remove()
  }

  async function exportPdf() {
    error = ''
    notice = ''
    exporting = 'pdf'
    try {
      const { blob } = await api.pdf(project.id)
      const href = URL.createObjectURL(blob)
      download(href, `${project.name.replace(/[\\/:*?"<>|]+/g, '-').trim() || 'documento'}.pdf`)
      setTimeout(() => URL.revokeObjectURL(href), 60000)
    } catch (e) {
      error = e.data?.diagnostics
        ? 'El documento tiene errores y no se pudo generar el PDF. Revísalos en el panel «Problemas».'
        : e.message
    } finally {
      exporting = ''
    }
  }

  async function exportZip() {
    error = ''
    notice = ''
    exporting = 'zip'
    try {
      // El servidor arma el .zip mientras lo envía: la descarga sigue en el navegador.
      download(await api.exportZipURL(project.id))
      notice = 'La descarga del proyecto comenzó en el navegador.'
    } catch (e) {
      error = e.message
    } finally {
      exporting = ''
    }
  }

  const initials = (n) => n.split(/\s+/).map((w) => w[0]).slice(0, 2).join('').toUpperCase()
  const suggestions = $derived(results.filter((u) => !memberIds.has(u.id)))
</script>

<Modal {open} title="Compartir proyecto" {onclose}>
  <div class="flex flex-col gap-4">
    {#if error}<div class="alert-error">{error}</div>{/if}
    {#if notice}<div class="alert-info">{notice}</div>{/if}

    {#if canManage}
      <form class="relative flex gap-2" onsubmit={submit} autocomplete="off">
        <label class="sr-only" for="invite-user">Usuario a invitar</label>
        <input
          id="invite-user"
          class="input"
          bind:value={query}
          oninput={onInput}
          onkeydown={onKeydown}
          placeholder="Buscar por usuario o nombre…"
          role="combobox"
          aria-expanded={suggestions.length > 0}
          aria-controls="invite-suggestions"
        />
        <button class="btn-primary shrink-0" disabled={busy || !query.trim()}>{busy ? 'Invitando…' : 'Invitar'}</button>
        {#if suggestions.length}
          <ul
            id="invite-suggestions"
            role="listbox"
            class="absolute top-12 right-0 left-0 z-10 overflow-hidden rounded-lg border border-slate-200 bg-surface py-1 shadow-lg"
          >
            {#each suggestions as u, i (u.id)}
              <li role="option" aria-selected={i === highlighted}>
                <button
                  type="button"
                  class="flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-left text-sm {i === highlighted ? 'bg-primary-tint' : 'hover:bg-slate-50'}"
                  onmouseenter={() => (highlighted = i)}
                  onclick={() => invite(u.username)}
                >
                  <span class="font-medium">{u.name}</span>
                  <span class="text-slate-500">@{u.username}</span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </form>
      <p class="-mt-2 text-xs text-slate-500">
        Los invitados pueden editar, compilar y hacer pull/push con la configuración de GitHub del proyecto. Solo tú
        (o un administrador) puede cambiar los ajustes e invitar.
      </p>
    {/if}

    <div>
      <p class="mb-2 text-[13px] font-semibold text-slate-800">Personas con acceso</p>
      {#if loading && !members.length}
        <p class="text-sm text-slate-500">Cargando…</p>
      {:else}
        <ul class="flex max-h-72 flex-col divide-y divide-slate-100 overflow-y-auto">
          {#each members as m (m.id)}
            <li class="flex items-center gap-3 py-2.5">
              <span
                class="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary-soft text-xs font-semibold text-primary"
                >{initials(m.name)}</span
              >
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium">
                  {m.name}
                  {#if m.id === session.user.id}<span class="font-normal text-slate-500">(tú)</span>{/if}
                </p>
                <p class="truncate text-xs text-slate-500">
                  @{m.username}{#if m.invited_by}{' · '}invitado por {m.invited_by}{/if}{#if !m.active}{' · '}cuenta desactivada{/if}
                </p>
              </div>
              {#if m.role === 'owner'}
                <span class="badge bg-slate-100 text-slate-600">Propietario</span>
              {:else if canManage}
                <button type="button" class="btn-secondary btn-sm !h-8" onclick={() => remove(m)}>Quitar</button>
              {:else if m.id === session.user.id}
                <button type="button" class="btn-secondary btn-sm !h-8 !text-red-700" onclick={() => remove(m)}>Abandonar</button>
              {:else}
                <span class="badge bg-primary-tint text-primary">Editor</span>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    <div class="border-t border-slate-200 pt-4">
      <p class="mb-2 text-[13px] font-semibold text-slate-800">Exportar</p>
      <div class="grid gap-2 sm:grid-cols-2">
        <button
          type="button"
          class="flex cursor-pointer items-start gap-3 rounded-lg border border-slate-200 p-3 text-left hover:border-primary hover:bg-primary-tint disabled:cursor-wait disabled:opacity-60"
          onclick={exportPdf}
          disabled={!!exporting}
        >
          <svg class="mt-0.5 shrink-0 text-red-600" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
            stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" /><path d="M14 3v5h5" /><path d="M12 12v6m-3-3 3 3 3-3" />
          </svg>
          <span class="text-sm">
            <span class="block font-medium text-slate-900">{exporting === 'pdf' ? 'Compilando PDF…' : 'Descargar PDF'}</span>
            <span class="block text-xs text-slate-500">El documento compilado, con las imágenes originales.</span>
          </span>
        </button>
        <button
          type="button"
          class="flex cursor-pointer items-start gap-3 rounded-lg border border-slate-200 p-3 text-left hover:border-primary hover:bg-primary-tint disabled:cursor-wait disabled:opacity-60"
          onclick={exportZip}
          disabled={!!exporting}
        >
          <svg class="mt-0.5 shrink-0 text-amber-600" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
            stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 8v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8" /><path d="M1 3h22v5H1z" /><path d="M10 12h4" />
          </svg>
          <span class="text-sm">
            <span class="block font-medium text-slate-900">{exporting === 'zip' ? 'Preparando…' : 'Descargar proyecto (.zip)'}</span>
            <span class="block text-xs text-slate-500">Todos los archivos fuente e imágenes, sin el historial de git.</span>
          </span>
        </button>
      </div>
    </div>
  </div>
</Modal>
