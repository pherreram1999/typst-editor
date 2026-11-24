// Palabras de prosa de un documento Typst para la revisión ortográfica
// (editor/spellcheck.js). Sin dependencias del navegador: se puede probar en Node.
//
// El resaltador de Typst (editor/typst-lang.js) no genera nodo para el texto
// normal del marcado y sí para código, matemáticas, cadenas, raw, etiquetas,
// referencias, URL y comentarios: se revisa lo que no está dentro de un nodo,
// salvo encabezados, negritas, cursivas y marcas de lista, que sí son prosa.
import { syntaxTree } from '@codemirror/language'

const PROSE = new Set(['heading', 'strong', 'emphasis', 'strong_emphasis', 'list'])
const WORD = /[\p{L}\p{M}]+/gu

// Palabras que no se revisan: una letra, junto a cifras («3er», unidades),
// extensiones y dominios («foto.png», «ejemplo.com»), siglas
// (MAYÚSCULAS de hasta 5 letras) y mezclas como «LaTeX» o «iPhone».
export function worthChecking(word, text, s, e) {
  if (word.length < 2) return false
  const before = text[s - 1] ?? ''
  const after = text[e] ?? ''
  if (/\d/.test(before) || /\d/.test(after)) return false
  if (before === '.' && /[\p{L}\d]/u.test(text[s - 2] ?? '')) return false
  if (after === '.' && /\p{L}/u.test(text[e + 1] ?? '')) return false
  if (before === '@' || before === '#' || before === '\\') return false
  const upper = word === word.toUpperCase()
  if (upper && word.length <= 5) return false
  if (!upper && /\p{Lu}/u.test(word.slice(1))) return false
  return true
}

// Palabras de prosa en los rangos: [{ from, to, word }], en orden. tree: el
// árbol de sintaxis (por omisión, el del estado, que en el editor solo cubre
// lo ya analizado).
export function collectWords(state, ranges, tree = syntaxTree(state)) {
  const out = []
  for (let { from, to } of ranges) {
    to = Math.min(to, tree.length) // más allá aún no se ha analizado: no se sabe qué es prosa
    if (to <= from) continue
    const blocked = []
    tree.iterate({
      from,
      to,
      enter(n) {
        if (!n.type.isTop && !PROSE.has(n.name)) blocked.push(n.from, n.to)
      },
    })
    const text = state.sliceDoc(from, to)
    let b = 0
    for (const m of text.matchAll(WORD)) {
      const s = m.index
      const e = s + m[0].length
      const absS = from + s
      const absE = from + e
      while (b < blocked.length && blocked[b + 1] <= absS) b += 2
      if (b < blocked.length && blocked[b] < absE) continue
      if (!worthChecking(m[0], text, s, e)) continue
      out.push({ from: absS, to: absE, word: m[0].normalize('NFC') })
    }
  }
  return out
}
