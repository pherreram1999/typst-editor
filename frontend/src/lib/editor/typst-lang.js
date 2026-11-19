// Resaltado de sintaxis de Typst para CodeMirror 6 mediante StreamLanguage.
// Distingue los tres modos de Typst: marcado, código (tras "#" o dentro de
// llaves/paréntesis de código) y matemáticas ($ … $).
import { StreamLanguage, HighlightStyle } from '@codemirror/language'
import { tags as t } from '@lezer/highlight'

const KEYWORDS = new Set([
  'let', 'set', 'show', 'if', 'else', 'for', 'in', 'while', 'break', 'continue', 'return',
  'import', 'include', 'as', 'context', 'and', 'or', 'not',
])
const ATOMS = new Set(['none', 'auto', 'true', 'false'])
const IDENT = /^[\p{L}_][\p{L}\p{N}_-]*/u

// Pila de modos: 'markup' | 'code' | 'math' | 'content' (corchetes en código)
function top(state) {
  return state.stack[state.stack.length - 1]
}

function tokenString(stream) {
  let escaped = false
  let ch
  while ((ch = stream.next()) != null) {
    if (ch === '"' && !escaped) break
    escaped = !escaped && ch === '\\'
  }
  return 'string'
}

function tokenCode(stream, state) {
  const mode = top(state)
  if (stream.eatSpace()) return null
  if (stream.match('//')) {
    stream.skipToEnd()
    return 'comment'
  }
  if (stream.match('/*')) {
    state.blockComment = true
    return 'comment'
  }
  const ch = stream.peek()
  if (ch === '"') {
    stream.next()
    return tokenString(stream)
  }
  if (stream.match(/^\d+(\.\d+)?(pt|mm|cm|in|em|fr|%|deg|rad)?/)) return 'number'
  if (ch === '[') {
    stream.next()
    state.stack.push('content')
    return 'bracket'
  }
  if (ch === '(' || ch === '{') {
    stream.next()
    state.stack.push(ch === '(' ? 'paren' : 'brace')
    return 'bracket'
  }
  if (ch === ')' || ch === '}') {
    stream.next()
    if (mode === 'paren' || mode === 'brace') state.stack.pop()
    if (state.inlineCode && state.stack.length === state.inlineDepth) state.inlineCode = false
    return 'bracket'
  }
  if (ch === '$') {
    stream.next()
    state.stack.push('math')
    return 'keyword'
  }
  const m = stream.match(IDENT)
  if (m) {
    const word = m[0]
    if (KEYWORDS.has(word)) return 'keyword'
    if (ATOMS.has(word)) return 'atom'
    if (stream.peek() === '(' || stream.peek() === '[') return 'variableName.function'
    return 'variableName'
  }
  stream.next()
  return /[=+\-*/<>!.:,]/.test(ch) ? 'operator' : null
}

function tokenMath(stream, state) {
  if (stream.eatSpace()) return null
  const ch = stream.next()
  if (ch === '$') {
    state.stack.pop()
    return 'keyword'
  }
  if (ch === '\\') {
    stream.next()
    return 'escape'
  }
  if (ch === '"') return tokenString(stream)
  if (/\d/.test(ch)) {
    stream.match(/^[\d.]*/)
    return 'number'
  }
  if (/[\p{L}]/u.test(ch)) {
    stream.match(/^[\p{L}\p{N}.]*/u)
    return 'variableName.special'
  }
  return /[_^&=<>+\-*/]/.test(ch) ? 'operator' : null
}

function tokenMarkup(stream, state) {
  const mode = top(state)
  if (stream.sol()) {
    if (stream.match(/^\s*=+\s/)) {
      stream.skipToEnd()
      return 'heading'
    }
    if (stream.match(/^\s*([-+]|\/|\d+\.)\s/)) return 'list'
  }
  if (stream.match('//')) {
    stream.skipToEnd()
    return 'comment'
  }
  if (stream.match('/*')) {
    state.blockComment = true
    return 'comment'
  }
  const ch = stream.peek()
  if (ch === '\\') {
    stream.next()
    stream.next()
    return 'escape'
  }
  if (ch === '#') {
    stream.next()
    // "#expr": el identificador y su llamada se tokenizan en modo código.
    if (stream.match(IDENT, false) || /[({[]/.test(stream.peek() ?? '')) {
      state.inlineCode = true
      state.inlineDepth = state.stack.length
      const m = stream.match(IDENT)
      if (m) {
        if (KEYWORDS.has(m[0])) return 'keyword'
        if (ATOMS.has(m[0])) return 'atom'
        return 'variableName.function'
      }
    }
    return 'keyword'
  }
  if (ch === '$') {
    stream.next()
    state.stack.push('math')
    return 'keyword'
  }
  if (ch === ']' && mode === 'content') {
    stream.next()
    state.stack.pop()
    return 'bracket'
  }
  if (ch === '`') {
    if (stream.match(/^```[\s\S]*?```/) || stream.match(/^`[^`]*`/)) return 'monospace'
    stream.skipToEnd()
    return 'monospace'
  }
  if (ch === '<' && stream.match(/^<[\p{L}\p{N}_:.-]+>/u)) return 'labelName'
  if (ch === '@' && stream.match(/^@[\p{L}\p{N}_:.-]+/u)) return 'link'
  if (stream.match(/^https?:\/\/[^\s)\]]+/)) return 'url'
  if (ch === '*') {
    stream.next()
    state.strong = !state.strong
    return 'strong'
  }
  if (ch === '_') {
    stream.next()
    state.emph = !state.emph
    return 'emphasis'
  }
  stream.next()
  // Consumir texto plano de golpe hasta el próximo carácter especial.
  stream.match(/^[^\\#$\]`<@*_/\s]*/)
  if (state.strong && state.emph) return 'strong emphasis'
  if (state.strong) return 'strong'
  if (state.emph) return 'emphasis'
  return null
}

export const typstLanguage = StreamLanguage.define({
  name: 'typst',
  startState: () => ({ stack: ['markup'], blockComment: false, strong: false, emph: false, inlineCode: false, inlineDepth: 0 }),
  copyState: (s) => ({ ...s, stack: s.stack.slice() }),
  token(stream, state) {
    if (state.blockComment) {
      if (stream.skipTo('*/')) {
        stream.match('*/')
        state.blockComment = false
      } else stream.skipToEnd()
      return 'comment'
    }
    const mode = top(state)
    if (mode === 'math') return tokenMath(stream, state)
    if (mode === 'paren' || mode === 'brace') return tokenCode(stream, state)
    if (state.inlineCode) {
      // Tras "#ident" sigue una llamada/acceso o se vuelve al marcado.
      const ch = stream.peek()
      if (ch === '(' || ch === '[' || ch === '{' || ch === '.') {
        if (ch === '.') {
          stream.next()
          const m = stream.match(IDENT)
          return m ? 'variableName.function' : 'operator'
        }
        if (ch === '[') {
          stream.next()
          state.stack.push('content')
          return 'bracket'
        }
        return tokenCode(stream, state)
      }
      state.inlineCode = false
    }
    return tokenMarkup(stream, state)
  },
  blankLine(state) {
    state.strong = false
    state.emph = false
  },
  languageData: { commentTokens: { line: '//', block: { open: '/*', close: '*/' } } },
})

export const typstHighlight = HighlightStyle.define([
  { tag: t.heading, color: '#1E40AF', fontWeight: '700' },
  { tag: t.strong, fontWeight: '700' },
  { tag: t.emphasis, fontStyle: 'italic' },
  { tag: t.keyword, color: '#7C3AED' },
  { tag: t.atom, color: '#B45309' },
  { tag: t.number, color: '#B45309' },
  { tag: t.string, color: '#047857' },
  { tag: t.comment, color: '#64748B', fontStyle: 'italic' },
  { tag: t.function(t.variableName), color: '#1D4ED8' },
  { tag: t.special(t.variableName), color: '#0E7490' },
  { tag: t.labelName, color: '#BE185D' },
  { tag: [t.link, t.url], color: '#BE185D', textDecoration: 'underline' },
  { tag: t.monospace, color: '#334155', backgroundColor: '#F1F5F9' },
  { tag: t.list, color: '#C2410C', fontWeight: '600' },
  { tag: t.escape, color: '#B45309' },
  { tag: t.operator, color: '#475569' },
])
