// Corrector ortográfico (español de México) en un worker: carga la lista de
// palabras (scripts/spell-dict.mjs, codificación frontal) en una sola cadena
// con un índice de inicios (~15 MB en vez de ~550 000 cadenas sueltas) y
// responde por mensajes:
//   { id, type: 'load', url }            → { id, count }
//   { id, type: 'user', words }          → diccionario personal (minúsculas)
//   { id, type: 'check', words }         → { id, bad: [palabras incorrectas] }
//   { id, type: 'suggest', word }        → { id, list: [sugerencias] }

let text = '' // palabras separadas por \n, ordenadas por unidad UTF-16
let starts = new Int32Array(0) // inicio de cada palabra (y uno más al final)
let count = 0
let user = new Set()

function decode(raw) {
  const words = []
  let prev = ''
  for (const line of raw.split('\n')) {
    if (!line) continue
    const w = prev.slice(0, line.charCodeAt(0) - 48) + line.slice(1)
    words.push(w)
    prev = w
  }
  text = words.join('\n') + '\n'
  count = words.length
  starts = new Int32Array(count + 1)
  let p = 0
  for (let i = 0; i < count; i++) {
    starts[i] = p
    p += words[i].length + 1
  }
  starts[count] = p
}

// Compara la palabra i del diccionario con w (orden de unidades UTF-16).
function cmp(i, w) {
  const a = starts[i]
  const len = starts[i + 1] - 1 - a
  const n = Math.min(len, w.length)
  for (let k = 0; k < n; k++) {
    const d = text.charCodeAt(a + k) - w.charCodeAt(k)
    if (d) return d
  }
  return len - w.length
}

function has(w) {
  let lo = 0
  let hi = count - 1
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    const c = cmp(mid, w)
    if (c === 0) return true
    if (c < 0) lo = mid + 1
    else hi = mid - 1
  }
  return false
}

const cap = (w) => w.charAt(0).toUpperCase() + w.slice(1)
const isUpper = (w) => w === w.toUpperCase() && w !== w.toLowerCase()
const isCapitalized = (w) => w.length > 1 && w[0] !== w[0].toLowerCase() && w.slice(1) === w.slice(1).toLowerCase()

// Correcta tal cual, en minúsculas si empieza la oración (Capitalizada) o en
// MAYÚSCULAS («MÉXICO» → «México»), o en el diccionario personal.
function correct(w) {
  if (has(w)) return true
  const lower = w.toLowerCase()
  if (user.has(lower)) return true
  if (isCapitalized(w) || isUpper(w)) return has(lower) || has(cap(lower))
  return false
}

// ---------- sugerencias ----------

const ALPHABET = 'aábcdeéfghiíjklmnñoópqrstuúüvwxyz'
const stripAccents = (w) => w.normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/ñ/g, 'n')
// Confusiones habituales en español: b/v, c/s/z, g/j, y/i, ll/y, x/s.
const CONFUSED = new Set(['bv', 'vb', 'cs', 'sc', 'sz', 'zs', 'cz', 'zc', 'gj', 'jg', 'yi', 'iy', 'xs', 'sx', 'kc', 'ck', 'qc', 'cq'])

// Llama a fn(candidato, coste) con cada palabra a una edición de w.
function edits1(w, fn) {
  const n = w.length
  for (let i = 0; i < n; i++) {
    const del = w.slice(0, i) + w.slice(i + 1)
    fn(del, w[i] === 'h' ? 0.5 : 1) // la h muda se olvida o se sobra a menudo
    if (i < n - 1 && w[i] !== w[i + 1]) fn(w.slice(0, i) + w[i + 1] + w[i] + w.slice(i + 2), 0.8)
    for (const c of ALPHABET) {
      if (c === w[i]) continue
      const sub = w.slice(0, i) + c + w.slice(i + 1)
      const accent = stripAccents(c) === stripAccents(w[i])
      fn(sub, accent ? 0.1 : CONFUSED.has(w[i] + c) ? 0.5 : 1)
    }
  }
  for (let i = 0; i <= n; i++) {
    // h muda y la u de «que/qui», «gue/gui» se olvidan a menudo.
    for (const c of ALPHABET) fn(w.slice(0, i) + c + w.slice(i), c === 'h' || (c === 'u' && /[qg]/.test(w[i - 1] ?? '')) ? 0.4 : 1)
  }
}

// Variantes de w cambiando solo acentos o la ñ en una posición (a↔á, n↔ñ, u↔ü…).
const ACCENT_SWAP = { a: 'á', á: 'a', e: 'é', é: 'e', i: 'í', í: 'i', o: 'ó', ó: 'o', u: 'úü', ú: 'uü', ü: 'uú', n: 'ñ', ñ: 'n' }
function accentEdits(w, fn) {
  for (let i = 0; i < w.length; i++) {
    for (const c of ACCENT_SWAP[w[i]] ?? '') fn(w.slice(0, i) + c + w.slice(i + 1))
  }
}
// Cambios «baratos»: acentos y letras que suenan igual (b/v, c/s/z, g/j, y/i/ll…).
const SOUND_SWAP = { b: 'v', v: 'b', c: 'sz', s: 'cz', z: 'sc', g: 'j', j: 'g', y: 'i', i: 'y', x: 's', k: 'c', q: 'c' }
function cheapEdits(w, fn) {
  accentEdits(w, fn)
  for (let i = 0; i < w.length; i++) {
    for (const c of SOUND_SWAP[w[i]] ?? '') fn(w.slice(0, i) + c + w.slice(i + 1))
  }
}

// Palabras muy frecuentes (generales y de textos académicos): el diccionario
// no trae frecuencias y, a igual distancia, deben ir primero («qe» → «que»).
const COMMON = new Set(`de la que el en y a los se del las un por con no una su para es al lo como más
pero sus le ya o este sí porque esta entre cuando muy sin sobre también me hasta hay donde quien desde
todo nos durante todos uno les ni contra otros ese eso ante ellos esto mí antes algunos qué unos yo otro
otras otra él tanto esa estos mucho quienes nada muchos cual poco ella estar estas algunas algo nosotros
cada vez parte puede pueden ser son fue era han ha había hacer hace tiene tienen tener según así aún
bien mismo misma mismos además después general forma caso casos tipo manera mayor menor primer primera
segundo segunda tres dos años año tiempo trabajo desarrollo proceso sistema sistemas resultados resultado
análisis estudio datos información investigación objetivo objetivos método métodos metodología problema
modelo modelos capítulo sección figura tabla ecuación conclusión conclusiones introducción referencias
bibliografía hipótesis teoría marco respecto través mediante cuál cómo dónde siguiente siguientes
anterior importante necesario necesaria posible diferentes diferente nuevo nueva gran grandes mejor
través medio través valor valores número números punto puntos función funciones relación base nivel
uso usar utilizar realizar realizó realizado obtener obtenido presenta presente estado estados país
México mexicano mexicana universidad tesis documento texto ejemplo dato grupo grupos social sociales`.split(/\s+/))

function suggest(word) {
  const lower = word.normalize('NFC').toLowerCase()
  const found = new Map() // candidato (tal como está en el diccionario) → coste
  const consider = (c, cost) => {
    let form = null
    if (c !== lower && (has(c) || user.has(c))) form = c
    else if (has(cap(c))) {
      form = cap(c) // nombre propio
      cost += c === lower ? 0 : 0.3 // «jalisco» → «Jalisco» es lo primero
    }
    if (form == null) return
    if (stripAccents(form.toLowerCase()) === stripAccents(lower)) cost = Math.min(cost, 0.05)
    else if (COMMON.has(form)) cost -= 0.55
    if (!found.has(form) || found.get(form) > cost) found.set(form, cost)
  }
  consider(lower, 0)
  // 1) Una edición; 2) una edición más hasta dos cambios «baratos» (acentos y
  // letras que suenan igual: «desicion» → «decisión» son tres cambios), que
  // solo tocan unas pocas letras; 3) dos ediciones cualesquiera solo si no
  // salió nada (coste ∝ n²·|alfabeto|²).
  const first = []
  edits1(lower, (e1, c1) => {
    consider(e1, c1)
    first.push([e1, c1])
  })
  for (const [e1, c1] of first) {
    cheapEdits(e1, (e2) => {
      consider(e2, c1 + 0.2)
      accentEdits(e2, (e3) => consider(e3, c1 + 0.3))
    })
  }
  if (!found.size && lower.length <= 12) {
    const seen = new Set()
    for (const [e1, c1] of first) {
      if (seen.has(e1)) continue
      seen.add(e1)
      edits1(e1, (e2, c2) => consider(e2, 1 + Math.min(c1, c2) + Math.max(c1, c2) * 0.5))
    }
  }
  const list = [...found.entries()]
    .sort((a, b) => a[1] - b[1] || Math.abs(a[0].length - lower.length) - Math.abs(b[0].length - lower.length) || a[0].localeCompare(b[0], 'es'))
    .slice(0, 6)
    .map(([w]) => w)
  // Respetar las mayúsculas de la palabra original.
  if (isUpper(word) && word.length > 1) return list.map((w) => w.toUpperCase())
  if (word[0] !== word[0].toLowerCase()) return list.map(cap)
  return list
}

self.onmessage = async (e) => {
  const { id, type } = e.data
  try {
    if (type === 'load') {
      const res = await fetch(e.data.url)
      if (!res.ok) throw new Error(`diccionario: HTTP ${res.status}`)
      decode(await res.text())
      self.postMessage({ id, count })
    } else if (type === 'user') {
      user = new Set(e.data.words)
      self.postMessage({ id })
    } else if (type === 'check') {
      self.postMessage({ id, bad: e.data.words.filter((w) => !correct(w.normalize('NFC'))) })
    } else if (type === 'suggest') {
      self.postMessage({ id, list: suggest(e.data.word) })
    }
  } catch (err) {
    self.postMessage({ id, error: String(err?.message ?? err) })
  }
}
