// Tarjeta al pasar el ratón por la primera línea de una tabla (#table(…) o
// table(…) dentro de #figure): resumen y botón «Editar tabla…», que abre el
// editor visual.
//
//   tableHover({ onEdit({ from, to }) })   from/to delimitan «table(…)[…]» (sin «#»)
import { hoverTooltip } from '@codemirror/view'
import { findTables, parseTable } from './tablecode.js'

const MAX = 400000 // una tabla no ocupa más que esto: no copiar documentos enormes

function el(tag, props = {}, ...children) {
  const e = document.createElement(tag)
  Object.assign(e, props)
  for (const c of children) e.append(c)
  return e
}

export function tableHover(handlers) {
  return hoverTooltip(
    (view, pos) => {
      const doc = view.state.doc
      const line = doc.lineAt(pos)
      for (const m of line.text.matchAll(/(#?)table[([]/g)) {
        const at = line.from + m.index + m[1].length // inicio de «table»
        const prev = line.text[m.index - 1]
        if (!m[1] && prev && /[\p{L}\p{N}_.-]/u.test(prev)) continue // table.cell(, mitable(
        if (pos < line.from + m.index) continue
        const chunk = doc.sliceString(at, Math.min(doc.length, at + MAX))
        const t = findTables(chunk)[0]
        if (!t || t.from !== 0) continue
        const from = at
        const to = at + t.to
        return {
          pos: line.from + m.index,
          end: line.to,
          above: true,
          create() {
            const src = chunk.slice(0, t.to)
            const parsed = parseTable(src)
            const info = parsed.ok
              ? `${parsed.model.R} × ${parsed.model.C}` + mergedText(parsed.model) + (parsed.model.headerRows ? ' · con encabezado' : '')
              : parsed.error
            const btn = el('button', {
              type: 'button',
              className: 'cm-table-action',
              textContent: 'Editar tabla…',
              disabled: !parsed.ok,
              title: parsed.ok ? 'Abrir el editor visual: filas, columnas y celdas combinadas' : parsed.error,
              onmousedown: (e) => e.preventDefault(), // no mover el cursor del editor
              onclick: () => handlers.onEdit({ from, to }),
            })
            const dom = el(
              'div',
              { className: 'cm-table-card' },
              el('div', { className: 'cm-table-title', textContent: 'Tabla' }),
              el('div', { className: parsed.ok ? 'cm-table-info' : 'cm-table-info cm-table-error', textContent: info }),
              btn,
            )
            return { dom }
          },
        }
      }
      return null
    },
    { hoverTime: 350 },
  )
}

function mergedText(m) {
  const n = m.cells.filter((c) => c.rs > 1 || c.cs > 1).length
  return n ? ` · ${n} ${n === 1 ? 'celda combinada' : 'celdas combinadas'}` : ''
}

export const tableHoverTheme = {
  '.cm-table-card': { display: 'flex', alignItems: 'center', gap: '10px', padding: '6px 8px', fontFamily: 'Inter, system-ui, sans-serif', maxWidth: '420px' },
  '.cm-table-title': { fontSize: '12px', fontWeight: '600', color: 'var(--color-slate-700)' },
  '.cm-table-info': { fontSize: '12px', color: 'var(--color-slate-500)', flex: '1', minWidth: '0' },
  '.cm-table-error': { color: 'var(--color-amber-800)' },
  '.cm-table-action': { height: '28px', padding: '0 10px', border: '1px solid var(--color-slate-300)', borderRadius: '6px', background: 'var(--color-surface)', color: 'var(--color-primary)', font: '500 12px Inter, system-ui, sans-serif', cursor: 'pointer', whiteSpace: 'nowrap' },
  '.cm-table-action:hover:not(:disabled)': { background: 'var(--color-primary-tint)' },
  '.cm-table-action:disabled': { opacity: '0.5', cursor: 'not-allowed' },
}
