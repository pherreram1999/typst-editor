// Resaltado de BibTeX/BibLaTeX para CodeMirror 6 (StreamLanguage); usa las
// mismas etiquetas que typst-lang.js, así que comparte sus colores.
import { StreamLanguage } from '@codemirror/language'

// Estado: fuera de entradas (texto libre = comentario), cabecera (@tipo{clave),
// dentro (campos), y profundidad de llaves del valor en curso.
export const bibLanguage = StreamLanguage.define({
  name: 'bibtex',
  startState: () => ({ mode: 'out', depth: 0, quote: false, afterEq: false }),
  copyState: (s) => ({ ...s }),
  token(stream, s) {
    // Dentro de un valor {…} o "…" que sigue en otra línea.
    if (s.depth > 0 || s.quote) {
      while (!stream.eol()) {
        const c = stream.next()
        if (c === '\\') stream.next()
        else if (c === '{') s.depth++
        else if (c === '}') {
          if (--s.depth === 0 && !s.quote) break
        } else if (c === '"' && s.quote && s.depth === 0) {
          s.quote = false
          break
        }
      }
      return 'string'
    }
    if (stream.eatSpace()) return null
    if (stream.peek() === '%') {
      stream.skipToEnd()
      return 'comment'
    }
    if (s.mode === 'out') {
      if (stream.match(/^@\s*[A-Za-z]+/)) {
        s.mode = 'head'
        return 'keyword'
      }
      stream.next()
      while (!stream.eol() && stream.peek() !== '@' && stream.peek() !== '%') stream.next()
      return 'comment'
    }
    if (s.mode === 'head') {
      if (stream.eat('{') || stream.eat('(')) return 'bracket'
      if (stream.match(/^[^,\s})]+/)) return 'labelName'
      if (stream.eat(',')) {
        s.mode = 'in'
        return null
      }
      if (stream.eat('}') || stream.eat(')')) {
        s.mode = 'out'
        return 'bracket'
      }
      stream.next()
      return null
    }
    // Campos.
    const c = stream.peek()
    if (c === '{') {
      stream.next()
      s.depth = 1
      s.afterEq = false
      while (!stream.eol() && s.depth > 0) {
        const ch = stream.next()
        if (ch === '\\') stream.next()
        else if (ch === '{') s.depth++
        else if (ch === '}') s.depth--
      }
      return 'string'
    }
    if (c === '"') {
      stream.next()
      s.quote = true
      s.afterEq = false
      while (!stream.eol()) {
        const ch = stream.next()
        if (ch === '\\') stream.next()
        else if (ch === '{') s.depth++
        else if (ch === '}') s.depth--
        else if (ch === '"' && s.depth === 0) {
          s.quote = false
          break
        }
      }
      return 'string'
    }
    if (c === '}' || c === ')') {
      stream.next()
      s.mode = 'out'
      return 'bracket'
    }
    if (c === '=' || c === '#') {
      stream.next()
      s.afterEq = true
      return 'operator'
    }
    if (c === ',') {
      stream.next()
      s.afterEq = false
      return null
    }
    if (stream.match(/^\d+/)) return 'number'
    if (stream.match(/^[^\s"#%'(),={}]+/)) {
      if (s.afterEq) return 'atom' // macro (@string, meses)
      return 'variableName.function'
    }
    stream.next()
    return null
  },
  languageData: { commentTokens: { line: '%' } },
})
