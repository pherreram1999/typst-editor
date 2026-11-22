// Compilador de typst en el navegador (Web Worker) para la vista previa en
// vivo. Usa typst.ts (typst compilado a WebAssembly): el compilador conserva
// en memoria lo que ya calculó, así que tras la primera compilación cada
// cambio solo rehace lo afectado (décimas de segundo en un documento de
// cientos de páginas, en vez de segundos con el typst del servidor).
//
// - Fuentes: las mismas que el typst del servidor, para paginar igual que el
//   PDF. El FontInfo de cada una se calcula con los esqueletos que sirve
//   /api/typst/fonts/meta (se guarda en IndexedDB) y el archivo completo se
//   descarga solo cuando el documento usa la fuente.
// - Archivos: los del espejo de la vista previa (imágenes ya reducidas); el
//   archivo abierto en el editor llega con su texto en vivo (source).
// - Paquetes: /api/typst/packages (el servidor los descarga si hace falta).
// - Páginas: cada compilación produce un delta del documento vectorial; las
//   páginas se convierten a SVG independientes bajo demanda (render), solo las
//   que se ven.
//
// Mensajes (petición → respuesta, con el mismo id):
//   init {projectId, token}      → {typst}
//   files {files: [{path, v}]}   → {}         (descarga los que cambiaron)
//   source {path, text}          (sin respuesta) texto en vivo de un archivo
//   release {path}               (sin respuesta) deja de usar el texto en vivo
//   compile {}                   → {ok, diagnostics, pages: [{width, height}], doc}
//   render {doc, pages: [i…]}    → {pages: [{index, svg}]}
import initCompiler, * as tc from '@myriaddreamin/typst-ts-web-compiler'
import compilerWasm from '@myriaddreamin/typst-ts-web-compiler/wasm?url'
import initRenderer, * as tr from '@myriaddreamin/typst-ts-renderer'
import rendererWasm from '@myriaddreamin/typst-ts-renderer/wasm?url'
import { splitPages } from './svgpage.js'

// Versión de typst que trae el compilador WASM (typst.ts 0.8.0-rc4). Si el
// servidor usa otra, la paginación podría no coincidir con el PDF.
export const WASM_TYPST = '0.15'

const PACKAGES = '/@memory/packages'
const RENDER_MAX = 12 // páginas por sesión del renderizador

let token = ''
let projectId = 0
let compiler = null
let renderer = null
const files = new Map() // ruta → v del archivo descargado
const live = new Set() // rutas con texto en vivo del editor
const memory = new Map() // archivos de paquetes (sistema de archivos del compilador)
let doc = 0 // número de la última compilación correcta
// Documento vectorial: base completa + deltas de cada compilación incremental
// (unos KB). Una sesión del renderizador se crea con la base y los deltas.
let incr = null
let base = null
let deltas = []
let deltaBytes = 0
let lastMain = ''
let spare = null // sesión del renderizador aún sin usar (ver session)

const auth = () => ({ Authorization: `Bearer ${token}` })

async function get(url, type = 'json') {
  const res = await fetch(url, { headers: auth() })
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    const err = new Error(data?.error ?? `Error ${res.status}`)
    err.status = res.status
    throw err
  }
  return type === 'json' ? res.json() : new Uint8Array(await res.arrayBuffer())
}

// Descarga síncrona (solo permitida en workers): el compilador pide fuentes
// y paquetes en mitad de una compilación y espera la respuesta.
function getSync(url) {
  const xhr = new XMLHttpRequest()
  xhr.open('GET', url, false)
  xhr.responseType = 'arraybuffer'
  xhr.setRequestHeader('Authorization', `Bearer ${token}`)
  xhr.send()
  if (xhr.status !== 200) return undefined
  return new Uint8Array(xhr.response)
}

// ---------- fuentes ----------

function idb() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open('typsteditor-fonts', 1)
    req.onupgradeneeded = () => req.result.createObjectStore('catalog')
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}

async function cachedInfos(etag) {
  try {
    const db = await idb()
    return await new Promise((resolve) => {
      const req = db.transaction('catalog').objectStore('catalog').get(etag)
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => resolve(undefined)
    })
  } catch {
    return undefined
  }
}

async function storeInfos(etag, infos) {
  try {
    const db = await idb()
    const tx = db.transaction('catalog', 'readwrite')
    const store = tx.objectStore('catalog')
    store.clear() // solo interesa el catálogo actual
    store.put(infos, etag)
  } catch {
    // Sin IndexedDB se vuelven a calcular la próxima vez.
  }
}

// FontInfo de cada archivo del catálogo, en orden (null si no se pudo leer).
async function fontInfos(cat, builder) {
  let infos = await cachedInfos(cat.etag)
  if (infos?.length === cat.fonts.length) return infos
  const meta = await get('/api/typst/fonts/meta', 'bin')
  const view = new DataView(meta.buffer, meta.byteOffset, meta.byteLength)
  infos = []
  let off = 0
  for (let i = 0; i < cat.fonts.length; i++) {
    const n = view.getUint32(off)
    off += 4
    let info = null
    if (n) {
      try {
        info = builder.get_font_info(meta.subarray(off, off + n)).info
      } catch {
        info = null
      }
    }
    infos.push(info)
    off += n
  }
  await storeInfos(cat.etag, infos)
  return infos
}

async function buildFonts() {
  const cat = await get('/api/typst/fonts')
  const server = cat.typst.match(/(\d+\.\d+)/)?.[1]
  if (server !== WASM_TYPST) {
    throw new Error(`el servidor usa typst ${server} y la vista previa del navegador typst ${WASM_TYPST}`)
  }
  const builder = new tc.TypstFontResolverBuilder()
  const infos = await fontInfos(cat, builder)
  cat.fonts.forEach((f, i) => {
    if (!infos[i]) return
    builder.add_lazy_font({ info: infos[i], conditions: [{ t: 'Sha256', v: f.id }] }, () => getSync(`/api/typst/fonts/${f.id}`))
  })
  return { resolver: await builder.build(), version: cat.typst }
}

// ---------- compilador ----------

async function init(msg) {
  token = msg.token
  projectId = msg.projectId
  await Promise.all([initCompiler({ module_or_path: compilerWasm }), initRenderer({ module_or_path: rendererWasm })])
  const { resolver, version } = await buildFonts()
  const b = new tc.TypstCompilerBuilder()
  // Sistema de archivos del compilador: solo los paquetes (los archivos del
  // proyecto se dan con map_shadow / add_source).
  await b.set_access_model(
    {},
    (p) => (memory.has(p) ? 1 : 0),
    (p) => memory.has(p),
    (p) => p,
    (p) => memory.get(p),
  )
  await b.set_package_registry({}, function (spec) {
    const dir = `${PACKAGES}/${spec.namespace}/${spec.name}/${spec.version}`
    if (memory.has(`${dir}/typst.toml`)) return dir
    const data = getSync(`/api/typst/packages/${spec.namespace}/${spec.name}/${spec.version}`)
    if (!data) return undefined
    this.untar(data, (path, content) => memory.set(`${dir}/${path}`, content))
    return dir
  })
  compiler = await b.build()
  compiler.set_fonts(resolver)
  incr = compiler.create_incr_server()
  renderer = await new tr.TypstRendererBuilder().build()
  return { typst: version }
}

// Descarga los archivos nuevos o modificados y olvida los borrados.
async function syncFiles({ files: list }) {
  const want = new Map(list.map((f) => [f.path, f.v]))
  for (const p of files.keys()) {
    if (!want.has(p)) {
      files.delete(p)
      if (!live.has(p)) compiler.unmap_shadow('/' + p)
    }
  }
  const todo = list.filter((f) => files.get(f.path) !== f.v && !live.has(f.path))
  let next = 0
  const worker = async () => {
    while (next < todo.length) {
      const f = todo[next++]
      const q = `path=${encodeURIComponent(f.path)}&v=${encodeURIComponent(f.v)}`
      const data = await get(`/api/projects/${projectId}/preview/file?${q}`, 'bin')
      if (!live.has(f.path)) compiler.map_shadow('/' + f.path, data)
      files.set(f.path, f.v)
    }
  }
  await Promise.all(Array.from({ length: Math.min(6, todo.length) }, worker))
  return {}
}

function source({ path, text }) {
  live.add(path)
  compiler.add_source('/' + path, text)
}

// El archivo deja de editarse aquí: en la próxima sincronización se toma la
// versión del servidor.
function release({ path }) {
  live.delete(path)
  files.delete(path)
}

function diagnostic(d) {
  const [line, col] = (d.range ?? '0:0').split('-')[0].split(':').map(Number)
  return { file: d.path.replace(/^\//, ''), line: line + 1, column: col + 1, severity: d.severity, message: d.message }
}

function compile({ main }) {
  // Con otro archivo principal, o demasiados deltas acumulados, se vuelve a
  // empezar desde un documento completo.
  if (main !== lastMain || deltas.length > 400 || deltaBytes > 16 << 20) {
    incr.reset()
    base = null
    lastMain = main
  }
  const r = compiler.incr_compile('/' + main, null, incr, 3)
  // incr_compile no devuelve los avisos de una compilación correcta: se piden
  // a una compilación normal, que ya está toda en caché (milisegundos).
  let diags = r.diagnostics
  if (r.result) {
    const world = compiler.snapshot(null, '/' + main, null)
    try {
      diags = world.compile(0, 3)?.diagnostics
    } finally {
      world.free()
    }
  }
  compiler.evict_cache(30)
  const diagnostics = (diags ?? []).filter((d) => d.severity === 'error' || d.severity === 'warning').map(diagnostic)
  if (!r.result) return { ok: false, diagnostics, output: diagnostics.find((d) => d.severity === 'error')?.message ?? 'error de compilación' }
  if (base) {
    deltas.push(r.result)
    deltaBytes += r.result.length
  } else {
    base = r.result
    deltas = []
    deltaBytes = 0
  }
  doc++
  spare?.free()
  spare = null
  const ses = session()
  const info = ses.pages_info
  const pages = []
  for (let i = 0; i < info.page_count; i++) {
    const p = info.page(i)
    pages.push({ width: p.width_pt, height: p.height_pt })
  }
  spare = ses // aún no ha dibujado nada: sirve para el primer lote
  return { ok: true, diagnostics, pages, doc }
}

// Sesión nueva del renderizador: dentro de una misma sesión el renderizador
// omite lo que ya emitió (está pensado para parchear un DOM), así que cada
// lote de páginas se dibuja en una sesión propia.
function session() {
  if (spare) {
    const s = spare
    spare = null
    return s
  }
  const o = new tr.CreateSessionOptions()
  o.format = 'vector'
  o.artifact_content = base
  const ses = renderer.create_session(o)
  for (const d of deltas) renderer.manipulate_data(ses, 'merge', d)
  return ses
}

function render({ doc: d, pages: want }) {
  if (d !== doc || !base) return { pages: [], stale: true }
  const sorted = [...new Set(want)].sort((a, b) => a - b)
  const out = []
  while (sorted.length) {
    // Un lote: páginas consecutivas (o casi) hasta RENDER_MAX.
    const first = sorted[0]
    let n = 1
    while (n < sorted.length && sorted[n] - first < RENDER_MAX) n++
    const batch = sorted.splice(0, n)
    const last = batch[batch.length - 1]
    const ses = session()
    try {
      const info = ses.pages_info
      let y = 0
      let top = 0
      let bottom = 0
      for (let i = 0; i <= last; i++) {
        const h = info.page(i).height_pt
        if (i === first) top = y
        y += h
        if (i === last) bottom = y
      }
      const svg = ses.render_in_window(0, top + 0.5, ses.doc_width, bottom - 0.5)
      for (const p of splitPages(svg)) if (batch.includes(p.index)) out.push({ index: p.index, svg: p.svg })
    } finally {
      ses.free()
    }
  }
  return { pages: out }
}

const handlers = { init, files: syncFiles, source, release, compile, render }

self.onmessage = async (e) => {
  const { id, type, ...msg } = e.data
  try {
    const result = await handlers[type](msg)
    if (id !== undefined) self.postMessage({ id, result })
  } catch (err) {
    if (id !== undefined) self.postMessage({ id, error: err?.message ?? String(err), status: err?.status })
  }
}
