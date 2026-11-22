// Código Typst para insertar medios soltados en el editor.

export const MEDIA = /\.(png|jpe?g|gif|svg|webp)$/i

// Ruta de `to` relativa al directorio de `from` (ambas relativas al proyecto,
// con «/»): typst resuelve image("…") desde el archivo que la contiene.
export function relativePath(from, to) {
  const base = from.split('/').slice(0, -1)
  const target = to.split('/')
  let i = 0
  while (i < base.length && i < target.length - 1 && base[i] === target[i]) i++
  return [...Array(base.length - i).fill('..'), ...target.slice(i)].join('/')
}

// Etiqueta legible y única en el documento a partir del nombre del archivo.
export function figureLabel(path, docText) {
  const name = path.split('/').pop().replace(/\.[^.]+$/, '')
  const slug =
    name
      .normalize('NFD')
      .replace(/\p{M}/gu, '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '') || 'figura'
  let label = `fig-${slug}`
  for (let n = 2; docText.includes(`<${label}>`); n++) label = `fig-${slug}-${n}`
  return label
}

// Escapa lo que rompería un bloque de contenido [..] (el resto del marcado,
// como *negrita* o @referencias, se respeta).
export const escapeContent = (t) => t.replace(/[\\[\]#$]/g, (c) => '\\' + c)

// #image si no hay pie; #figure con pie y etiqueta si lo hay. El cursor queda
// al final del bloque.
export function mediaSnippet(src, caption, label) {
  const img = `image("${src.replace(/["\\]/g, (c) => '\\' + c)}", width: 80%)`
  const text = caption ? `#figure(\n  ${img},\n  caption: [${escapeContent(caption)}],\n) <${label}>` : `#${img}`
  return { text, select: [text.length, text.length] }
}

// Ruta del proyecto a la que apunta `rel`, escrita en el archivo `from`
// (como la resuelve typst: relativa a su carpeta, o a la raíz si empieza por «/»).
export function resolvePath(from, rel) {
  const parts = rel.startsWith('/') ? [] : from.split('/').slice(0, -1)
  for (const seg of rel.replace(/^\/+/, '').split('/')) {
    if (seg === '..') parts.pop()
    else if (seg && seg !== '.') parts.push(seg)
  }
  return parts.join('/')
}

// Contenido de una cadena Typst "…".
export const escapeString = (s) => s.replace(/["\\]/g, (c) => '\\' + c)
