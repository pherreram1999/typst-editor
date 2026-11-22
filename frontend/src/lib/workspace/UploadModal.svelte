<script>
  // Subida de archivos con Uppy (panel Dashboard dentro de nuestro Modal).
  // Modos: «upload» (varios archivos a una carpeta) y «replace» (un archivo
  // sustituye a otro existente, aunque se llame distinto). Las imágenes PNG y
  // JPEG se pueden optimizar sin pérdida en el servidor (imgopt). addFiles
  // recibe archivos soltados en el explorador (se suben directamente a la
  // carpeta resaltada) o en el editor (se pregunta la carpeta y, al terminar,
  // oninsert recibe las rutas de los medios para insertarlos como #figure o
  // #image donde se soltaron).
  import Uppy from '@uppy/core'
  import Dashboard from '@uppy/dashboard'
  import XHRUpload from '@uppy/xhr-upload'
  import es_MX from '@uppy/locales/es_MX'
  import '@uppy/core/css/style.min.css'
  import '@uppy/dashboard/css/style.min.css'
  import { onDestroy } from 'svelte'
  import Modal from '../Modal.svelte'
  import FolderPicker from '../FolderPicker.svelte'
  import { session } from '../session.svelte.js'
  import { theme } from '../theme.svelte.js'

  let { projectId, files = [], editingFile = null, onuploaded, ontoast, oninsert, onrelease } = $props()

  const MAX = 20 * 1024 * 1024
  const IMAGE = /\.(png|jpe?g)$/i

  let open = $state(false)
  let mode = $state({ kind: 'upload' }) // | { kind: 'replace', path }
  let dir = $state('')
  let optimize = $state(true)
  let uploading = $state(false)
  let asking = $state(false) // se soltaron archivos en el editor: elegir carpeta
  // Inserción en el documento de las imágenes subidas: siempre #image; si se
  // escribe un pie, dentro de #figure. insertCtx es la posición donde se
  // soltaron en el editor; sin ella, se inserta en el cursor del archivo abierto.
  // { path, at } o, para reemplazar la ruta de un image("…"), { path, replace: { from, to } }.
  // $state.raw: los objetos de posición se comparan por identidad en el editor.
  let insertCtx = $state.raw(null)
  let insertOn = $state(false)
  let mediaFiles = $state([]) // [{ id, name }] imágenes en la cola de Uppy
  let captions = $state({}) // id -> pie de figura
  const hasMedia = $derived(mode.kind === 'upload' && mediaFiles.length > 0 && (!!insertCtx || !!editingFile))
  const MEDIA = /\.(png|jpe?g|gif|svg|webp)$/i
  function refreshMedia() {
    mediaFiles = uppy.getFiles().filter((f) => MEDIA.test(f.name)).map((f) => ({ id: f.id, name: f.name }))
  }
  function releaseInsert() {
    if (insertCtx) onrelease?.(insertCtx)
    insertCtx = null
    insertOn = false
    captions = {}
  }

  // Carpetas existentes (las vacías no existen: solo hay archivos).
  const folders = $derived(
    [...new Set(files.flatMap((f) => f.path.split('/').slice(0, -1).map((_, i, a) => a.slice(0, i + 1).join('/'))))].sort(),
  )
  const replacingImage = $derived(mode.kind === 'replace' && IMAGE.test(mode.path))

  const uppy = new Uppy({ locale: es_MX, restrictions: { maxFileSize: MAX } })
    .use(XHRUpload, {
      endpoint: '', // se fija abajo, reactivo a projectId
      fieldName: 'file',
      formData: true,
      limit: 2,
      timeout: 180_000, // la optimización de una imagen grande tarda unos segundos sin progreso
      headers: () => ({ Authorization: `Bearer ${session.token}` }),
      allowedMetaFields: ['dir', 'optimize', 'path'],
      onAfterResponse: (xhr) => {
        if (xhr.status < 400) return
        let msg = ''
        try {
          msg = JSON.parse(xhr.responseText).error
        } catch {}
        throw new Error(msg || `Error ${xhr.status}`)
      },
    })

  // Los metadatos se envían con cada archivo.
  function syncMeta() {
    uppy.setMeta({
      dir: mode.kind === 'upload' ? dir.trim().replace(/^\/+|\/+$/g, '') : '',
      optimize: optimize ? '1' : '0',
      path: mode.kind === 'replace' ? mode.path : '',
    })
  }
  $effect(syncMeta)
  $effect(() => uppy.getPlugin('XHRUpload').setOptions({ endpoint: `/api/projects/${projectId}/upload` }))

  uppy.on('upload', () => (uploading = true))
  for (const ev of ['file-added', 'file-removed', 'cancel-all']) uppy.on(ev, refreshMedia)
  uppy.on('complete', (result) => {
    uploading = false
    const ok = result.successful ?? []
    if (!ok.length) return
    let before = 0
    let after = 0
    for (const f of ok) {
      const o = f.response?.body?.optimized
      if (o) {
        before += o.before
        after += o.after
      }
    }
    const saved = before - after
    const what = mode.kind === 'replace' ? `«${mode.path}» reemplazado` : `${ok.length} ${ok.length === 1 ? 'archivo subido' : 'archivos subidos'}`
    ontoast?.(saved > 0 ? `${what}; imágenes optimizadas: ${fmt(before)} → ${fmt(after)}` : what)
    onuploaded?.()
    if (insertOn && mode.kind === 'upload') {
      const items = ok
        .map((f) => ({ path: f.response?.body?.path, caption: (captions[f.id] ?? '').trim() }))
        .filter((it) => it.path && MEDIA.test(it.path))
      if (items.length) oninsert?.({ ctx: insertCtx, items })
    }
    if (!result.failed?.length) close()
  })

  export function openUpload(folder = '') {
    if (mode.kind !== 'upload') uppy.cancelAll()
    mode = { kind: 'upload' }
    dir = folder
    asking = false
    releaseInsert()
    uppy.setOptions({ restrictions: { maxFileSize: MAX, maxNumberOfFiles: null, allowedFileTypes: null } })
    open = true
  }

  export function openReplace(path) {
    uppy.cancelAll()
    releaseInsert()
    mode = { kind: 'replace', path }
    const ext = path.includes('.') ? path.split('.').pop().toLowerCase() : ''
    const types = IMAGE.test(path) ? ['.png', '.jpg', '.jpeg', '.PNG', '.JPG', '.JPEG'] : ext ? [`.${ext}`] : null
    uppy.setOptions({ restrictions: { maxFileSize: MAX, maxNumberOfFiles: 1, allowedFileTypes: types } })
    open = true
  }

  // Botón «Insertar imagen» del editor: solo imágenes, insertándolas en `insert`.
  export function openInsertImage({ folder = '', insert }) {
    if (uploading) {
      ontoast?.('Espera a que termine la subida en curso', 'error')
      onrelease?.(insert)
      return
    }
    openUpload(folder)
    asking = true
    insertCtx = insert
    insertOn = true
    uppy.setOptions({
      restrictions: { maxFileSize: MAX, maxNumberOfFiles: insert.replace ? 1 : null, allowedFileTypes: ['image/*', '.svg'] },
    })
  }

  // Archivos soltados en la página. autostart: subir ya a folder (explorador);
  // si no, se muestran y se pregunta la carpeta (editor).
  export function addFiles(list, { folder = '', autostart = false, insert = null } = {}) {
    if (uploading) {
      ontoast?.('Espera a que termine la subida en curso', 'error')
      if (insert) onrelease?.(insert)
      return
    }
    openUpload(folder)
    asking = !autostart
    insertCtx = insert
    insertOn = !!insert // soltar en el editor: insertar por defecto
    syncMeta()
    for (const f of list) {
      try {
        uppy.addFile({ name: f.name, type: f.type, data: f, source: 'Local' })
      } catch (e) {
        if (!e.isRestriction) ontoast?.(`${f.name}: ${e.message}`, 'error')
      }
    }
    if (autostart && uppy.getFiles().length) uppy.upload().catch(() => {})
  }

  function close() {
    if (uploading) return
    releaseInsert()
    open = false
    uppy.cancelAll()
  }

  // El panel de Uppy se monta mientras el modal está abierto.
  function dashboard(node) {
    uppy.use(Dashboard, {
      id: 'Dashboard',
      inline: true,
      target: node,
      width: '100%',
      height: 300,
      theme: theme.current === 'dark' ? 'dark' : 'light',
      proudlyDisplayPoweredByUppy: false,
      showProgressDetails: true,
      note: mode.kind === 'replace' ? 'Un solo archivo; conserva el nombre y la ruta del original' : 'Hasta 20 MB por archivo',
    })
    return {
      destroy() {
        const p = uppy.getPlugin('Dashboard')
        if (p) uppy.removePlugin(p)
      },
    }
  }

  const fmt = (n) => (n < 1024 ? `${n} B` : n < 1048576 ? `${Math.round(n / 1024)} KB` : `${(n / 1048576).toFixed(1)} MB`)

  onDestroy(() => uppy.destroy())
</script>

<Modal {open} title={mode.kind === 'replace' ? 'Reemplazar archivo' : 'Subir archivos'} wide={mode.kind === 'upload' ? 'xl' : true} onclose={close}>
  <div class="grid gap-5 {mode.kind === 'upload' ? 'md:grid-cols-[17rem_minmax(0,1fr)]' : ''}">
    {#if mode.kind === 'upload'}
      <div class="flex min-w-0 flex-col gap-1.5">
        <span class="label">{asking ? '¿En qué carpeta se guardan?' : 'Carpeta de destino'}</span>
        <FolderPicker {folders} bind:value={dir} disabled={uploading} height="19rem" />
      </div>
    {/if}
    <div class="flex min-w-0 flex-col gap-4">
      {#if mode.kind === 'replace'}
        <p class="text-sm text-slate-600">
          El archivo que elijas sustituirá a <code class="rounded bg-slate-100 px-1.5 py-0.5 text-[13px]">{mode.path}</code>; el
          documento seguirá usándolo con el mismo nombre.
        </p>
      {/if}
      {#if mode.kind === 'upload' || replacingImage}
        <label class="flex items-start gap-2.5 text-sm text-slate-700">
          <input type="checkbox" bind:checked={optimize} class="mt-0.5 size-4 accent-blue-800" disabled={uploading} />
          <span>
            <span class="font-medium">Optimizar imágenes sin pérdida</span>
            <span class="block text-xs text-slate-500">
              PNG y JPEG ocupan menos sin cambiar un solo píxel; se conserva su tamaño en el documento.
            </span>
          </span>
        </label>
      {/if}
      {#if insertCtx?.replace && mode.kind === 'upload'}
        <p class="rounded-lg border border-slate-200 p-3 text-sm text-slate-600">
          La imagen subida sustituirá a la que usa el código en <code class="font-mono text-xs">{insertCtx.path}</code>.
        </p>
      {:else if hasMedia}
        <fieldset class="flex flex-col gap-2 rounded-lg border border-slate-200 p-3" disabled={uploading}>
          <label class="flex items-start gap-2.5 text-sm text-slate-700">
            <input type="checkbox" bind:checked={insertOn} class="mt-0.5 size-4 accent-blue-800" />
            <span>
              <span class="font-medium">Insertar en el documento</span>
              <span class="block text-xs text-slate-500">
                {insertCtx ? 'Donde soltaste las imágenes' : `En la posición del cursor de ${editingFile}`}, como
                <code>#image</code>; con pie de figura, dentro de <code>#figure</code>.
              </span>
            </span>
          </label>
          {#if insertOn}
            <ul class="flex max-h-40 flex-col gap-1.5 overflow-y-auto">
              {#each mediaFiles as f (f.id)}
                <li class="flex items-center gap-2">
                  <span class="w-32 shrink-0 truncate font-mono text-xs text-slate-600" title={f.name}>{f.name}</span>
                  <input
                    class="h-8 min-w-0 flex-1 rounded-md border border-slate-300 bg-surface px-2 text-[13px] outline-none focus:border-primary focus:ring-2 focus:ring-primary/20"
                    placeholder="Pie de figura (opcional)"
                    bind:value={captions[f.id]}
                    aria-label="Pie de figura de {f.name}"
                  />
                  <span class="w-14 shrink-0 text-right font-mono text-[11px] {captions[f.id]?.trim() ? 'text-primary' : 'text-slate-400'}">
                    {captions[f.id]?.trim() ? '#figure' : '#image'}
                  </span>
                </li>
              {/each}
            </ul>
          {/if}
        </fieldset>
      {/if}
      {#key mode}
        <div use:dashboard></div>
      {/key}
    </div>
  </div>
</Modal>

<style>
  /* Uppy con los colores del tema. */
  :global(.uppy-Root) {
    font-family: inherit;
  }
  :global(.uppy-StatusBar-actionBtn--upload),
  :global(.uppy-StatusBar-actionBtn--done) {
    background-color: var(--color-primary) !important;
    color: #fff !important;
  }
  :global(.uppy-StatusBar-actionBtn--upload:hover) {
    background-color: var(--color-primary-hover) !important;
  }
  :global(.uppy-Dashboard-browse),
  :global(.uppy-DashboardContent-addMore),
  :global(.uppy-DashboardContent-back) {
    color: var(--color-primary) !important;
  }
  :global(.uppy-StatusBar-progress) {
    background-color: var(--color-primary) !important;
  }
</style>
