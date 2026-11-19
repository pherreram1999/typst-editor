// Cursores y selecciones de otros usuarios. Las posiciones se mapean a través
// de cada cambio del documento, así siguen en su sitio al editar.
import { StateEffect, StateField } from '@codemirror/state'
import { Decoration, EditorView, WidgetType } from '@codemirror/view'

export const setRemoteCursor = StateEffect.define()
export const removeRemoteCursor = StateEffect.define()

class CaretWidget extends WidgetType {
  constructor(name, color) {
    super()
    this.name = name
    this.color = color
  }
  eq(o) {
    return o.name === this.name && o.color === this.color
  }
  toDOM() {
    const el = document.createElement('span')
    el.className = 'cm-remote-caret'
    el.style.borderColor = this.color
    const label = document.createElement('span')
    label.className = 'cm-remote-label'
    label.style.backgroundColor = this.color
    label.textContent = this.name
    el.appendChild(label)
    return el
  }
  ignoreEvent() {
    return true
  }
}

const cursorField = StateField.define({
  create: () => new Map(),
  update(cursors, tr) {
    let next = cursors
    if (tr.docChanged && cursors.size) {
      next = new Map()
      for (const [id, c] of cursors) {
        next.set(id, { ...c, anchor: tr.changes.mapPos(c.anchor), head: tr.changes.mapPos(c.head) })
      }
    }
    for (const e of tr.effects) {
      if (e.is(setRemoteCursor)) {
        if (next === cursors) next = new Map(cursors)
        const len = tr.state.doc.length
        next.set(e.value.peer, {
          ...e.value,
          anchor: Math.min(Math.max(e.value.anchor, 0), len),
          head: Math.min(Math.max(e.value.head, 0), len),
        })
      } else if (e.is(removeRemoteCursor)) {
        if (next === cursors) next = new Map(cursors)
        next.delete(e.value)
      }
    }
    return next
  },
  provide: (f) =>
    EditorView.decorations.from(f, (cursors) => {
      const ranges = []
      for (const c of cursors.values()) {
        const from = Math.min(c.anchor, c.head)
        const to = Math.max(c.anchor, c.head)
        if (from < to) {
          ranges.push(
            Decoration.mark({
              attributes: { style: `background-color: ${c.color}26` },
            }).range(from, to),
          )
        }
        ranges.push(Decoration.widget({ widget: new CaretWidget(c.name, c.color), side: 1 }).range(c.head))
      }
      return Decoration.set(ranges, true)
    }),
})

const cursorTheme = EditorView.baseTheme({
  '.cm-remote-caret': {
    position: 'relative',
    borderLeft: '2px solid',
    marginLeft: '-1px',
    marginRight: '-1px',
  },
  '.cm-remote-label': {
    position: 'absolute',
    top: '-1.35em',
    left: '-2px',
    padding: '0 4px',
    borderRadius: '3px 3px 3px 0',
    color: 'white',
    fontSize: '10px',
    lineHeight: '1.35em',
    fontFamily: 'Inter, sans-serif',
    fontWeight: '600',
    whiteSpace: 'nowrap',
    pointerEvents: 'none',
    opacity: '0.9',
    zIndex: '10',
  },
})

export const remoteCursors = [cursorField, cursorTheme]
