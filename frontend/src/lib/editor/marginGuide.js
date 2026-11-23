// Guía de margen derecho al estilo de los IDE de JetBrains («Hard wrap at» /
// «Visual guides»): una línea vertical tenue en la columna N que indica hasta
// dónde no debería llegar el texto. Es solo visual: no corta ni ajusta nada.
//
// Se dibuja como una capa de CodeMirror detrás del texto (no recibe eventos y
// sigue el scroll); la posición sale de la columna 0 real de las líneas y del
// ancho de carácter de la fuente monoespaciada, así que se recoloca sola al
// cargar la fuente, cambiar el tamaño o hacer zoom.
//
// La columna elegida se guarda por navegador en localStorage
// (typsteditor.margin; 0 = sin guía).
import { EditorView, layer } from '@codemirror/view'

const KEY = 'typsteditor.margin'
export const MARGIN_DEFAULT = 100
export const MARGIN_OPTIONS = [80, 100, 120]

export function savedMargin() {
  try {
    const v = localStorage.getItem(KEY)
    if (v === null) return MARGIN_DEFAULT
    const n = Number(v)
    return Number.isInteger(n) && n >= 0 && n <= 400 ? n : MARGIN_DEFAULT
  } catch {
    return MARGIN_DEFAULT
  }
}

export function saveMargin(col) {
  try {
    localStorage.setItem(KEY, String(col))
  } catch {
    /* sin almacenamiento: solo dura esta sesión */
  }
}

class GuideMarker {
  constructor(left, height) {
    this.left = left
    this.height = height
  }
  draw() {
    const el = document.createElement('div')
    el.className = 'cm-marginGuide'
    this.adjust(el)
    return el
  }
  update(el, prev) {
    if (prev.left !== this.left || prev.height !== this.height) this.adjust(el)
    return true
  }
  adjust(el) {
    el.style.left = `${this.left}px`
    el.style.height = `${this.height}px`
  }
  eq(o) {
    return o.left === this.left && o.height === this.height
  }
}

// Base de coordenadas de las capas (la misma que usa CodeMirror para la selección).
function base(view) {
  const r = view.scrollDOM.getBoundingClientRect()
  return { left: r.left - view.scrollDOM.scrollLeft * view.scaleX }
}

function markers(view, column) {
  if (!column) return []
  // Columna 0: donde empieza el texto de una línea visible (incluye el
  // relleno de .cm-line, así que no hay que adivinarlo).
  const start = view.coordsAtPos(view.lineBlockAt(view.viewport.from).from, 1)
  if (!start) return []
  const x = (start.left - base(view).left) / view.scaleX + column * view.defaultCharacterWidth
  // Con ajuste de línea el texto nunca pasa del borde: una guía más allá no
  // diría nada (y crearía scroll horizontal).
  if (x >= view.scrollDOM.clientWidth + view.scrollDOM.scrollLeft - 1) return []
  return [new GuideMarker(Math.round(x) + 0.5, Math.max(view.contentHeight, view.scrollDOM.clientHeight))]
}

const theme = EditorView.theme({
  '.cm-marginGuideLayer': { pointerEvents: 'none' },
  '.cm-marginGuide': {
    position: 'absolute',
    top: '0',
    width: '0',
    borderLeft: '1px solid var(--editor-margin-guide, var(--color-slate-200))',
  },
})

// Extensión para la columna dada (0: nada).
export function marginGuide(column) {
  if (!column) return []
  return [
    layer({
      above: false,
      class: 'cm-marginGuideLayer',
      markers: (view) => markers(view, column),
      update: (u) => u.geometryChanged || u.viewportChanged || u.docChanged,
    }),
    theme,
  ]
}
