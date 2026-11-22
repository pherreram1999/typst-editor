<script>
  // Diálogo para definir una imagen del documento: elegirla del proyecto
  // (FolderPicker en modo archivo, con vista previa) o subir una nueva.
  // - openFor({ mode: 'insert' }): inserta #image (o #figure si hay pie).
  // - openFor({ mode: 'replace', current }): cambia la ruta de un image("…").
  // onchoose({ path, caption }) con la imagen elegida; onupload() para pasar a
  // la subida; onclose() al cancelar.
  import Modal from '../Modal.svelte'
  import FolderPicker from '../FolderPicker.svelte'
  import { imageURL } from './imageCache.js'

  let { projectId, files = [], onchoose, onupload, onclose } = $props()

  const IMAGE = /\.(png|jpe?g|gif|svg|webp)$/i
  const images = $derived(files.filter((f) => IMAGE.test(f.path)))
  const imagePaths = $derived(images.map((f) => f.path))
  const folders = $derived([...new Set(files.map((f) => (f.path.includes('/') ? f.path.slice(0, f.path.lastIndexOf('/')) : '')).filter(Boolean))])

  let open = $state(false)
  let mode = $state('insert')
  let current = $state('')
  let tab = $state('project') // project | upload
  let picked = $state('')
  let caption = $state('')
  let preview = $state(null) // { url, w, h } | null
  let previewErr = $state('')
  let pickerKey = $state(0)

  export function openFor(target) {
    mode = target.mode
    current = target.current ?? ''
    picked = imagePaths.includes(current) ? current : ''
    caption = ''
    tab = images.length ? 'project' : 'upload'
    pickerKey++
    open = true
  }

  // Vista previa de la imagen elegida.
  $effect(() => {
    const p = picked
    preview = null
    previewErr = ''
    if (!p) return
    const f = images.find((x) => x.path === p)
    let alive = true
    imageURL(projectId, p, f?.size).then(
      (url) => {
        if (!alive) return
        const img = new Image()
        img.onload = () => alive && (preview = { url, w: img.naturalWidth, h: img.naturalHeight })
        img.onerror = () => alive && (preview = { url, w: 0, h: 0 })
        img.src = url
      },
      () => alive && (previewErr = 'No se pudo cargar la vista previa'),
    )
    return () => (alive = false)
  })

  const fmtSize = (n) => (n < 1024 ? `${n} B` : n < 1048576 ? `${Math.round(n / 1024)} KB` : `${(n / 1048576).toFixed(1)} MB`)
  const pickedFile = $derived(images.find((x) => x.path === picked))

  function choose(p = picked) {
    if (!p) return
    open = false
    onchoose?.({ path: p, caption: mode === 'insert' ? caption.trim() : '' })
  }
  function cancel() {
    open = false
    onclose?.()
  }
  function toUpload() {
    open = false
    onupload?.()
  }
</script>

<Modal {open} title={mode === 'replace' ? 'Cambiar imagen' : 'Insertar imagen'} wide="xl" onclose={cancel}>
  <div class="flex flex-col gap-4">
    <div class="flex gap-1 border-b border-slate-200" role="tablist">
      {#each [['project', 'Del proyecto'], ['upload', 'Subir nueva']] as [k, label]}
        <button
          type="button"
          role="tab"
          aria-selected={tab === k}
          class="-mb-px cursor-pointer border-b-2 px-3 py-2 text-sm font-medium {tab === k ? 'border-primary text-primary' : 'border-transparent text-slate-500 hover:text-slate-900'}"
          onclick={() => (tab = k)}>{label}</button
        >
      {/each}
    </div>

    {#if tab === 'project'}
      <div class="grid gap-5 md:grid-cols-[18rem_minmax(0,1fr)]">
        {#key pickerKey}
          <FolderPicker pick="file" files={imagePaths} {folders} bind:value={picked} height="20rem" onpick={(p) => choose(p)} />
        {/key}
        <div class="flex min-w-0 flex-col gap-3">
          <div class="flex h-64 items-center justify-center overflow-hidden rounded-lg border border-slate-200 bg-slate-100">
            {#if preview}
              <img src={preview.url} alt={picked} class="max-h-full max-w-full bg-white object-contain" />
            {:else if previewErr}
              <p class="text-sm text-red-700">{previewErr}</p>
            {:else if picked}
              <p class="text-sm text-slate-500">Cargando…</p>
            {:else}
              <p class="px-6 text-center text-sm text-slate-500">Elige una imagen en el árbol para verla aquí.</p>
            {/if}
          </div>
          {#if pickedFile}
            <p class="flex flex-wrap gap-x-3 text-xs text-slate-500">
              <code class="font-mono text-slate-700">{pickedFile.path}</code>
              <span>{fmtSize(pickedFile.size)}</span>
              {#if preview?.w}<span>{preview.w} × {preview.h} px</span>{/if}
              {#if mode === 'replace' && picked === current}<span class="font-medium text-amber-700">(es la imagen actual)</span>{/if}
            </p>
          {/if}
          {#if mode === 'insert'}
            <label class="flex flex-col gap-1.5">
              <span class="label">Pie de figura (opcional)</span>
              <input class="input !h-9" bind:value={caption} placeholder="Con pie se inserta como #figure; sin pie, como #image" />
            </label>
          {/if}
        </div>
      </div>
      <div class="flex items-center justify-end gap-2">
        <button type="button" class="btn-secondary btn-sm" onclick={cancel}>Cancelar</button>
        <button type="button" class="btn-primary btn-sm" disabled={!picked || (mode === 'replace' && picked === current)} onclick={() => choose()}>
          {mode === 'replace' ? 'Usar esta imagen' : caption.trim() ? 'Insertar figura' : 'Insertar imagen'}
        </button>
      </div>
    {:else}
      <div class="flex flex-col items-center gap-4 rounded-lg border-2 border-dashed border-slate-300 px-6 py-12 text-center">
        <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" class="text-slate-400" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2" /><circle cx="9" cy="9" r="2" /><path d="m21 15-5-5L5 21" /></svg>
        <div class="flex flex-col gap-1">
          <p class="font-semibold">Sube una imagen nueva</p>
          <p class="text-sm text-slate-500">
            {mode === 'replace'
              ? 'Se guardará en la carpeta que elijas y el código pasará a usarla.'
              : 'Elige la carpeta y, si quieres, un pie de figura; se insertará donde está el cursor.'}
          </p>
        </div>
        <button type="button" class="btn-primary btn-sm" onclick={toUpload}>Elegir archivo…</button>
      </div>
    {/if}
  </div>
</Modal>
