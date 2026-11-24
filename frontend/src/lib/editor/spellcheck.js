// Revisión ortográfica (español de México) en el editor: subraya las palabras
// que no están en el diccionario (spell/speller.js) y, con clic derecho (o un
// toque sobre la palabra en el celular, o la tecla de menú / Mayús+F10),
// muestra sugerencias, «Agregar al diccionario» e «Ignorar».
//
// Solo se revisa la prosa: el resaltador de Typst (typst-lang.js) no genera
// nodo para el texto normal y sí para código, matemáticas, cadenas, raw,
// etiquetas, referencias, URL y comentarios, que se saltan. Encabezados,
// negritas, cursivas y listas sí se revisan. Se revisa lo visible (más un
// margen) unos cientos de milisegundos después de cada cambio, y nunca la
// palabra que se está escribiendo.
import { EditorView, ViewPlugin, Decoration, showTooltip, keymap } from '@codemirror/view'
import { StateField, StateEffect, Prec } from '@codemirror/state'
import { speller } from '../spell/speller.js'
import { collectWords } from '../spell/prose.js'
import { syntaxTree } from '@codemirror/language'

const MARGIN = 3000 // caracteres revisados por encima y por debajo de lo visible

const setMarks = StateEffect.define()
const setMenu = StateEffect.define() // { from, to, word, keyboard } | null

const misspelledMark = Decoration.mark({ class: 'cm-misspelled' })

// Zonas a revisar: lo visible ± MARGIN, en líneas completas y sin solaparse.
function checkRanges(view) {
  const { doc } = view.state
  const out = []
  for (const r of view.visibleRanges) {
    const from = doc.lineAt(Math.max(0, r.from - MARGIN)).from
    const to = doc.lineAt(Math.min(doc.length, r.to + MARGIN)).to
    const last = out[out.length - 1]
    if (last && from <= last.to) last.to = Math.max(last.to, to)
    else out.push({ from, to })
  }
  return out
}

const spellPlugin = ViewPlugin.fromClass(
  class {
    constructor(view) {
      this.view = view
      this.marks = Decoration.none
      this.timer = 0
      this.touch = 0
      this.unsubscribe = speller.subscribe(() => this.schedule(0))
      this.schedule(300)
    }

    update(u) {
      for (const tr of u.transactions) for (const e of tr.effects) if (e.is(setMarks)) this.marks = e.value
      if (u.docChanged) this.marks = this.marks.map(u.changes)
      if (u.docChanged || u.viewportChanged) this.schedule(u.docChanged ? 400 : 150)
      // El análisis avanzó (documento largo recién abierto): revisar lo que ya se sabe que es prosa.
      else if (syntaxTree(u.state) !== syntaxTree(u.startState)) this.schedule(300)
      else if (u.selectionSet) {
        // Al salir de la palabra que se escribía, revisarla.
        this.schedule(300)
        // Celular: tocar una palabra subrayada muestra sus sugerencias.
        if (Date.now() - this.touch < 700 && u.state.selection.main.empty) {
          const head = u.state.selection.main.head
          setTimeout(() => {
            const t = this.markAt(head)
            if (t && this.view.state.selection.main.head === head) this.view.dispatch({ effects: setMenu.of(t) })
          })
        }
      }
    }

    schedule(ms) {
      clearTimeout(this.timer)
      this.timer = setTimeout(() => this.run(), ms)
    }

    async run() {
      const view = this.view
      const state = view.state
      const words = collectWords(state, checkRanges(view))
      let bad
      try {
        bad = await speller.misspelled(words.map((w) => w.word))
      } catch {
        return // sin diccionario: no se subraya nada
      }
      if (this.destroyed) return
      if (view.state.doc !== state.doc) return this.schedule(200)
      const sel = view.state.selection.main
      const marks = []
      for (const w of words) {
        if (!bad.has(w.word)) continue
        if (sel.empty && sel.head === w.to && view.hasFocus) continue // se está escribiendo
        marks.push(misspelledMark.range(w.from, w.to))
      }
      view.dispatch({ effects: setMarks.of(Decoration.set(marks)) })
    }

    // Palabra subrayada en pos: { from, to, word } o null.
    markAt(pos) {
      let found = null
      this.marks.between(pos, pos, (from, to) => {
        if (from <= pos && pos <= to) found = { from, to, word: this.view.state.sliceDoc(from, to) }
      })
      return found
    }

    destroy() {
      this.destroyed = true
      clearTimeout(this.timer)
      this.unsubscribe()
    }
  },
  {
    decorations: (p) => p.marks,
    eventHandlers: {
      pointerdown(e) {
        this.touch = e.pointerType === 'touch' ? Date.now() : 0
      },
    },
  },
)

// ---------- menú de sugerencias ----------

function closeMenu(view) {
  if (view.state.field(menuField, false)) view.dispatch({ effects: setMenu.of(null) })
}

function buildMenu(view, target) {
  const dom = document.createElement('div')
  dom.className = 'cm-spell-menu'
  dom.setAttribute('role', 'menu')
  dom.setAttribute('aria-label', `Ortografía: ${target.word}`)

  const button = (label, onclick, cls = '') => {
    const b = document.createElement('button')
    b.type = 'button'
    b.setAttribute('role', 'menuitem')
    b.className = 'cm-spell-item ' + cls
    b.textContent = label
    // Sin robar el foco al editor (en el celular cerraría el teclado).
    b.addEventListener('pointerdown', (e) => e.preventDefault())
    b.addEventListener('mousedown', (e) => e.preventDefault())
    b.addEventListener('click', (e) => {
      e.preventDefault()
      onclick()
    })
    return b
  }

  const list = document.createElement('div')
  list.className = 'cm-spell-list'
  const status = document.createElement('div')
  status.className = 'cm-spell-status'
  status.textContent = 'Buscando sugerencias…'
  list.append(status)

  const apply = (s) => {
    const { from, to, word } = target
    if (view.state.sliceDoc(from, to) !== word) return closeMenu(view)
    view.dispatch({
      changes: { from, to, insert: s },
      selection: { anchor: from + s.length },
      effects: setMenu.of(null),
      userEvent: 'input.spell',
    })
    view.focus()
  }

  speller.suggest(target.word).then(
    (sugs) => {
      list.replaceChildren()
      if (!sugs.length) {
        status.textContent = 'Sin sugerencias'
        list.append(status)
      }
      for (const s of sugs) list.append(button(s, () => apply(s), 'cm-spell-suggestion'))
      if (target.keyboard) list.querySelector('button')?.focus()
    },
    () => (status.textContent = 'El corrector no está disponible'),
  )

  const sep = document.createElement('div')
  sep.className = 'cm-spell-sep'
  dom.append(
    list,
    sep,
    button(`Agregar «${target.word}» al diccionario`, () => {
      speller.addWord(target.word)
      closeMenu(view)
      view.focus()
    }),
    button('Ignorar en esta sesión', () => {
      speller.ignore(target.word)
      closeMenu(view)
      view.focus()
    }),
  )

  // Teclado: flechas entre opciones, Esc vuelve al editor.
  dom.addEventListener('keydown', (e) => {
    const items = [...dom.querySelectorAll('button')]
    const i = items.indexOf(document.activeElement)
    if (e.key === 'Escape') {
      e.preventDefault()
      closeMenu(view)
      view.focus()
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const n = items.length
      items[(i + (e.key === 'ArrowDown' ? 1 : n - 1) + n) % n]?.focus()
    }
  })

  // Un clic o toque fuera del menú lo cierra.
  const outside = (e) => {
    if (!dom.contains(e.target)) closeMenu(view)
  }
  return {
    dom,
    mount() {
      setTimeout(() => document.addEventListener('pointerdown', outside, true))
      if (target.keyboard) dom.querySelector('button')?.focus()
    },
    destroy() {
      document.removeEventListener('pointerdown', outside, true)
    },
  }
}

const menuField = StateField.define({
  create: () => null,
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setMenu)) return e.value
    return value && tr.docChanged ? null : value
  },
  provide: (f) =>
    showTooltip.from(f, (t) => t && { pos: t.from, end: t.to, above: false, create: (view) => buildMenu(view, t) }),
})

function openAt(view, pos, keyboard) {
  const t = view.plugin(spellPlugin)?.markAt(pos)
  if (!t) return false
  view.dispatch({ effects: setMenu.of({ ...t, keyboard }) })
  return true
}

const theme = EditorView.theme({
  '.cm-misspelled': {
    textDecoration: 'underline wavy var(--color-red-600, #dc2626)',
    textDecorationSkipInk: 'none',
    textUnderlineOffset: '3px',
    textDecorationThickness: '1px',
  },
  '.cm-tooltip:has(> .cm-spell-menu)': {
    border: '1px solid var(--color-slate-200)',
    borderRadius: '10px',
    boxShadow: '0 8px 24px rgb(15 23 42 / 0.15)',
    background: 'var(--color-surface)',
    overflow: 'hidden',
  },
  '.cm-spell-menu': { minWidth: '210px', maxWidth: '300px', padding: '4px', fontFamily: 'Inter, system-ui, sans-serif' },
  '.cm-spell-item': {
    display: 'block',
    width: '100%',
    minHeight: '30px',
    padding: '4px 10px',
    border: 'none',
    borderRadius: '6px',
    background: 'transparent',
    color: 'var(--color-slate-800)',
    font: '400 13px Inter, system-ui, sans-serif',
    textAlign: 'left',
    cursor: 'pointer',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  '.cm-spell-item:hover, .cm-spell-item:focus-visible': { background: 'var(--color-slate-100)', outline: 'none' },
  '.cm-spell-suggestion': { fontWeight: '600', color: 'var(--color-primary)' },
  '.cm-spell-status': { padding: '6px 10px', fontSize: '12px', color: 'var(--color-slate-500)' },
  '.cm-spell-sep': { height: '1px', margin: '4px 2px', background: 'var(--color-slate-200)' },
  // Dedos: opciones más altas en pantallas táctiles.
  '@media (pointer: coarse)': {
    '.cm-spell-item': { minHeight: '44px', fontSize: '15px' },
  },
})

// Extensión de revisión ortográfica para el editor.
export function spellcheck() {
  speller.start().catch(() => {})
  return [
    spellPlugin,
    menuField,
    theme,
    // Clic derecho sobre una palabra subrayada (o tecla de menú / Mayús+F10 en
    // el cursor; en Android también el toque largo). En otra parte, el menú
    // normal del navegador.
    EditorView.domEventHandlers({
      contextmenu(e, view) {
        const keyboard = !e.clientX && !e.clientY
        const pos = keyboard ? view.state.selection.main.head : view.posAtCoords({ x: e.clientX, y: e.clientY })
        if (pos == null || !openAt(view, pos, keyboard)) return false
        e.preventDefault()
        return true
      },
    }),
    Prec.high(
      keymap.of([
        {
          key: 'Escape',
          run: (view) => {
            if (!view.state.field(menuField, false)) return false
            closeMenu(view)
            return true
          },
        },
      ]),
    ),
  ]
}

// Archivos en los que se revisa la ortografía.
export const spellable = (path) => /\.(typ|txt|md)$/i.test(path ?? '')
