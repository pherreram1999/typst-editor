// Envoltorio del compilador del navegador (typst.worker.js): peticiones con
// respuesta como promesas. Un worker por vista previa abierta; destroy() lo
// termina y libera su memoria (un documento grande ocupa más de 1 GB).
import { api } from '../api.js'
import { session } from '../session.svelte.js'

export class BrowserCompiler {
  constructor(projectId) {
    this.projectId = projectId
    this.worker = new Worker(new URL('./typst.worker.js', import.meta.url), { type: 'module' })
    this.seq = 0
    this.waiting = new Map()
    this.broken = null // error que dejó el worker inservible
    this.worker.onmessage = (e) => {
      const { id, result, error, status } = e.data
      const w = this.waiting.get(id)
      if (!w) return
      this.waiting.delete(id)
      if (error !== undefined) {
        const err = new Error(error)
        err.status = status
        w.reject(err)
      } else w.resolve(result)
    }
    // Errores que no llegan como respuesta (p. ej. el módulo no carga o el
    // WASM se queda sin memoria): el worker no se puede seguir usando.
    this.worker.onerror = (e) => this.fail(new Error(e.message || 'el compilador del navegador falló'))
  }

  fail(err) {
    this.broken ??= err
    for (const w of this.waiting.values()) w.reject(this.broken)
    this.waiting.clear()
  }

  call(type, msg = {}) {
    if (this.broken) return Promise.reject(this.broken)
    const id = ++this.seq
    return new Promise((resolve, reject) => {
      this.waiting.set(id, { resolve, reject })
      this.worker.postMessage({ id, type, ...msg })
    })
  }

  post(type, msg = {}) {
    if (!this.broken) this.worker.postMessage({ type, ...msg })
  }

  init() {
    return this.call('init', { projectId: this.projectId, token: session.token })
  }

  // Lista los archivos del espejo en el servidor y descarga los que cambiaron.
  async syncFiles() {
    const r = await api.previewFiles(this.projectId)
    await this.call('files', { files: r.files })
    return r
  }

  source(path, text) {
    this.post('source', { path, text })
  }

  release(path) {
    this.post('release', { path })
  }

  compile(main) {
    return this.call('compile', { main })
  }

  render(doc, pages) {
    return this.call('render', { doc, pages })
  }

  destroy() {
    this.fail(new Error('vista previa cerrada'))
    this.worker.terminate()
  }
}
