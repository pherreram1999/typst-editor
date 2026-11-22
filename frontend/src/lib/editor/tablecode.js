// Tablas Typst ↔ modelo de cuadrícula para el editor visual de tablas.
//
//   findTables(text)           -> [{ from, to }]  llamadas table(…) del texto (from = inicio de «table»)
//   tableAt(text, pos)         -> { from, to } | null   la tabla que contiene pos (la más interna)
//   parseTable(src)            -> { ok: true, model } | { ok: false, error }
//   newTable(rows, cols, header) -> model
//   generate(model, indent)    -> código «table(…)» (sin «#»)
//
// El modelo es una cuadrícula R × C de celdas ancla { r, c, rs, cs, body, extra, lead }
// (las posiciones cubiertas por colspan/rowspan no tienen celda propia). body es
// { kind: 'content', text } (el interior de [ … ], tal cual) o { kind: 'expr', text }
// (otra expresión: "texto", una llamada…). extra son los demás argumentos con
// nombre de table.cell (fill, align…), tal cual. El código se regenera en orden
// de filas, que con la colocación automática de typst reproduce la misma
// cuadrícula; el contenido de las celdas y los demás argumentos se copian sin
// tocarlos, así que editar no pierde nada.
//
// Operaciones (mutan el modelo): insertRow, insertCol, deleteRow, deleteCol,
// merge, split, normalizeRect, setRows, setCols.

// ---------- escáner de código Typst ----------

const ID = /[\p{L}\p{N}_-]/u
const isId = (c) => !!c && ID.test(c)

// Fin de una cadena "…" que empieza en i.
function skipString(s, i) {
  for (let j = i + 1; j < s.length; j++) {
    if (s[j] === '\\') j++
    else if (s[j] === '"') return j + 1
  }
  return s.length
}

function skipLineComment(s, i) {
  const j = s.indexOf('\n', i)
  return j < 0 ? s.length : j
}

function skipBlockComment(s, i) {
  let depth = 0
  for (let j = i; j < s.length - 1; j++) {
    if (s[j] === '/' && s[j + 1] === '*') (depth++, j++)
    else if (s[j] === '*' && s[j + 1] === '/') {
      depth--
      j++
      if (depth === 0) return j + 1
    }
  }
  return s.length
}

// Fin de una fórmula $…$ que empieza en i.
function skipMath(s, i) {
  for (let j = i + 1; j < s.length; j++) {
    if (s[j] === '\\') j++
    else if (s[j] === '"') j = skipString(s, j) - 1
    else if (s[j] === '$') return j + 1
  }
  return s.length
}

// Fin de un bloque `raw` (uno o tres+ acentos graves) que empieza en i.
function skipRaw(s, i) {
  let n = 0
  while (s[i + n] === '`') n++
  if (n === 2) return i + 2 // `` vacío
  const close = '`'.repeat(n)
  const j = s.indexOf(close, i + n)
  return j < 0 ? s.length : j + n
}

// Fin de un grupo de código ( … ) o { … } que empieza en i (s[i] es la apertura).
function skipCode(s, i) {
  const close = s[i] === '(' ? ')' : '}'
  for (let j = i + 1; j < s.length; j++) {
    const ch = s[j]
    if (ch === close) return j + 1
    if (ch === '"') j = skipString(s, j) - 1
    else if (ch === '/' && s[j + 1] === '/') j = skipLineComment(s, j) - 1
    else if (ch === '/' && s[j + 1] === '*') j = skipBlockComment(s, j) - 1
    else if (ch === '(' || ch === '{') j = skipCode(s, j) - 1
    else if (ch === '[') j = skipMarkup(s, j) - 1
    else if (ch === '$') j = skipMath(s, j) - 1
    else if (ch === ')' || ch === '}' || ch === ']') return j + 1 // desequilibrado: cortar aquí
  }
  return s.length
}

// Tras «#» en el marcado: una expresión incrustada (ident.campo(…)[…], "…", (…), {…}).
function skipEmbedded(s, i) {
  let j = i
  if (s[j] === '"') return skipString(s, j)
  if (s[j] === '(' || s[j] === '{') return skipCode(s, j)
  if (s[j] === '[') return skipMarkup(s, j)
  while (j < s.length) {
    while (isId(s[j])) j++
    if (s[j] === '.' && isId(s[j + 1])) {
      j++
      continue
    }
    if (s[j] === '(') j = skipCode(s, j)
    else if (s[j] === '[') j = skipMarkup(s, j)
    else break
    while (s[j] === '(' || s[j] === '[') j = s[j] === '(' ? skipCode(s, j) : skipMarkup(s, j)
    if (!(s[j] === '.' && isId(s[j + 1]))) break
  }
  return j
}

// Fin de un bloque de contenido [ … ] que empieza en i.
function skipMarkup(s, i) {
  for (let j = i + 1; j < s.length; j++) {
    const ch = s[j]
    if (ch === ']') return j + 1
    if (ch === '\\') j++
    else if (ch === '[') j = skipMarkup(s, j) - 1
    else if (ch === '`') j = skipRaw(s, j) - 1
    else if (ch === '$') j = skipMath(s, j) - 1
    else if (ch === '/' && s[j + 1] === '/' && s[j - 1] !== ':') j = skipLineComment(s, j) - 1
    else if (ch === '/' && s[j + 1] === '*') j = skipBlockComment(s, j) - 1
    else if (ch === '#' && (isId(s[j + 1]) || '"({['.includes(s[j + 1] ?? ' '))) j = skipEmbedded(s, j + 1) - 1
  }
  return s.length
}

// Divide los argumentos de un grupo «( … )» (sin los paréntesis) por las comas
// de primer nivel. Devuelve [{ text, lead }] con lead = comentarios previos.
function splitArgs(s) {
  const out = []
  let start = 0
  const push = (end) => {
    const { lead, rest } = takeComments(s.slice(start, end))
    if (rest.trim() || lead) out.push({ text: rest.trim(), lead })
  }
  for (let j = 0; j < s.length; j++) {
    const ch = s[j]
    if (ch === '"') j = skipString(s, j) - 1
    else if (ch === '/' && s[j + 1] === '/') j = skipLineComment(s, j) - 1
    else if (ch === '/' && s[j + 1] === '*') j = skipBlockComment(s, j) - 1
    else if (ch === '(' || ch === '{') j = skipCode(s, j) - 1
    else if (ch === '[') j = skipMarkup(s, j) - 1
    else if (ch === '$') j = skipMath(s, j) - 1
    else if (ch === ',') {
      push(j)
      start = j + 1
    }
  }
  push(s.length)
  return out
}

// Separa los comentarios del principio de un argumento (para no perderlos).
function takeComments(t) {
  let i = 0
  let lead = ''
  for (;;) {
    while (i < t.length && /\s/.test(t[i])) i++
    if (t.startsWith('//', i)) {
      const e = skipLineComment(t, i)
      lead += (lead ? '\n' : '') + t.slice(i, e).trimEnd()
      i = e
    } else if (t.startsWith('/*', i)) {
      const e = skipBlockComment(t, i)
      lead += (lead ? '\n' : '') + t.slice(i, e)
      i = e
    } else break
  }
  return { lead, rest: t.slice(i) }
}

// Llamada «nombre(args)[…][…]» que ocupa todo t: { args: string|null, trailing: string[] }.
function parseCall(t, name) {
  if (!t.startsWith(name)) return null
  let j = name.length
  let args = null
  const trailing = []
  if (t[j] === '(') {
    const e = skipCode(t, j)
    args = t.slice(j + 1, e - 1)
    j = e
  }
  while (t[j] === '[') {
    const e = skipMarkup(t, j)
    trailing.push(t.slice(j + 1, e - 1))
    j = e
  }
  if (j !== t.length || (args === null && !trailing.length)) return null
  return { args, trailing }
}

const NAMED = /^([\p{L}_][\p{L}\p{N}_-]*)\s*:(?!:)\s*/u

// Fin de una llamada table(…)[…]… que empieza en from («table»).
function callEnd(s, from) {
  let j = from + 5
  if (s[j] !== '(' && s[j] !== '[') return -1
  if (s[j] === '(') j = skipCode(s, j)
  while (s[j] === '[') j = skipMarkup(s, j)
  return j
}

// Llamadas a table(…) del texto (incluidas las que van dentro de #figure(…)).
export function findTables(text) {
  const out = []
  const re = /table[([]/g
  for (let m; (m = re.exec(text)); ) {
    const prev = text[m.index - 1]
    if (prev && (isId(prev) || prev === '.')) continue
    const to = callEnd(text, m.index)
    if (to > 0) out.push({ from: m.index, to })
  }
  return out
}

export function tableAt(text, pos) {
  let best = null
  for (const t of findTables(text)) if (t.from <= pos && pos <= t.to && (!best || t.from >= best.from)) best = t
  return best
}

// ---------- modelo ----------

const fail = (error) => ({ ok: false, error })

// Valor «(a, b, c)» que es un arreglo literal: sus elementos; si no, null.
function arrayItems(v) {
  if (v[0] !== '(' || skipCode(v, 0) !== v.length) return null
  const inner = v.slice(1, -1)
  if (!/,/.test(inner) && inner.trim()) return null // «(auto)» es solo un valor entre paréntesis
  const items = splitArgs(inner)
  if (items.some((x) => x.lead || x.text.startsWith('..'))) return null
  return items.map((x) => x.text)
}

const TRACK = /^(auto|\d+(\.\d+)?(fr|pt|mm|cm|in|em|%)|\d+(\.\d+)?%\s*[+-]\s*\d+(\.\d+)?(pt|mm|cm|in|em))$/

// Analiza el código de una tabla («table(…)…»).
export function parseTable(src) {
  const call = parseCall(src.trim(), 'table')
  if (!call) return fail('No es una llamada a table(…) completa')
  const args = [...(call.args !== null ? splitArgs(call.args) : []), ...call.trailing.map((t) => ({ text: `[${t}]`, lead: '' }))]

  const model = { R: 0, C: 1, cells: [], named: [], widths: null, widthsForm: 'none', headerRows: 0, footerRows: 0, header: null, footer: null, lines: [], trailingLead: '' }
  const items = [] // { section, kind: 'cell'|'line', … } en orden

  const cellOf = (a, section) => {
    const t = a.text
    if (t.startsWith('..')) return fail('La tabla genera celdas con «..» y no se puede editar visualmente')
    if (t.startsWith('table.hline') || t.startsWith('table.vline') || t.startsWith('grid.hline') || t.startsWith('grid.vline')) {
      const explicit = /\b(x|y)\s*:/.test(t)
      return { ok: true, item: { section, kind: 'line', src: t, lead: a.lead, explicit } }
    }
    const cc = parseCall(t, 'table.cell') ?? parseCall(t, 'grid.cell')
    let rs = 1
    let cs = 1
    let extra = []
    let body = null
    if (cc) {
      const parts = cc.args !== null ? splitArgs(cc.args) : []
      for (const p of parts) {
        const m = NAMED.exec(p.text)
        if (!m) {
          if (body) return fail('Una celda tiene más de un contenido')
          body = bodyOf(p.text)
          continue
        }
        const v = p.text.slice(m[0].length).trim()
        if (m[1] === 'colspan' || m[1] === 'rowspan') {
          if (!/^\d+$/.test(v) || +v < 1) return fail(`${m[1]}: ${v} no es un número fijo`)
          if (m[1] === 'colspan') cs = +v
          else rs = +v
        } else if (m[1] === 'x' || m[1] === 'y') {
          if (v !== 'auto') return fail('La tabla coloca celdas con x:/y: y no se puede editar visualmente')
        } else extra.push((p.lead ? p.lead + '\n' : '') + p.text)
      }
      for (const tr of cc.trailing) {
        if (body) return fail('Una celda tiene más de un contenido')
        body = { kind: 'content', text: tr }
      }
      if (!body) body = { kind: 'content', text: '' }
    } else body = bodyOf(t)
    return { ok: true, item: { section, kind: 'cell', rs, cs, extra, body, lead: a.lead } }
  }

  let seenBody = false
  for (const a of args) {
    const t = a.text
    if (!t) {
      model.trailingLead += (model.trailingLead ? '\n' : '') + a.lead
      continue
    }
    const nm = NAMED.exec(t)
    if (nm) {
      model.named.push({ name: nm[1], value: t.slice(nm[0].length).trim(), lead: a.lead })
      continue
    }
    for (const [key, name] of [['header', 'table.header'], ['footer', 'table.footer']]) {
      if (!t.startsWith(name)) continue
      const hc = parseCall(t, name)
      if (!hc) return fail(`No se entiende ${name}`)
      if (model[key]) return fail(`La tabla tiene más de un ${name}`)
      if (key === 'header' && seenBody) return fail('El encabezado no está al principio de la tabla')
      const sub = [...(hc.args !== null ? splitArgs(hc.args) : []), ...hc.trailing.map((x) => ({ text: `[${x}]`, lead: '' }))]
      const sec = { named: [], lead: a.lead, trailing: hc.args === null }
      model[key] = sec
      for (const p of sub) {
        if (!p.text) continue
        const m = NAMED.exec(p.text)
        if (m) {
          sec.named.push((p.lead ? p.lead + '\n' : '') + p.text)
          continue
        }
        const r = cellOf(p, key)
        if (!r.ok) return r
        items.push(r.item)
      }
    }
    if (t.startsWith('table.header') || t.startsWith('table.footer')) continue
    if (model.footer) return fail('Hay celdas después del pie de la tabla')
    const r = cellOf(a, 'body')
    if (!r.ok) return r
    if (r.item.kind === 'cell') seenBody = true
    items.push(r.item)
  }

  // Columnas
  const colArg = model.named.find((n) => n.name === 'columns')
  if (colArg) {
    const v = colArg.value
    const arr = arrayItems(v)
    if (/^\d+$/.test(v)) {
      model.C = +v
      model.widthsForm = 'int'
    } else if (arr) {
      model.C = arr.length
      model.widths = arr
      model.widthsForm = 'array'
    } else if (TRACK.test(v)) {
      model.C = 1
      model.widths = [v]
      model.widthsForm = 'single'
    } else return fail(`No se puede saber cuántas columnas da «columns: ${v}»`)
    if (model.C < 1) return fail('La tabla no tiene columnas')
  }
  for (const n of model.named) {
    if (n.name === 'align' || n.name === 'rows') n.items = arrayItems(n.value)
  }

  // Colocación automática (como typst): orden de filas saltando lo ocupado.
  const C = model.C
  const occ = [] // occ[r][c] = celda
  const free = (r, c, rs, cs) => {
    if (c + cs > C) return false
    for (let y = r; y < r + rs; y++) for (let x = c; x < c + cs; x++) if (occ[y]?.[x]) return false
    return true
  }
  let cur = 0 // índice lineal r * C + c
  let maxRow = 0 // filas usadas hasta ahora
  let lastRow = -1 // fila de la última celda colocada
  const startSection = () => {
    // Las secciones (encabezado, cuerpo, pie) empiezan en una fila nueva.
    const row = Math.max(maxRow, Math.ceil(cur / C))
    cur = row * C
    return row
  }
  let bodyStart = 0
  let footStart = -1
  let prevSection = 'header'
  for (const it of items) {
    if (it.section !== prevSection) {
      const row = startSection()
      if (it.section === 'body') bodyStart = row
      if (it.section === 'footer') {
        if (prevSection === 'header') bodyStart = row
        footStart = row
      }
      prevSection = it.section
    }
    if (it.kind === 'line') {
      model.lines.push({ src: it.src, lead: it.lead, explicit: it.explicit, at: it.explicit ? null : lastRow + 1, section: it.section })
      continue
    }
    if (it.cs > C) return fail(`Una celda ocupa ${it.cs} columnas y la tabla solo tiene ${C}`)
    let p = cur
    while (!free(Math.floor(p / C), p % C, it.rs, it.cs)) p++
    const r = Math.floor(p / C)
    const c = p % C
    const cell = { r, c, rs: it.rs, cs: it.cs, body: it.body, extra: it.extra, lead: it.lead }
    for (let y = r; y < r + it.rs; y++) for (let x = c; x < c + it.cs; x++) (occ[y] ??= [])[x] = cell
    model.cells.push(cell)
    cur = p + it.cs
    maxRow = Math.max(maxRow, r + it.rs)
    lastRow = r
  }
  if (prevSection === 'header' && model.header) bodyStart = startSection()
  model.R = Math.max(maxRow, Math.ceil(cur / C), 1)
  model.headerRows = model.header ? Math.min(bodyStart, model.R) : 0
  model.footerRows = footStart >= 0 ? model.R - footStart : 0
  // Las celdas de una sección no pueden salirse de ella.
  for (const cell of model.cells) {
    if (sectionOf(model, cell.r) !== sectionOf(model, cell.r + cell.rs - 1)) return fail('Una celda combinada cruza el encabezado o el pie')
  }
  fillEmpty(model)
  return { ok: true, model }
}

function bodyOf(t) {
  if (t[0] === '[' && skipMarkup(t, 0) === t.length) return { kind: 'content', text: t.slice(1, -1) }
  return { kind: 'expr', text: t }
}

export function sectionOf(m, r) {
  if (r < m.headerRows) return 'header'
  if (r >= m.R - m.footerRows) return 'footer'
  return 'body'
}

// Mapa de ocupación: grid[r][c] = celda ancla que cubre esa posición.
export function gridOf(m) {
  const g = Array.from({ length: m.R }, () => Array(m.C).fill(null))
  for (const cell of m.cells) for (let y = cell.r; y < cell.r + cell.rs; y++) for (let x = cell.c; x < cell.c + cell.cs; x++) if (g[y]) g[y][x] = cell
  return g
}

const emptyCell = (r, c) => ({ r, c, rs: 1, cs: 1, body: { kind: 'content', text: '' }, extra: [], lead: '' })

// Rellena con celdas vacías las posiciones sin celda (fila incompleta, filas nuevas…).
function fillEmpty(m) {
  const g = gridOf(m)
  for (let r = 0; r < m.R; r++) for (let c = 0; c < m.C; c++) if (!g[r][c]) m.cells.push(emptyCell(r, c))
  sortCells(m)
}

const sortCells = (m) => m.cells.sort((a, b) => a.r - b.r || a.c - b.c)

export function newTable(rows, cols, header = true) {
  const m = { R: rows, C: cols, cells: [], named: [{ name: 'columns', value: String(cols), lead: '' }], widths: null, widthsForm: 'int', headerRows: header ? 1 : 0, footerRows: 0, header: header ? { named: [], lead: '', trailing: true } : null, footer: null, lines: [], trailingLead: '' }
  fillEmpty(m)
  if (header) for (const cell of m.cells) if (cell.r === 0) cell.body.text = `*Columna ${cell.c + 1}*`
  return m
}

export const isEmpty = (cell) => !cell.body.text.trim()

// ---------- operaciones ----------

// Inserta una fila en el índice at (las de debajo bajan). Hereda la sección
// de sus vecinas si las dos son de la misma; entre el encabezado (o el pie) y
// el cuerpo, o al final de un encabezado sin cuerpo, es del cuerpo.
export function insertRow(m, at) {
  const above = at > 0 ? sectionOf(m, at - 1) : null
  const below = at < m.R ? sectionOf(m, at) : null
  const sec = above === below ? above : !above ? (below === 'header' ? 'header' : 'body') : !below ? (above === 'footer' ? 'footer' : 'body') : 'body'
  for (const cell of m.cells) {
    if (cell.r >= at) cell.r++
    else if (cell.r + cell.rs > at) cell.rs++ // la celda combinada atraviesa la fila nueva: crece
  }
  for (const l of m.lines) if (l.at !== null && l.at > at) l.at++
  m.R++
  if (sec === 'header') m.headerRows++
  else if (sec === 'footer') m.footerRows++
  shiftArray(m, 'rows', at, 1)
  fillEmpty(m)
}

export function insertCol(m, at, ref = at) {
  for (const cell of m.cells) {
    if (cell.c >= at) cell.c++
    else if (cell.c + cell.cs > at) cell.cs++
  }
  m.C++
  const r = Math.min(Math.max(ref, 0), m.C - 2)
  if (m.widths) m.widths.splice(at, 0, m.widths[r] ?? 'auto')
  shiftArray(m, 'align', at, 1, r)
  fillEmpty(m)
}

// Elimina la fila r. Las celdas combinadas que la atraviesan se encogen (y
// conservan su contenido); devuelve las celdas con contenido que se pierden.
export function deleteRow(m, r) {
  if (m.R <= 1) return []
  const lost = []
  const sec = sectionOf(m, r)
  m.cells = m.cells.filter((cell) => {
    if (cell.r === r && cell.rs === 1) {
      if (!isEmpty(cell)) lost.push(cell)
      return false
    }
    if (cell.r <= r && r < cell.r + cell.rs) cell.rs--
    else if (cell.r > r) cell.r--
    return true
  })
  m.lines = m.lines.filter((l) => l.at === null || l.at <= r || (l.at--, true))
  m.R--
  if (sec === 'header') m.headerRows--
  else if (sec === 'footer') m.footerRows--
  if (!m.headerRows && m.header) m.header = null
  if (!m.footerRows && m.footer) m.footer = null
  shiftArray(m, 'rows', r, -1)
  fillEmpty(m)
  return lost
}

export function deleteCol(m, c) {
  if (m.C <= 1) return []
  const lost = []
  m.cells = m.cells.filter((cell) => {
    if (cell.c === c && cell.cs === 1) {
      if (!isEmpty(cell)) lost.push(cell)
      return false
    }
    if (cell.c <= c && c < cell.c + cell.cs) cell.cs--
    else if (cell.c > c) cell.c--
    return true
  })
  m.C--
  if (m.widths) m.widths.splice(c, 1)
  shiftArray(m, 'align', c, -1)
  fillEmpty(m)
  return lost
}

// Arreglos por columna (align) o por fila (rows) con un elemento por pista:
// se mantienen alineados con la cuadrícula.
function shiftArray(m, name, at, delta, ref = at) {
  const n = m.named.find((x) => x.name === name)
  const len = name === 'rows' ? m.R - delta : m.C - delta
  if (!n?.items || n.items.length !== len) return
  if (delta > 0) n.items.splice(at, 0, n.items[Math.min(ref, n.items.length - 1)])
  else n.items.splice(at, 1)
}

// Agranda el rectángulo hasta que no corte ninguna celda combinada.
export function normalizeRect(m, rect) {
  let { r0, c0, r1, c1 } = rect
  for (let changed = true; changed; ) {
    changed = false
    for (const cell of m.cells) {
      const inter = cell.r <= r1 && cell.r + cell.rs - 1 >= r0 && cell.c <= c1 && cell.c + cell.cs - 1 >= c0
      if (!inter) continue
      const nr0 = Math.min(r0, cell.r)
      const nc0 = Math.min(c0, cell.c)
      const nr1 = Math.max(r1, cell.r + cell.rs - 1)
      const nc1 = Math.max(c1, cell.c + cell.cs - 1)
      if (nr0 !== r0 || nc0 !== c0 || nr1 !== r1 || nc1 !== c1) {
        ;[r0, c0, r1, c1] = [nr0, nc0, nr1, nc1]
        changed = true
      }
    }
  }
  return { r0, c0, r1, c1 }
}

export function cellsIn(m, { r0, c0, r1, c1 }) {
  return m.cells.filter((cell) => cell.r >= r0 && cell.r <= r1 && cell.c >= c0 && cell.c <= c1)
}

// ¿Se puede combinar? (más de una celda y sin cruzar encabezado/pie)
export function canMerge(m, rect) {
  if (!rect || (rect.r0 === rect.r1 && rect.c0 === rect.c1)) return false
  return sectionOf(m, rect.r0) === sectionOf(m, rect.r1) && cellsIn(m, rect).length > 1
}

// Combina el rectángulo en una celda. Nada se pierde: el contenido de las
// celdas no vacías se une (en orden de lectura, separado por espacios) y la
// celda conserva los argumentos (fill, align…) de la primera.
export function merge(m, rect) {
  rect = normalizeRect(m, rect)
  if (!canMerge(m, rect)) return null
  const inside = cellsIn(m, rect)
  const full = inside.filter((c) => !isEmpty(c))
  const first = inside[0]
  let body
  if (full.length <= 1) body = full[0]?.body ?? first.body
  else body = { kind: 'content', text: full.map((c) => (c.body.kind === 'expr' ? `#(${c.body.text})` : c.body.text.trim())).join(' ') }
  const extra = [...new Set(inside.flatMap((c) => c.extra))].filter((x, i, a) => a.findIndex((y) => argName(y) === argName(x)) === i)
  const lead = inside.map((c) => c.lead).filter(Boolean).join('\n')
  const cell = { r: rect.r0, c: rect.c0, rs: rect.r1 - rect.r0 + 1, cs: rect.c1 - rect.c0 + 1, body, extra, lead }
  m.cells = m.cells.filter((c) => !inside.includes(c))
  m.cells.push(cell)
  sortCells(m)
  return cell
}

const argName = (x) => NAMED.exec(x.replace(/^(\/\/[^\n]*\n|\/\*[\s\S]*?\*\/\s*)*/, ''))?.[1] ?? x

// Separa una celda combinada: su contenido queda en la esquina superior izquierda.
export function split(m, cell) {
  if (cell.rs === 1 && cell.cs === 1) return
  cell.rs = 1
  cell.cs = 1
  fillEmpty(m)
}

// Ajusta las dimensiones añadiendo o quitando filas/columnas al final.
// Solo quita las que estén vacías; devuelve false si alguna tenía contenido.
export function setRows(m, n) {
  n = Math.max(1, n)
  while (m.R < n) insertRow(m, m.R - m.footerRows)
  while (m.R > n) {
    const r = m.R - m.footerRows - 1
    if (r < m.headerRows || m.cells.some((c) => c.r <= r && r < c.r + c.rs && (!isEmpty(c) || c.rs > 1))) return false
    deleteRow(m, r)
  }
  return true
}

export function setCols(m, n) {
  n = Math.max(1, n)
  while (m.C < n) insertCol(m, m.C, m.C - 1)
  while (m.C > n) {
    const c = m.C - 1
    if (m.cells.some((x) => x.c <= c && c < x.c + x.cs && (!isEmpty(x) || x.cs > 1))) return false
    deleteCol(m, c)
  }
  return true
}

// Cambia el número de filas de encabezado (0 = sin encabezado).
export function setHeaderRows(m, n) {
  n = Math.min(Math.max(0, n), m.R - m.footerRows)
  // No cortar celdas combinadas por la línea del encabezado.
  if (m.cells.some((c) => c.r < n && c.r + c.rs > n)) return false
  m.headerRows = n
  if (n && !m.header) m.header = { named: [], lead: '', trailing: false }
  if (!n) m.header = null
  return true
}

// ---------- generación ----------

function cellCode(cell) {
  const b = cell.body
  const spans = []
  if (cell.cs > 1) spans.push(`colspan: ${cell.cs}`)
  if (cell.rs > 1) spans.push(`rowspan: ${cell.rs}`)
  const args = [...spans, ...cell.extra]
  if (!args.length) return b.kind === 'content' ? `[${b.text}]` : b.text
  if (b.kind === 'content') return `table.cell(${args.join(', ')})[${b.text}]`
  return `table.cell(${args.join(', ')}, ${b.text})`
}

const withLead = (lead, text, ind) => (lead ? lead.split('\n').join('\n' + ind) + '\n' + ind + text : text)

function namedValue(m, n) {
  if (n.name === 'columns') {
    if (m.widthsForm === 'int' || m.widthsForm === 'none') return String(m.C)
    if (m.widths.length === 1 && m.widthsForm === 'single') return m.widths[0]
    return `(${m.widths.join(', ')}${m.widths.length === 1 ? ',' : ''})`
  }
  if (n.items) return `(${n.items.join(', ')}${n.items.length === 1 ? ',' : ''})`
  return n.value
}

// Código «table(…)» del modelo; indent es la sangría de la línea donde empieza.
export function generate(m, indent = '') {
  const i1 = indent + '  '
  const i2 = indent + '    '
  const out = []
  const named = [...m.named]
  if (!named.some((n) => n.name === 'columns') && m.C > 1) named.unshift({ name: 'columns', value: String(m.C), lead: '' })
  for (const n of named) out.push(i1 + withLead(n.lead, `${n.name}: ${namedValue(m, n)},`, i1))

  const flowLines = (sec, row) => m.lines.filter((l) => l.section === sec && l.at === row)
  const rowsOf = (from, to, sec, ind) => {
    const lines = []
    for (let r = from; r < to; r++) {
      const cells = m.cells.filter((c) => c.r === r)
      for (const l of flowLines(sec, r)) lines.push(ind + withLead(l.lead, l.src + ',', ind))
      if (!cells.length) continue
      let line = ''
      for (const c of cells) {
        const code = cellCode(c) + ','
        if (c.lead) {
          if (line) lines.push(ind + line.trimEnd())
          lines.push(ind + withLead(c.lead, code, ind))
          line = ''
        } else line += code + ' '
      }
      if (line) lines.push(ind + line.trimEnd())
    }
    for (const l of m.lines.filter((l) => l.section === sec && (l.explicit || l.at >= to))) lines.push(ind + withLead(l.lead, l.src + ',', ind))
    return lines
  }
  const section = (key, name, from, to) => {
    const sec = m[key] ?? { named: [], lead: '', trailing: false }
    const cells = m.cells.filter((c) => c.r >= from && c.r < to)
    const simple = !sec.named.length && cells.every((c) => c.body.kind === 'content' && !c.lead && c.rs === 1 && c.cs === 1) && !m.lines.some((l) => l.section === key)
    if (sec.trailing && simple && to - from === 1) {
      out.push(i1 + withLead(sec.lead, `${name}${cells.map((c) => `[${c.body.text}]`).join('')},`, i1))
      return
    }
    const inner = [...sec.named.map((x) => i2 + x + ','), ...rowsOf(from, to, key, i2)]
    if (to - from === 1 && !sec.named.length && inner.length === 1) {
      out.push(i1 + withLead(sec.lead, `${name}(${inner[0].trim().replace(/,$/, '')}),`, i1))
      return
    }
    out.push(i1 + withLead(sec.lead, `${name}(`, i1), ...inner, i1 + '),')
  }
  if (m.headerRows) section('header', 'table.header', 0, m.headerRows)
  out.push(...rowsOf(m.headerRows, m.R - m.footerRows, 'body', i1))
  if (m.footerRows) section('footer', 'table.footer', m.R - m.footerRows, m.R)
  if (m.trailingLead) out.push(i1 + m.trailingLead.split('\n').join('\n' + i1))
  return `table(\n${out.join('\n')}\n${indent})`
}

// Copia profunda (para deshacer en el diálogo).
export const cloneModel = (m) => structuredClone(m)
