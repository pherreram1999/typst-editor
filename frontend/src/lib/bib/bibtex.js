// Análisis y edición de archivos BibTeX/BibLaTeX conservando el texto original.
//
// parseBib(text) devuelve las entradas con las posiciones (en unidades UTF-16,
// las mismas que usa CodeMirror) de su tipo, clave y cada campo. Las funciones
// de edición (setField, removeField, setType, setKey…) devuelven los cambios
// mínimos { from, to, insert } sobre el texto: el editor visual solo reescribe
// lo que el usuario toca, de modo que el resto del archivo (comentarios,
// formato, orden) queda intacto y los diffs de git son pequeños.

const NAME = /[^\s"#%'(),={}]/

// ---------- análisis ----------

/**
 * @typedef {{ name: string, nameFrom: number, nameTo: number, vFrom: number, vTo: number,
 *   raw: string, value: string, complex: boolean }} BibField
 * @typedef {{ type: string, typeFrom: number, typeTo: number, key: string, keyFrom: number,
 *   keyTo: number, from: number, to: number, close: number, fields: BibField[], index: number }} BibEntry
 */

/** @returns {{ entries: BibEntry[], strings: Map<string, string>, errors: {pos: number, line: number, message: string}[] }} */
export function parseBib(text) {
  const entries = []
  const strings = new Map()
  const errors = []
  let i = 0
  const n = text.length

  const lineOf = (pos) => {
    let l = 1
    for (let k = 0; k < pos && k < n; k++) if (text.charCodeAt(k) === 10) l++
    return l
  }
  const fail = (pos, message) => {
    errors.push({ pos, line: lineOf(pos), message })
  }
  const skipWs = () => {
    while (i < n) {
      const c = text[i]
      if (c === ' ' || c === '\t' || c === '\n' || c === '\r') i++
      else if (c === '%') {
        // Comentario de línea (biber lo acepta dentro de las entradas).
        while (i < n && text[i] !== '\n') i++
      } else break
    }
  }
  // Grupo {…} balanceado a partir de text[i] === '{'; devuelve el final (tras «}») o -1.
  const balanced = (start) => {
    let depth = 0
    for (let k = start; k < n; k++) {
      const c = text[k]
      if (c === '\\') {
        k++
        continue
      }
      if (c === '{') depth++
      else if (c === '}') {
        depth--
        if (depth === 0) return k + 1
      }
    }
    return -1
  }
  // Cadena "…" (las llaves internas deben estar balanceadas).
  const quoted = (start) => {
    let depth = 0
    for (let k = start + 1; k < n; k++) {
      const c = text[k]
      if (c === '\\') {
        k++
        continue
      }
      if (c === '{') depth++
      else if (c === '}') depth--
      else if (c === '"' && depth === 0) return k + 1
    }
    return -1
  }
  // Salta hasta la siguiente «@» que empiece una línea (recuperación de errores).
  const recover = (from) => {
    let k = from
    while (k < n) {
      const at = text.indexOf('@', k)
      if (at < 0) return n
      let b = at - 1
      while (b >= 0 && (text[b] === ' ' || text[b] === '\t')) b--
      if (b < 0 || text[b] === '\n') return at
      k = at + 1
    }
    return n
  }

  while (i < n) {
    const at = text.indexOf('@', i)
    if (at < 0) break
    i = at + 1
    skipWs()
    const typeFrom = i
    while (i < n && /[A-Za-z0-9_-]/.test(text[i])) i++
    const typeTo = i
    const type = text.slice(typeFrom, typeTo)
    if (!type) continue // «@» suelto fuera de una entrada: texto libre
    skipWs()
    const open = text[i]
    if (open !== '{' && open !== '(') {
      fail(at, `Falta «{» después de @${type}`)
      i = recover(i)
      continue
    }
    const closer = open === '{' ? '}' : ')'
    const lower = type.toLowerCase()

    if (lower === 'comment') {
      const end = open === '{' ? balanced(i) : text.indexOf(')', i) + 1
      i = end > 0 ? end : recover(i + 1)
      continue
    }
    if (lower === 'preamble') {
      const end = balancedWith(text, i, open, closer)
      i = end > 0 ? end : recover(i + 1)
      continue
    }
    i++ // tras «{»

    if (lower === 'string') {
      skipWs()
      const nameFrom = i
      while (i < n && NAME.test(text[i])) i++
      const name = text.slice(nameFrom, i)
      skipWs()
      if (!name || text[i] !== '=') {
        fail(at, 'Definición @string mal formada')
        i = recover(i)
        continue
      }
      i++
      const v = readValue()
      if (!v) {
        i = recover(i)
        continue
      }
      strings.set(name.toLowerCase(), v.value)
      skipWs()
      if (text[i] === closer) i++
      continue
    }

    // Entrada normal: clave, campos.
    skipWs()
    const keyFrom = i
    while (i < n && text[i] !== ',' && text[i] !== closer && !/\s/.test(text[i])) i++
    const keyTo = i
    const key = text.slice(keyFrom, keyTo)
    skipWs()
    const entry = { type, typeFrom, typeTo, key, keyFrom, keyTo, from: at, to: -1, close: -1, fields: [], index: entries.length }
    let ok = true
    while (i < n) {
      skipWs()
      if (text[i] === ',') {
        i++
        continue
      }
      if (text[i] === closer) {
        entry.close = i
        i++
        entry.to = i
        break
      }
      if (text[i] === '@') {
        ok = false
        fail(at, `La entrada «${key || type}» no está cerrada`)
        break
      }
      const nameFrom = i
      while (i < n && NAME.test(text[i])) i++
      const nameTo = i
      const name = text.slice(nameFrom, nameTo)
      skipWs()
      if (!name || text[i] !== '=') {
        ok = false
        fail(nameFrom, name ? `Falta «=» después del campo «${name}» en «${key}»` : `Carácter inesperado en «${key}»`)
        break
      }
      i++
      skipWs()
      const vFrom = i
      const v = readValue()
      if (!v) {
        ok = false
        break
      }
      entry.fields.push({ name, nameFrom, nameTo, vFrom, vTo: v.end, raw: text.slice(vFrom, v.end), value: v.value, complex: v.complex })
    }
    if (ok && entry.to < 0) {
      ok = false
      fail(at, `La entrada «${key || type}» no está cerrada`)
    }
    if (!ok) {
      i = recover(i)
      continue
    }
    entries.push(entry)
  }

  // Sustituye las macros (@string y meses) en los valores compuestos.
  for (const e of entries) {
    for (const f of e.fields) if (f.complex) f.display = expandRaw(f.raw, strings)
  }
  return { entries, strings, errors }

  // Valor: piezas {…}, "…", número o macro, unidas con «#».
  function readValue() {
    const pieces = []
    for (;;) {
      skipWs()
      const c = text[i]
      if (c === '{') {
        const end = balanced(i)
        if (end < 0) {
          fail(i, 'Llave sin cerrar')
          return null
        }
        pieces.push({ kind: 'text', value: text.slice(i + 1, end - 1) })
        i = end
      } else if (c === '"') {
        const end = quoted(i)
        if (end < 0) {
          fail(i, 'Comillas sin cerrar')
          return null
        }
        pieces.push({ kind: 'text', value: text.slice(i + 1, end - 1) })
        i = end
      } else {
        const s = i
        while (i < n && NAME.test(text[i])) i++
        if (s === i) {
          fail(i, 'Falta el valor del campo')
          return null
        }
        const word = text.slice(s, i)
        pieces.push(/^\d+$/.test(word) ? { kind: 'text', value: word } : { kind: 'macro', value: word })
      }
      const end = i
      skipWs()
      if (text[i] === '#') {
        i++
        continue
      }
      i = end
      const complex = pieces.length !== 1 || pieces[0].kind === 'macro'
      return { end, complex, value: complex ? rawOf(pieces) : pieces[0].value }
    }
  }
}

function balancedWith(text, start, open, closer) {
  let depth = 0
  for (let k = start; k < text.length; k++) {
    if (text[k] === open) depth++
    else if (text[k] === closer) {
      depth--
      if (depth === 0) return k + 1
    }
  }
  return -1
}

const rawOf = (pieces) => pieces.map((p) => (p.kind === 'macro' ? p.value : `{${p.value}}`)).join(' # ')

const MONTHS = { jan: 'enero', feb: 'febrero', mar: 'marzo', apr: 'abril', may: 'mayo', jun: 'junio', jul: 'julio', aug: 'agosto', sep: 'septiembre', oct: 'octubre', nov: 'noviembre', dec: 'diciembre' }

// Texto de un valor compuesto con las macros sustituidas.
function expandRaw(raw, strings) {
  const out = []
  const re = /\{((?:[^{}]|\{(?:[^{}]|\{[^{}]*\})*\})*)\}|"((?:[^"\\]|\\.)*)"|([^\s#{}"]+)/g
  for (const m of raw.matchAll(re)) {
    if (m[1] != null) out.push(m[1])
    else if (m[2] != null) out.push(m[2])
    else if (m[3]) {
      const w = m[3].toLowerCase()
      out.push(strings.get(w) ?? MONTHS[w] ?? m[3])
    }
  }
  return out.join('')
}

// ---------- texto legible ----------

const ACCENTS = { "'": '́', '`': '̀', '^': '̂', '"': '̈', '~': '̃', '=': '̄', '.': '̇', u: '̆', v: '̌', H: '̋', c: '̧', k: '̨', r: '̊' }
const SYMBOLS = { ss: 'ß', o: 'ø', O: 'Ø', ae: 'æ', AE: 'Æ', oe: 'œ', OE: 'Œ', aa: 'å', AA: 'Å', l: 'ł', L: 'Ł', i: 'ı', j: 'ȷ', '&': '&', '%': '%', $: '$', '#': '#', _: '_', textendash: '–', textemdash: '—' }

// LaTeX habitual en .bib → texto Unicode (solo para mostrar y buscar).
export function latexToText(s) {
  if (!s) return ''
  return s
    .replace(/\\([`'^"~=.])\s*\{?\s*\\?([A-Za-z])\s*\}?/g, (_, a, ch) => (ch + ACCENTS[a]).normalize('NFC'))
    .replace(/\\([uvHckr])\s*\{\s*\\?([A-Za-z])\s*\}/g, (_, a, ch) => (ch + ACCENTS[a]).normalize('NFC'))
    .replace(/\\([uvHckr]) ([A-Za-z])/g, (_, a, ch) => (ch + ACCENTS[a]).normalize('NFC'))
    .replace(/\\(ss|ae|AE|oe|OE|aa|AA|o|O|l|L|i|j|textendash|textemdash)(?![A-Za-z])\s*/g, (_, w) => SYMBOLS[w])
    .replace(/\\([&%$#_])/g, (_, c) => c)
    .replace(/\\(?:emph|textit|textbf|textsc|texttt|mkbibquote|enquote)\s*\{/g, '{')
    .replace(/---/g, '—')
    .replace(/--/g, '–')
    .replace(/~/g, ' ')
    .replace(/[{}]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

// Sin acentos ni mayúsculas, para buscar.
export const fold = (s) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase()

// ---------- nombres ----------

// Divide en el nivel 0 de llaves por un separador (p. ej. « and »).
function splitTop(s, sepRe) {
  const out = []
  let depth = 0
  let last = 0
  for (let k = 0; k < s.length; k++) {
    const c = s[k]
    if (c === '{') depth++
    else if (c === '}') depth--
    else if (depth === 0) {
      sepRe.lastIndex = k
      const m = sepRe.exec(s)
      if (m && m.index === k) {
        out.push(s.slice(last, k))
        last = k + m[0].length
        k = last - 1
      }
    }
  }
  out.push(s.slice(last))
  return out
}

/** «Apellido, Nombre and Nombre Apellido» → [{ last, first }]. */
export function parseNames(value) {
  if (!value || !value.trim()) return []
  return splitTop(value.replace(/\s+/g, ' ').trim(), /\s+and\s+/y)
    .map((name) => name.trim())
    .filter(Boolean)
    .map((name) => {
      const parts = splitTop(name, /\s*,\s*/y)
      if (parts.length >= 3) return { last: parts[0], first: parts[2], suffix: parts[1] }
      if (parts.length === 2) return { last: parts[0], first: parts[1] }
      // «Nombre von Apellido»: el apellido es la última palabra (o desde la partícula en minúscula).
      const words = splitTop(name, /\s+/y)
      if (words.length === 1) return { last: words[0], first: '' }
      let k = words.length - 1
      while (k > 1 && /^[a-z]/.test(words[k - 1])) k--
      return { last: words.slice(k).join(' '), first: words.slice(0, k).join(' ') }
    })
}

export function formatNames(list) {
  return list
    .filter((p) => (p.last ?? '').trim() || (p.first ?? '').trim())
    .map((p) => {
      const last = p.last.trim()
      const first = (p.first ?? '').trim()
      if (!first) return last.includes(' ') && !/^\{.*\}$/.test(last) ? `{${last}}` : last
      return p.suffix ? `${last}, ${p.suffix}, ${first}` : `${last}, ${first}`
    })
    .join(' and ')
}

// «García, Pérez y López» / «García et al.»
export function shortAuthors(value) {
  const names = parseNames(value).map((p) => latexToText(p.last))
  if (!names.length) return ''
  if (names.length > 3) return `${names[0]} et al.`
  if (names.length === 1) return names[0]
  return names.slice(0, -1).join(', ') + ' y ' + names[names.length - 1]
}

// ---------- consulta ----------

export const fieldOf = (entry, name) => entry.fields.find((f) => f.name.toLowerCase() === name)

/** Valor de un campo (con las macros sustituidas). */
export function fieldValue(entry, name) {
  const f = fieldOf(entry, name)
  if (!f) return ''
  return f.complex ? (f.display ?? f.value) : f.value
}

export function entryYear(entry) {
  const y = fieldValue(entry, 'year') || fieldValue(entry, 'date')
  return /\d{4}/.exec(y)?.[0] ?? ''
}

// Fecha en que se añadió la entrada, si la registró un gestor (este editor y
// JabRef usan creationdate; BibDesk date-added; otros timestamp).
export function entryAdded(entry) {
  for (const n of ['creationdate', 'date-added', 'timestamp', 'added-at', 'dateadded']) {
    const v = fieldValue(entry, n)
    if (v) {
      const t = Date.parse(v.replace(/ ([+-]\d{4})$/, '$1').replace(' ', 'T'))
      if (!Number.isNaN(t)) return t
    }
  }
  return 0
}

// ---------- edición ----------

// ¿Llaves balanceadas? (un valor desbalanceado rompería el archivo)
export function balancedValue(v) {
  let depth = 0
  for (let k = 0; k < v.length; k++) {
    if (v[k] === '\\') {
      k++
      continue
    }
    if (v[k] === '{') depth++
    else if (v[k] === '}' && --depth < 0) return false
  }
  return depth === 0
}

// Sangría de los campos de la entrada (la del primero, o dos espacios).
function indentOf(text, entry) {
  const f = entry.fields[0]
  if (!f) return '  '
  const lineStart = text.lastIndexOf('\n', f.nameFrom - 1) + 1
  const ws = text.slice(lineStart, f.nameFrom)
  return /^[ \t]*$/.test(ws) ? ws : '  '
}

export const META = new Set(['creationdate', 'modificationdate', 'date-added', 'date-modified', 'timestamp', 'added-at', 'dateadded'])

/** Cambios para dar `value` al campo `name` (lo crea si no existe; vacío lo quita). */
export function setField(text, entry, name, value) {
  const f = fieldOf(entry, name.toLowerCase())
  if (!value.trim()) return f ? removeField(text, entry, f) : []
  const v = `{${value}}`
  if (f) return f.raw === v ? [] : [{ from: f.vFrom, to: f.vTo, insert: v }]
  const ind = indentOf(text, entry)
  // Los campos nuevos van antes de los metadatos del final (creationdate…).
  let k = entry.fields.length
  while (k > 0 && META.has(entry.fields[k - 1].name.toLowerCase())) k--
  if (k < entry.fields.length) {
    const next = entry.fields[k]
    return [{ from: next.nameFrom, to: next.nameFrom, insert: `${name} = ${v},\n${ind}` }]
  }
  const last = entry.fields[entry.fields.length - 1]
  if (last) return [{ from: last.vTo, to: last.vTo, insert: `,\n${ind}${name} = ${v}` }]
  // Sin campos: tras la clave (y su coma, si la hay).
  const tail = text.slice(entry.keyTo, entry.close)
  const comma = tail.indexOf(',')
  const pos = comma >= 0 ? entry.keyTo + comma + 1 : entry.keyTo
  return [{ from: pos, to: entry.close, insert: `${comma >= 0 ? '' : ','}\n${ind}${name} = ${v}\n` }]
}

export function removeField(text, entry, field) {
  const k = entry.fields.indexOf(field)
  if (k < 0) return []
  if (k > 0) return [{ from: entry.fields[k - 1].vTo, to: field.vTo, insert: '' }]
  const next = entry.fields[1]
  if (next) return [{ from: field.nameFrom, to: next.nameFrom, insert: '' }]
  return [{ from: entry.keyTo, to: field.vTo, insert: '' }]
}

export const setType = (entry, type) => (entry.type === type ? [] : [{ from: entry.typeFrom, to: entry.typeTo, insert: type }])
export const setKey = (entry, key) => (entry.key === key ? [] : [{ from: entry.keyFrom, to: entry.keyTo, insert: key }])

// Borra la entrada y las líneas en blanco que la siguen.
export function deleteEntry(text, entry) {
  let to = entry.to
  while (to < text.length && /[ \t]/.test(text[to])) to++
  while (to < text.length && (text[to] === '\n' || text[to] === '\r')) to++
  let from = entry.from
  if (to >= text.length) while (from > 0 && /\s/.test(text[from - 1])) from--
  return [{ from, to, insert: '' }]
}

/** Texto de una entrada nueva: fields es [[nombre, valor], …] (los valores van entre llaves; raw:true, tal cual). */
export function formatEntry(type, key, fields) {
  const width = Math.max(0, ...fields.map(([n]) => n.length))
  const body = fields.map(([n, v, raw]) => `  ${n.padEnd(width)} = ${raw ? v : `{${v}}`}`).join(',\n')
  return `@${type}{${key}${body ? ',\n' + body + ',' : ','}\n}`
}

// Añadir al final del archivo, separada por una línea en blanco.
export function appendEntry(text, entryText) {
  const trimmed = text.replace(/\s+$/, '')
  const from = trimmed.length
  return { from, to: text.length, insert: (trimmed ? '\n\n' : '') + entryText + '\n', start: from + (trimmed ? 2 : 0) }
}

// Fecha y hora local en el formato de JabRef (2026-11-22T10:15:00).
export function nowStamp(d = new Date()) {
  const p = (x) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

// Clave propuesta: apellido del primer autor + año + primera palabra significativa del título.
const STOP = new Set(['a', 'an', 'the', 'of', 'on', 'in', 'and', 'for', 'to', 'el', 'la', 'los', 'las', 'un', 'una', 'de', 'del', 'en', 'y', 'por', 'para', 'con', 'sobre', 'le', 'les', 'des', 'du', 'der', 'die', 'das'])
const slug = (s) => fold(latexToText(s)).replace(/[^a-z0-9]+/g, '')
export function suggestKey(fields, taken) {
  const who = parseNames(fields.author || fields.editor || '')[0]
  const last = who ? slug(who.last.split(/\s+/).pop()) : ''
  const year = /\d{4}/.exec(fields.year || fields.date || '')?.[0] ?? ''
  const word = fold(latexToText(fields.title || '')).split(/[^a-z0-9]+/).find((w) => w.length > 2 && !STOP.has(w)) ?? ''
  const base = (last || slug(fields.organization || fields.institution || '') || 'ref') + year + word
  let key = base
  for (let k = 0; taken.has(key); k++) key = base + String.fromCharCode(97 + (k % 26)) + (k >= 26 ? Math.floor(k / 26) : '')
  return key
}

// ---------- tipos y campos ----------

export const FIELD_LABELS = {
  author: 'Autores', editor: 'Editores', title: 'Título', subtitle: 'Subtítulo', journal: 'Revista', journaltitle: 'Revista',
  booktitle: 'Título del libro / actas', year: 'Año', date: 'Fecha', month: 'Mes', volume: 'Volumen', number: 'Número',
  pages: 'Páginas', publisher: 'Editorial', address: 'Lugar', location: 'Lugar', edition: 'Edición', series: 'Serie',
  chapter: 'Capítulo', school: 'Universidad', institution: 'Institución', organization: 'Organización',
  howpublished: 'Cómo se publicó', type: 'Tipo', doi: 'DOI', url: 'URL', urldate: 'Fecha de consulta', isbn: 'ISBN',
  issn: 'ISSN', note: 'Nota', abstract: 'Resumen', keywords: 'Palabras clave', language: 'Idioma',
  creationdate: 'Añadida', eprint: 'Eprint', file: 'Archivo',
}

export const COMMON = ['doi', 'url', 'urldate', 'note', 'keywords', 'abstract']

export const ENTRY_TYPES = {
  article: { label: 'Artículo de revista', required: ['author', 'title', 'journal', 'year'], optional: ['volume', 'number', 'pages', 'month', 'issn'] },
  book: { label: 'Libro', required: ['author', 'title', 'publisher', 'year'], optional: ['editor', 'edition', 'volume', 'series', 'address', 'isbn'] },
  inbook: { label: 'Parte de un libro', required: ['author', 'title', 'chapter', 'publisher', 'year'], optional: ['editor', 'pages', 'address', 'isbn'] },
  incollection: { label: 'Capítulo en obra colectiva', required: ['author', 'title', 'booktitle', 'publisher', 'year'], optional: ['editor', 'pages', 'chapter', 'address', 'isbn'] },
  inproceedings: { label: 'Artículo en congreso', required: ['author', 'title', 'booktitle', 'year'], optional: ['editor', 'pages', 'publisher', 'address', 'organization'] },
  proceedings: { label: 'Actas de congreso', required: ['title', 'year'], optional: ['editor', 'publisher', 'address', 'organization', 'isbn'] },
  phdthesis: { label: 'Tesis doctoral', required: ['author', 'title', 'school', 'year'], optional: ['address', 'month', 'type'] },
  mastersthesis: { label: 'Tesis de maestría', required: ['author', 'title', 'school', 'year'], optional: ['address', 'month', 'type'] },
  techreport: { label: 'Informe técnico', required: ['author', 'title', 'institution', 'year'], optional: ['number', 'type', 'address', 'month'] },
  manual: { label: 'Manual', required: ['title'], optional: ['author', 'organization', 'edition', 'address', 'year'] },
  online: { label: 'Página web', required: ['title', 'url'], optional: ['author', 'organization', 'year', 'urldate'] },
  misc: { label: 'Otro', required: ['title'], optional: ['author', 'howpublished', 'year', 'month', 'organization'] },
  unpublished: { label: 'No publicado', required: ['author', 'title', 'note'], optional: ['year', 'month'] },
}

export function typeInfo(type) {
  const t = ENTRY_TYPES[type.toLowerCase()]
  const info = t ?? { label: type, required: ['title'], optional: ['author', 'year'] }
  const optional = [...info.optional, ...COMMON.filter((c) => !info.required.includes(c) && !info.optional.includes(c))]
  return { ...info, optional, known: !!t }
}

export const LONG_FIELDS = new Set(['abstract', 'note', 'title', 'booktitle'])
export const NAME_FIELDS = new Set(['author', 'editor'])

// Campos que cubren a uno obligatorio (BibLaTeX usa date, journaltitle, location…).
const ALTERNATES = { year: ['date'], journal: ['journaltitle'], author: ['editor'], address: ['location'], school: ['institution'] }

/** Campos obligatorios del tipo que faltan; has(nombre) dice si el campo tiene valor. */
export function missingRequired(type, has) {
  return typeInfo(type).required.filter((n) => !has(n) && !(ALTERNATES[n] ?? []).some(has))
}

/** BibTeX de un DOI (doi.org con negociación de contenido; si falla, Crossref). */
export async function fetchDoi(doi) {
  const id = doi.trim().replace(/^(https?:\/\/(dx\.)?doi\.org\/|doi:\s*)/i, '')
  if (!/^10\.\d{4,}(\.\d+)*\/\S+$/.test(id)) throw new Error('DOI no válido: debe tener la forma 10.xxxx/…')
  const tries = [
    () => fetch(`https://doi.org/${encodeURI(id)}`, { headers: { Accept: 'application/x-bibtex; charset=utf-8' } }),
    () => fetch(`https://api.crossref.org/works/${encodeURIComponent(id)}/transform/application/x-bibtex`),
  ]
  let notFound = false
  for (const t of tries) {
    try {
      const res = await t()
      if (res.ok) {
        const text = await res.text()
        if (/@\s*\w+\s*[{(]/.test(text)) return text
      } else if (res.status === 404) notFound = true
    } catch {
      /* siguiente servicio */
    }
  }
  throw new Error(notFound ? 'No se encontró ese DOI' : 'No se pudo consultar el DOI (sin conexión o servicio no disponible)')
}

/**
 * Prepara entradas importadas (pegadas o de un DOI) para añadirlas a un .bib:
 * formato uniforme, claves únicas frente a `taken` (y entre sí) y creationdate.
 * keyFromFields: proponer la clave a partir de los datos (p. ej. las de Crossref).
 */
export function prepareImport(source, taken, { keyFromFields = false, stamp = nowStamp() } = {}) {
  const p = parseBib(source)
  const used = new Set(taken)
  const keys = []
  const texts = []
  for (const e of p.entries) {
    const vals = Object.fromEntries(e.fields.map((f) => [f.name.toLowerCase(), f.complex ? (f.display ?? f.value) : f.value]))
    let key = e.key
    if (keyFromFields || !key || used.has(key)) {
      key = keyFromFields || !key ? suggestKey(vals, used) : key
      for (let k = 2; used.has(key); k++) key = `${e.key}-${k}`
    }
    used.add(key)
    keys.push(key)
    const fields = e.fields.map((f) => [f.name.toLowerCase(), f.raw, true])
    if (!e.fields.some((f) => META.has(f.name.toLowerCase()))) fields.push(['creationdate', stamp])
    texts.push(formatEntry(e.type.toLowerCase(), key, fields))
  }
  const strings = [...p.strings].map(([k, v]) => `@string{${k} = {${v}}}`)
  return { keys, text: [...strings, ...texts].join('\n\n'), errors: p.errors, count: texts.length }
}
