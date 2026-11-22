// Autocompletado de CodeMirror tras «@»: claves de la bibliografía (las
// añadidas más recientemente primero) y etiquetas <…> del archivo.
import { labelsIn } from '../editor/commands.js'

const TYPE_LABEL = { article: 'artículo', book: 'libro', inproceedings: 'congreso', incollection: 'capítulo', phdthesis: 'tesis', mastersthesis: 'tesis', techreport: 'informe', online: 'web', misc: 'otro' }

/** @param {() => Promise<import('./library.js').LibItem[]>} getItems */
export function citeCompletion(getItems) {
  return async (ctx) => {
    const m = ctx.matchBefore(/@[\p{L}\p{N}_:.-]*/u)
    if (!m) return null
    // Tras una letra no es una referencia (p. ej. un correo).
    const prev = ctx.state.sliceDoc(Math.max(0, m.from - 1), m.from)
    if (prev && /[\p{L}\p{N}_]/u.test(prev)) return null
    let items = []
    try {
      items = await getItems()
    } catch {
      items = []
    }
    if (ctx.aborted) return null
    const n = items.length
    const options = items.map((x, i) => ({
      label: x.key,
      detail: [x.authors, x.year].filter(Boolean).join(', '),
      info: () => {
        const el = document.createElement('div')
        el.style.maxWidth = '22rem'
        el.style.fontSize = '12px'
        const t = document.createElement('div')
        t.style.fontWeight = '600'
        t.textContent = x.title || x.key
        const s = document.createElement('div')
        s.style.opacity = '0.75'
        s.textContent = [x.authors, x.venue, x.year].filter(Boolean).join(' · ') + ` — ${TYPE_LABEL[x.type] ?? x.type} · ${x.file}`
        el.append(t, s)
        return el
      },
      type: 'text',
      section: { name: 'Bibliografía', rank: 0 },
      // Conserva el orden por recientes cuando todas encajan igual de bien.
      boost: n > 1 ? Math.round(99 - (198 * i) / (n - 1)) : 0,
    }))
    const keys = new Set(items.map((x) => x.key))
    for (const l of labelsIn(ctx.state.doc.toString())) {
      if (!keys.has(l)) options.push({ label: l, type: 'variable', detail: 'etiqueta', section: { name: 'Etiquetas', rank: 1 } })
    }
    if (!options.length) return null
    return { from: m.from + 1, options, validFor: /^[\p{L}\p{N}_:.-]*$/u }
  }
}
