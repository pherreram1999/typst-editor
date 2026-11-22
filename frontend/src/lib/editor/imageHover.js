// Tarjeta al pasar el ratón sobre image("…") en el código: miniatura, ruta y
// acciones para cambiar la imagen (elegirla del proyecto o subir una nueva).
//
//   imageHover({ resolve(rel) -> { path, exists, url: Promise<string>|null }, onEdit({ from, to, rel }, 'pick'|'upload') })
//
// from/to delimitan el contenido de la cadena (sin comillas).
import { hoverTooltip } from '@codemirror/view'

const CALL = /\bimage\(\s*"((?:[^"\\\n]|\\.)*)"/g

function el(tag, props = {}, ...children) {
  const e = document.createElement(tag)
  Object.assign(e, props)
  for (const c of children) e.append(c)
  return e
}

export function imageHover(handlers) {
  return hoverTooltip(
    (view, pos) => {
      const line = view.state.doc.lineAt(pos)
      for (const m of line.text.matchAll(CALL)) {
        const callFrom = line.from + m.index
        const from = callFrom + m[0].indexOf('"') + 1
        const to = from + m[1].length
        if (pos < callFrom || pos > to + 1) continue
        const rel = m[1].replace(/\\(.)/g, '$1')
        return {
          pos: callFrom,
          end: to + 1,
          above: true,
          create() {
            const info = handlers.resolve(rel)
            const thumb = el('div', { className: 'cm-image-thumb' })
            if (info.url) {
              thumb.textContent = 'Cargando…'
              info.url.then(
                (u) => thumb.replaceChildren(el('img', { src: u, alt: rel })),
                () => (thumb.textContent = 'No se pudo cargar la imagen'),
              )
            } else {
              thumb.textContent = info.exists ? 'Sin vista previa' : 'La imagen no existe en el proyecto'
              thumb.classList.add('cm-image-missing')
            }
            const act = (mode, label, title) =>
              el('button', {
                type: 'button',
                className: 'cm-image-action',
                textContent: label,
                title,
                onmousedown: (e) => e.preventDefault(), // no mover el cursor del editor
                onclick: () => handlers.onEdit({ from, to, rel }, mode),
              })
            const dom = el(
              'div',
              { className: 'cm-image-card' },
              thumb,
              el('div', { className: 'cm-image-path', textContent: info.path || rel, title: info.path }),
              el(
                'div',
                { className: 'cm-image-actions' },
                act('pick', info.exists ? 'Cambiar…' : 'Elegir del proyecto…', 'Elegir otra imagen del proyecto'),
                act('upload', 'Subir nueva', 'Subir una imagen y usarla aquí'),
              ),
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

// Estilos (con las variables de color del tema).
export const imageHoverTheme = {
  '.cm-tooltip.cm-tooltip-hover': { border: '1px solid var(--color-slate-200)', borderRadius: '10px', boxShadow: '0 8px 24px rgb(15 23 42 / 0.15)', overflow: 'hidden' },
  '.cm-image-card': { width: '232px', display: 'flex', flexDirection: 'column', gap: '8px', padding: '8px', fontFamily: 'Inter, system-ui, sans-serif' },
  '.cm-image-thumb': { height: '128px', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: '6px', background: 'var(--color-slate-100)', color: 'var(--color-slate-500)', fontSize: '12px', textAlign: 'center', padding: '0 8px', overflow: 'hidden' },
  '.cm-image-thumb img': { maxWidth: '100%', maxHeight: '100%', objectFit: 'contain', background: 'white' },
  '.cm-image-missing': { background: 'var(--color-amber-50)', color: 'var(--color-amber-800)' },
  '.cm-image-path': { fontFamily: "'JetBrains Mono', monospace", fontSize: '11px', color: 'var(--color-slate-600)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '.cm-image-actions': { display: 'flex', gap: '6px' },
  '.cm-image-action': { flex: '1', height: '28px', border: '1px solid var(--color-slate-300)', borderRadius: '6px', background: 'var(--color-surface)', color: 'var(--color-primary)', font: '500 12px Inter, system-ui, sans-serif', cursor: 'pointer' },
  '.cm-image-action:hover': { background: 'var(--color-primary-tint)' },
}
