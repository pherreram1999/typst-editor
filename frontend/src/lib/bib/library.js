// Bibliografía del proyecto: entradas de todos los .bib (para citar y
// autocompletar) y orden por «añadidas recientemente».
//
// La fecha en que se añadió una entrada sale de su campo creationdate (lo
// escribe el editor visual; JabRef usa el mismo) u otros equivalentes. Para
// las que no lo tienen (añadidas a mano en el código, subidas…) se recuerda
// en este navegador cuándo se vio cada clave por primera vez; las que ya
// estaban la primera vez que se abrió el proyecto cuentan como antiguas. A
// igualdad, la que está más abajo en el archivo es la más reciente.
import { api } from '../api.js'
import { parseBib, fieldValue, entryYear, entryAdded, latexToText, shortAuthors, fold } from './bibtex.js'

// Texto de los archivos (no abiertos) por proyecto; se invalida con los
// mensajes «rev» del servidor (invalidateText).
const texts = new Map() // `${projectId}:${path}` -> Promise<string>

export function invalidateText(projectId, path) {
  if (path) texts.delete(`${projectId}:${path}`)
  else for (const k of [...texts.keys()]) if (k.startsWith(`${projectId}:`)) texts.delete(k)
}

/** Texto de un archivo del proyecto; `live` es la sesión abierta (su texto manda). */
export function fileText(projectId, path, live) {
  if (live?.loaded && live.path === path) return Promise.resolve(live.text)
  const k = `${projectId}:${path}`
  let p = texts.get(k)
  if (!p) {
    p = api.textFile(projectId, path)
    p.catch(() => texts.delete(k))
    texts.set(k, p)
  }
  return p
}

// ---------- primera vez que se vio cada clave ----------

const seenKey = (projectId) => `typsteditor.bibseen.${projectId}`

/** Marca de tiempo de la primera vez que se vio cada clave (0: ya estaba al empezar). */
export function seenTimes(projectId, keys, prune = false) {
  let map = null
  try {
    map = JSON.parse(localStorage.getItem(seenKey(projectId)) ?? 'null')
  } catch {
    map = null
  }
  const first = !map
  map ??= {}
  const now = Date.now()
  let changed = first
  for (const k of keys) {
    if (!(k in map)) {
      map[k] = first ? 0 : now
      changed = true
    }
  }
  if (prune) {
    const live = new Set(keys)
    for (const k of Object.keys(map)) if (!live.has(k)) (delete map[k], (changed = true))
  }
  if (changed) {
    try {
      localStorage.setItem(seenKey(projectId), JSON.stringify(map))
    } catch {
      /* sin almacenamiento: solo se ordena por creationdate y posición */
    }
  }
  return map
}

// ---------- entradas de todo el proyecto ----------

/**
 * @typedef {{ key: string, type: string, file: string, index: number, title: string, authors: string,
 *   year: string, venue: string, added: number, search: string, entry: object }} LibItem
 */

/** Entradas de una sola fuente ya analizada, con los datos para mostrar. */
export function itemsOf(file, entries) {
  return entries.map((e) => {
    const title = latexToText(fieldValue(e, 'title'))
    const authors = shortAuthors(fieldValue(e, 'author') || fieldValue(e, 'editor')) || latexToText(fieldValue(e, 'organization') || fieldValue(e, 'institution'))
    const venue = latexToText(fieldValue(e, 'journal') || fieldValue(e, 'journaltitle') || fieldValue(e, 'booktitle') || fieldValue(e, 'publisher') || fieldValue(e, 'school') || fieldValue(e, 'howpublished'))
    const year = entryYear(e)
    return {
      key: e.key,
      type: e.type.toLowerCase(),
      file,
      index: e.index,
      title,
      authors,
      year,
      venue,
      added: entryAdded(e),
      search: fold(`${e.key} ${title} ${authors} ${latexToText(fieldValue(e, 'author'))} ${year} ${venue} ${fieldValue(e, 'keywords')} ${fieldValue(e, 'doi')}`),
      entry: e,
    }
  })
}

/** Ordena de la más reciente a la más antigua (ver el comentario del módulo). */
export function byRecent(projectId, items, prune = false) {
  const seen = seenTimes(projectId, items.map((x) => x.key), prune)
  const when = (x) => x.added || seen[x.key] || 0
  return [...items].sort((a, b) => when(b) - when(a) || (a.file === b.file ? b.index - a.index : a.file < b.file ? -1 : 1))
}

const SORTS = {
  author: (a, b) => (a.authors || '\uffff').localeCompare(b.authors || '\uffff', 'es') || b.year.localeCompare(a.year),
  year: (a, b) => (b.year || '0').localeCompare(a.year || '0') || a.authors.localeCompare(b.authors, 'es'),
  title: (a, b) => a.title.localeCompare(b.title, 'es'),
  key: (a, b) => a.key.localeCompare(b.key, 'es'),
  file: (a, b) => (a.file === b.file ? a.index - b.index : a.file < b.file ? -1 : 1),
}

export function sortItems(projectId, items, sort) {
  if (sort === 'recent') return byRecent(projectId, items)
  return [...items].sort(SORTS[sort] ?? SORTS.file)
}

/** Filtra por texto (todas las palabras, sin acentos ni mayúsculas). */
export function filterItems(items, query) {
  const words = fold(query).split(/\s+/).filter(Boolean)
  if (!words.length) return items
  return items.filter((x) => words.every((w) => x.search.includes(w)))
}

/**
 * Entradas de todos los .bib del proyecto (más recientes primero) y los
 * errores de lectura por archivo.
 */
export async function loadLibrary(projectId, files, live) {
  const bibs = files.filter((f) => /\.bib$/i.test(f.path)).map((f) => f.path)
  const results = await Promise.all(
    bibs.map((path) =>
      fileText(projectId, path, live).then(
        (text) => ({ path, parsed: parseBib(text) }),
        (e) => ({ path, error: e.message }),
      ),
    ),
  )
  const items = []
  const problems = []
  for (const r of results) {
    if (r.error) problems.push({ file: r.path, message: r.error })
    else {
      items.push(...itemsOf(r.path, r.parsed.entries))
      if (r.parsed.errors.length) problems.push({ file: r.path, message: `${r.parsed.errors.length} error(es) de sintaxis (línea ${r.parsed.errors[0].line})` })
    }
  }
  return { files: bibs, items: byRecent(projectId, items, problems.length === 0), problems }
}

/**
 * .bib que enlaza algún #bibliography(…) del proyecto (rutas del proyecto).
 * found: si hay alguna llamada a bibliography.
 */
export async function linkedBibs(projectId, files, live) {
  const typs = files.filter((f) => f.path.endsWith('.typ')).map((f) => f.path)
  const linked = new Set()
  let found = false
  await Promise.all(
    typs.map(async (path) => {
      const text = await fileText(projectId, path, live).catch(() => '')
      for (const m of text.matchAll(/\bbibliography\(\s*(\((?:[^()"]|"(?:[^"\\]|\\.)*")*\)|"(?:[^"\\]|\\.)*")/g)) {
        found = true
        for (const s of m[1].matchAll(/"((?:[^"\\]|\\.)*)"/g)) linked.add(resolveFrom(path, s[1].replace(/\\(.)/g, '$1')))
      }
    }),
  )
  return { found, linked }
}

function resolveFrom(from, rel) {
  const parts = rel.startsWith('/') ? [] : from.split('/').slice(0, -1)
  for (const seg of rel.replace(/^\/+/, '').split('/')) {
    if (seg === '..') parts.pop()
    else if (seg && seg !== '.') parts.push(seg)
  }
  return parts.join('/')
}

// ---------- código de la cita ----------

// Claves que admiten la sintaxis corta: @clave y <clave>.
const LABEL_OK = /^[\p{L}\p{N}_][\p{L}\p{N}_:.-]*$/u
const escapeString = (s) => s.replace(/["\\]/g, (c) => '\\' + c)
const escapeContent = (t) => t.replace(/[\\[\]#$]/g, (c) => '\\' + c)
const labelOf = (k) => (LABEL_OK.test(k) ? `<${k}>` : `label("${escapeString(k)}")`)

export const CITE_FORMS = [
  ['normal', 'Normal', '[1] · (García, 2020)'],
  ['prose', 'En el texto', 'García (2020)'],
  ['author', 'Solo autor', 'García'],
  ['year', 'Solo año', '2020'],
  ['full', 'Referencia completa', 'García, J. (2020). Título…'],
]

/** Código Typst que cita `keys` (supplement: p. ej. «p. 12», solo con una clave). */
export function citeCode(keys, form = 'normal', supplement = '') {
  const supp = supplement.trim()
  return keys
    .map((k) => {
      if (form === 'normal' && LABEL_OK.test(k) && !/[.:]$/.test(k)) return `@${k}${supp ? `[${escapeContent(supp)}]` : ''}`
      const args = [labelOf(k)]
      if (form !== 'normal') args.push(`form: "${form}"`)
      if (supp) args.push(`supplement: [${escapeContent(supp)}]`)
      return `#cite(${args.join(', ')})`
    })
    .join(' ')
}
