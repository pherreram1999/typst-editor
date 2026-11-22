// Convierte la salida de render_in_window del renderizador de typst.ts (un
// SVG de todo el documento, con las páginas fuera de la ventana vacías y las
// definiciones de glifos y recortes compartidas) en un SVG independiente por
// página, como los que genera `typst compile --format svg`: así se muestran
// con <img> y se pueden guardar como blob URL.
//
// Cada página lleva solo las definiciones que usa y no lleva la capa de texto
// seleccionable (<foreignObject>), que dentro de un <img> se vería encima.

const TAG = /<(\/?)([a-zA-Z][\w:.-]*)(?:"[^"]*"|'[^']*'|[^'">])*?(\/?)>/g

// Índice justo después del cierre del elemento que abre en `start`.
function elementEnd(s, start) {
  TAG.lastIndex = start
  let depth = 0
  let m
  while ((m = TAG.exec(s))) {
    if (m[3]) {
      if (depth === 0) return TAG.lastIndex
    } else if (m[1]) {
      if (--depth === 0) return TAG.lastIndex
    } else depth++
  }
  return s.length
}

// Elementos de primer nivel de un fragmento.
function children(s) {
  const out = []
  let i = 0
  while ((i = s.indexOf('<', i)) >= 0) {
    if (s[i + 1] === '/') break
    const end = elementEnd(s, i)
    out.push(s.slice(i, end))
    i = end
  }
  return out
}

function inner(s, open, close) {
  const i = s.indexOf(open)
  if (i < 0) return ''
  const j = s.indexOf(close, i + open.length)
  return j < 0 ? '' : s.slice(i + open.length, j)
}

const REF = /(?:href="#|url\(#)([^")]+)/g

// Los enlaces (entradas del índice, citas, referencias, #link) se dibujan como
// <a><rect class="pseudo-link"></a>, un rectángulo que solo es transparente
// gracias al CSS del renderizador; render_in_window emite ese <style> vacío,
// así que el rectángulo se pintaría negro. Dentro de un <img> no son
// clicables: se quitan, y la regla queda como respaldo por si cambia el
// formato.
const PSEUDO_LINK = /<a\b[^>]*>\s*<rect\b[^>]*class="pseudo-link"[^>]*?(?:\/>|>\s*<\/rect>)\s*<\/a>/g
const BASE_STYLE = '.pseudo-link{fill:none}'

/**
 * @param {string} svg salida de render_in_window
 * @returns {{index: number, width: number, height: number, svg: string}[]} páginas no vacías, en orden
 */
export function splitPages(svg) {
  const defs = new Map()
  const headEnd = svg.indexOf('<g class="typst-page"')
  const head = headEnd < 0 ? svg : svg.slice(0, headEnd)
  for (const el of children(inner(head, '<defs class="glyph">', '</defs>') + inner(head, '<defs class="clip-path">', '</defs>'))) {
    const id = el.match(/^<[^>]*?\sid="([^"]+)"/)?.[1]
    if (id) defs.set(id, el)
  }
  const style = inner(head, '<style type="text/css">', '</style>')

  const pages = []
  let index = 0
  let i = headEnd
  while (i >= 0 && (i = svg.indexOf('<g class="typst-page"', i)) >= 0) {
    const openEnd = svg.indexOf('>', i) + 1
    const open = svg.slice(i, openEnd)
    const end = elementEnd(svg, i)
    if (!open.includes('data-dummy')) {
      const width = Number(open.match(/data-page-width="([^"]+)"/)?.[1])
      const height = Number(open.match(/data-page-height="([^"]+)"/)?.[1])
      const body = svg
        .slice(openEnd, end - 4)
        .replace(/<foreignObject[\s\S]*?<\/foreignObject>/g, '')
        .replace(PSEUDO_LINK, '')
      // Definiciones usadas (y las que estas usan a su vez).
      const used = new Map()
      const scan = (text) => {
        for (const m of text.matchAll(REF)) {
          const d = defs.get(m[1])
          if (d && !used.has(m[1])) {
            used.set(m[1], d)
            scan(d)
          }
        }
      }
      scan(body)
      const w = +width.toFixed(3)
      const h = +height.toFixed(3)
      pages.push({
        index,
        width,
        height,
        svg:
          `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 ${w} ${h}" width="${w}" height="${h}">` +
          `<defs>${[...used.values()].join('')}</defs>` +
          `<style type="text/css">${BASE_STYLE}${style}</style>` +
          `<g class="typst-page">${body}</g></svg>`,
      })
    }
    index++
    i = end
  }
  return pages
}
