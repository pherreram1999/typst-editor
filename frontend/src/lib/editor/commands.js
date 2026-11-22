// Comandos de la barra de herramientas del editor (marcado Typst) sobre un
// EditorView de CodeMirror. Todos devuelven true si hicieron algo (para
// usarlos también como atajos de teclado) y dejan el foco en el editor.
import { EditorSelection } from '@codemirror/state'
import { undo, redo, undoDepth, redoDepth } from '@codemirror/commands'
import { openSearchPanel } from '@codemirror/search'

const isWord = (c) => /[\p{L}\p{N}_-]/u.test(c)

function done(view, tr) {
  view.dispatch(tr, { scrollIntoView: true, userEvent: 'input' })
  view.focus()
  return true
}

// Alterna un marcado simétrico (*negrita*, _cursiva_, `código`).
// - Selección ya envuelta (por dentro o por fuera): se quita.
// - Selección: se envuelve y queda seleccionado el texto.
// - Cursor dentro de una palabra: se envuelve la palabra.
// - Cursor suelto: se insertan las dos marcas con el cursor en medio.
export function toggleMark(view, mark) {
  const { state } = view
  const n = mark.length
  return done(
    view,
    state.changeByRange((r) => {
      let { from, to } = r
      const doc = state.doc
      if (from === to) {
        const line = doc.lineAt(from)
        let a = from
        let b = to
        while (a > line.from && isWord(doc.sliceString(a - 1, a))) a--
        while (b < line.to && isWord(doc.sliceString(b, b + 1))) b++
        if (a === b) {
          // Cursor entre dos marcas vacías («**»): quitarlas.
          if (doc.sliceString(from - n, from) === mark && doc.sliceString(to, to + n) === mark) {
            return { changes: { from: from - n, to: to + n, insert: '' }, range: EditorSelection.cursor(from - n) }
          }
          return { changes: { from, insert: mark + mark }, range: EditorSelection.cursor(from + n) }
        }
        from = a
        to = b
      }
      const text = doc.sliceString(from, to)
      if (doc.sliceString(from - n, from) === mark && doc.sliceString(to, to + n) === mark) {
        return {
          changes: [
            { from: from - n, to: from, insert: '' },
            { from: to, to: to + n, insert: '' },
          ],
          range: EditorSelection.range(from - n, to - n),
        }
      }
      if (text.length >= 2 * n && text.startsWith(mark) && text.endsWith(mark)) {
        const inner = text.slice(n, text.length - n)
        return { changes: { from, to, insert: inner }, range: EditorSelection.range(from, from + inner.length) }
      }
      return {
        changes: [
          { from, insert: mark },
          { from: to, insert: mark },
        ],
        range: EditorSelection.range(from + n, to + n),
      }
    }),
  )
}

// Envuelve en una función de contenido: #underline[…]. Si el cursor ya está
// dentro de una, la quita.
export function toggleFunc(view, name) {
  const { state } = view
  const open = `#${name}[`
  return done(
    view,
    state.changeByRange((r) => {
      const doc = state.doc
      const line = doc.lineAt(r.from)
      const before = doc.sliceString(line.from, r.from)
      const start = before.lastIndexOf(open)
      if (start >= 0 && before.indexOf(']', start) < 0) {
        const s = line.from + start
        const close = doc.sliceString(r.to, line.to).indexOf(']')
        if (close >= 0) {
          const e = r.to + close
          return {
            changes: [
              { from: s, to: s + open.length, insert: '' },
              { from: e, to: e + 1, insert: '' },
            ],
            range: EditorSelection.range(r.from - open.length, r.to - open.length),
          }
        }
      }
      return {
        changes: [
          { from: r.from, insert: open },
          { from: r.to, insert: ']' },
        ],
        range: EditorSelection.range(r.from + open.length, r.to + open.length),
      }
    }),
  )
}

// Líneas tocadas por la selección principal.
function selectedLines(state) {
  const r = state.selection.main
  const first = state.doc.lineAt(r.from).number
  let last = state.doc.lineAt(r.to).number
  if (last > first && r.to === state.doc.line(last).from) last-- // selección que acaba al inicio de línea
  const lines = []
  for (let i = first; i <= last; i++) lines.push(state.doc.line(i))
  return lines
}

// Encabezado de nivel 1–3 (0: texto normal).
export function setHeading(view, level) {
  const changes = selectedLines(view.state).map((l) => {
    const m = /^(\s*)=+\s+/.exec(l.text)
    const prefix = level ? '='.repeat(level) + ' ' : ''
    return m ? { from: l.from + m[1].length, to: l.from + m[0].length, insert: prefix } : { from: l.from, insert: prefix }
  })
  return done(view, { changes })
}

const LIST = { bullets: { re: /^(\s*)-\s+/, prefix: '- ' }, numbered: { re: /^(\s*)(\+|\d+\.)\s+/, prefix: '+ ' } }

// Alterna listas con viñetas (-) o numeradas (+) en las líneas seleccionadas.
export function toggleList(view, kind) {
  const { re, prefix } = LIST[kind]
  const other = LIST[kind === 'bullets' ? 'numbered' : 'bullets'].re
  const lines = selectedLines(view.state).filter((l) => l.text.trim() !== '' || view.state.selection.main.empty)
  const all = lines.length > 0 && lines.every((l) => re.test(l.text))
  const changes = lines.map((l) => {
    const m = re.exec(l.text) ?? other.exec(l.text)
    const indent = /^\s*/.exec(l.text)[0]
    if (all) return { from: l.from + m[1].length, to: l.from + m[0].length, insert: '' }
    if (m) return { from: l.from + m[1].length, to: l.from + m[0].length, insert: prefix }
    return { from: l.from + indent.length, insert: prefix }
  })
  return done(view, { changes })
}

// Inserta texto en lugar de la selección y selecciona [a, b) dentro de él.
function insertAt(view, text, a = text.length, b = a) {
  const r = view.state.selection.main
  return done(view, {
    changes: { from: r.from, to: r.to, insert: text },
    selection: EditorSelection.range(r.from + a, r.from + b),
  })
}

export function insertMath(view) {
  const sel = view.state.sliceDoc(view.state.selection.main.from, view.state.selection.main.to)
  if (sel) return insertAt(view, `$${sel}$`, 1, 1 + sel.length)
  return insertAt(view, '$  $', 2)
}

export function insertLink(view) {
  const sel = view.state.sliceDoc(view.state.selection.main.from, view.state.selection.main.to)
  const url = 'https://'
  const text = `#link("${url}")[${sel || 'texto'}]`
  return insertAt(view, text, 7, 7 + url.length) // la URL queda seleccionada
}

export function insertRef(view, label) {
  // Tras una letra, «@» no inicia una referencia: separar con un espacio.
  const r = view.state.selection.main
  const prev = view.state.sliceDoc(Math.max(0, r.from - 1), r.from)
  const text = (prev && isWord(prev) ? ' ' : '') + '@' + (label ?? '')
  return insertAt(view, text)
}

// Inserta una cita en pos (separada con espacios de las palabras vecinas: tras
// una letra «@» no inicia una referencia, y una letra detrás alargaría la clave).
export function insertCitation(view, pos, code) {
  const doc = view.state.doc
  pos = Math.min(Math.max(pos, 0), doc.length)
  const prev = doc.sliceString(Math.max(0, pos - 1), pos)
  const next = doc.sliceString(pos, pos + 1)
  const text = (prev && /[\p{L}\p{N}_)\]]/u.test(prev) ? ' ' : '') + code + (next && /[\p{L}\p{N}_[(]/u.test(next) ? ' ' : '')
  view.dispatch({ changes: { from: pos, insert: text }, selection: { anchor: pos + text.length }, scrollIntoView: true, userEvent: 'input' })
  view.focus()
  return true
}

// Etiquetas <…> del documento, para el menú de referencias.
export function labelsIn(text) {
  const set = new Set()
  for (const m of text.matchAll(/<([\p{L}\p{N}_][\p{L}\p{N}_:.-]*)>/gu)) set.add(m[1])
  return [...set]
}

export const TABLE = {
  text: '#table(\n  columns: 3,\n  table.header[*Columna 1*][*Columna 2*][*Columna 3*],\n  [], [], [],\n)',
  select: ['#table(\n  columns: 3,\n  table.header[*'.length, '#table(\n  columns: 3,\n  table.header[*Columna 1'.length],
}

export const FIGURE = {
  text: '#figure(\n  image("ruta/de/la/imagen.png", width: 80%),\n  caption: [Descripción de la figura],\n) <fig-nueva>',
  select: ['#figure(\n  image("'.length, '#figure(\n  image("ruta/de/la/imagen.png'.length],
}

export const runUndo = (view) => (undo(view), view.focus(), true)
export const runRedo = (view) => (redo(view), view.focus(), true)
export const runSearch = (view) => openSearchPanel(view)

// Formato bajo el cursor, para marcar los botones activos.
export function activeFormats(state) {
  const head = state.selection.main.head
  const line = state.doc.lineAt(head)
  const before = line.text.slice(0, head - line.from)
  const odd = (ch) => (before.split(ch).length - 1 - (before.split('\\' + ch).length - 1)) % 2 === 1
  const und = before.lastIndexOf('#underline[')
  const h = /^\s*(=+)\s/.exec(line.text)
  return {
    bold: odd('*'),
    italic: odd('_'),
    code: odd('`'),
    underline: und >= 0 && before.indexOf(']', und) < 0,
    heading: h ? Math.min(h[1].length, 6) : 0,
    bullets: LIST.bullets.re.test(line.text),
    numbered: LIST.numbered.re.test(line.text),
    canUndo: undoDepth(state) > 0,
    canRedo: redoDepth(state) > 0,
  }
}

// Atajos de teclado (mismos que muestran los tooltips).
export function formatKeymap(handlers) {
  return [
    { key: 'Mod-b', run: (v) => toggleMark(v, '*') },
    { key: 'Mod-i', run: (v) => toggleMark(v, '_') },
    { key: 'Mod-u', run: (v) => toggleFunc(v, 'underline') },
    { key: 'Mod-e', run: (v) => toggleMark(v, '`') },
    { key: 'Mod-Alt-0', run: (v) => setHeading(v, 0) },
    { key: 'Mod-Alt-1', run: (v) => setHeading(v, 1) },
    { key: 'Mod-Alt-2', run: (v) => setHeading(v, 2) },
    { key: 'Mod-Alt-3', run: (v) => setHeading(v, 3) },
    { key: 'Mod-Shift-8', run: (v) => toggleList(v, 'bullets') },
    { key: 'Mod-Shift-7', run: (v) => toggleList(v, 'numbered') },
    { key: 'Mod-m', run: insertMath },
    { key: 'Mod-k', run: insertLink },
    { key: 'Mod-Alt-i', run: () => (handlers.image?.(), true) },
    { key: 'Mod-Alt-c', run: () => (handlers.cite?.(), true) },
  ]
}
