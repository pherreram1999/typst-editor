<script>
  // Selección de un proyecto en la computadora para importarlo: un .zip, una
  // carpeta o archivos sueltos (botones o arrastrar y soltar). Produce
  // `selection` = { label, size, count, zip } o { label, size, count, files: [{ path, file }] }.
  // Los archivos ocultos (.git, .DS_Store…) y __MACOSX no se suben.
  let { selection = $bindable(null), disabled = false, onname } = $props()

  let zipInput = $state()
  let dirInput = $state()
  let over = $state(false)
  let reading = $state(false)
  let error = $state('')

  const hidden = (path) => path.split('/').some((p) => p.startsWith('.') || p === '__MACOSX' || p === 'Thumbs.db' || p === 'desktop.ini')
  const isZip = (f) => /\.zip$/i.test(f.name)
  const baseName = (n) => n.replace(/\.zip$/i, '').replace(/[_-]+/g, ' ').trim()

  function fmtSize(n) {
    if (n < 1024) return `${n} B`
    if (n < 1024 ** 2) return `${(n / 1024).toFixed(0)} KB`
    if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`
    return `${(n / 1024 ** 3).toFixed(2)} GB`
  }

  function setZip(file) {
    error = ''
    selection = { label: file.name, size: file.size, count: 1, zip: file }
    onname?.(baseName(file.name))
  }

  // entries: [{ path, file }] con rutas relativas a la raíz del proyecto.
  function setFiles(entries, label) {
    error = ''
    entries = entries.filter((e) => e.path && !hidden(e.path))
    if (!entries.length) {
      selection = null
      error = 'No se encontraron archivos para importar.'
      return
    }
    if (!entries.some((e) => /\.typ$/i.test(e.path))) {
      selection = null
      error = 'La selección no contiene ningún archivo .typ.'
      return
    }
    selection = { label, size: entries.reduce((s, e) => s + e.file.size, 0), count: entries.length, files: entries }
    onname?.(label)
  }

  function onZipInput(e) {
    const f = e.currentTarget.files?.[0]
    if (f) setZip(f)
    e.currentTarget.value = ''
  }

  function onDirInput(e) {
    const list = [...(e.currentTarget.files ?? [])]
    e.currentTarget.value = ''
    if (!list.length) return
    // webkitRelativePath = «carpeta/sub/archivo»: la carpeta elegida es la raíz.
    const root = list[0].webkitRelativePath.split('/')[0]
    setFiles(
      list.map((file) => ({ path: file.webkitRelativePath.split('/').slice(1).join('/'), file })),
      root,
    )
  }

  // Recorre una carpeta soltada (API de entradas del navegador), sin bajar a
  // las carpetas ocultas (un .git puede pesar mucho).
  async function walk(entry, prefix, out) {
    if (entry.name.startsWith('.') || entry.name === '__MACOSX') return
    if (entry.isFile) {
      const file = await new Promise((res, rej) => entry.file(res, rej))
      out.push({ path: prefix + entry.name, file })
    } else if (entry.isDirectory) {
      const reader = entry.createReader()
      for (;;) {
        // readEntries devuelve los hijos por tandas hasta una vacía.
        const batch = await new Promise((res, rej) => reader.readEntries(res, rej))
        if (!batch.length) break
        for (const child of batch) await walk(child, prefix + entry.name + '/', out)
      }
    }
  }

  async function ondrop(e) {
    e.preventDefault()
    over = false
    if (disabled) return
    const entries = [...(e.dataTransfer?.items ?? [])].map((it) => it.webkitGetAsEntry?.()).filter(Boolean)
    const files = [...(e.dataTransfer?.files ?? [])]
    if (files.length === 1 && isZip(files[0]) && !entries[0]?.isDirectory) return setZip(files[0])
    if (!entries.length) {
      if (files.length) setFiles(files.map((file) => ({ path: file.name, file })), baseName(files[0].name))
      return
    }
    reading = true
    error = ''
    try {
      const out = []
      if (entries.length === 1 && entries[0].isDirectory) {
        // Una carpeta: su contenido es la raíz del proyecto.
        const root = entries[0]
        const tmp = []
        await walk(root, '', tmp)
        for (const x of tmp) out.push({ path: x.path.slice(root.name.length + 1), file: x.file })
        setFiles(out, root.name)
      } else {
        for (const en of entries) await walk(en, '', out)
        const label = entries.find((en) => en.isFile && /\.typ$/i.test(en.name))?.name.replace(/\.typ$/i, '') ?? entries[0].name
        setFiles(out, label)
      }
    } catch {
      error = 'No se pudieron leer los archivos soltados.'
    } finally {
      reading = false
    }
  }
</script>

<div class="flex flex-col gap-2">
  <div
    role="region"
    aria-label="Zona para soltar el proyecto"
    class="flex flex-col items-center gap-3 rounded-lg border-2 border-dashed px-4 py-6 text-center transition {over
      ? 'border-violet-500 bg-violet-50'
      : 'border-slate-300'} {disabled ? 'opacity-60' : ''}"
    ondragover={(e) => {
      e.preventDefault()
      if (!disabled) over = true
    }}
    ondragleave={() => (over = false)}
    {ondrop}
  >
    {#if reading}
      <p class="text-sm text-slate-600">Leyendo archivos…</p>
    {:else if selection}
      <div class="flex w-full items-center gap-3 rounded-md bg-slate-50 p-3 text-left">
        <svg viewBox="0 0 24 24" class="size-8 shrink-0 text-violet-600" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
          {#if selection.zip}
            <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" /><path d="M14 3v5h5M10 6h1M10 9h1M10 12h1M10 15h1" />
          {:else}
            <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
          {/if}
        </svg>
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-semibold">{selection.label}</p>
          <p class="text-xs text-slate-500">
            {selection.zip ? 'Archivo .zip' : `${selection.count} ${selection.count === 1 ? 'archivo' : 'archivos'}`} · {fmtSize(selection.size)}
          </p>
        </div>
        <button type="button" class="btn-secondary btn-sm" {disabled} onclick={() => (selection = null)}>Cambiar</button>
      </div>
    {:else}
      <svg viewBox="0 0 24 24" class="size-9 text-slate-400" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
        <path d="M12 15V4m0 0-4 4m4-4 4 4M5 15v3a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-3" />
      </svg>
      <p class="text-sm text-slate-600">Arrastra aquí un <b>.zip</b> o una <b>carpeta</b> con tu proyecto Typst</p>
      <div class="flex flex-wrap justify-center gap-2">
        <button type="button" class="btn-secondary btn-sm" {disabled} onclick={() => zipInput.click()}>Elegir .zip</button>
        <button type="button" class="btn-secondary btn-sm" {disabled} onclick={() => dirInput.click()}>Elegir carpeta</button>
      </div>
    {/if}
  </div>
  {#if error}<p class="text-sm text-red-600">{error}</p>{/if}
  <p class="text-xs text-slate-500">
    Se crea un proyecto nuevo tuyo con su propio historial. El archivo principal será <code>main.typ</code> (o el primer <code>.typ</code> de la raíz); puedes cambiarlo en los ajustes. Los archivos ocultos (como <code>.git</code>) no se importan.
  </p>
  <input bind:this={zipInput} type="file" accept=".zip,application/zip" class="hidden" onchange={onZipInput} />
  <input bind:this={dirInput} type="file" webkitdirectory class="hidden" onchange={onDirInput} />
</div>
