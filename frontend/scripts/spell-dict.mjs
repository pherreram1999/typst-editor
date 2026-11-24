// Genera la lista de palabras del corrector ortográfico (español de México)
// a partir del diccionario Hunspell de RLA-ES (paquete dictionary-es-mx, el
// de LibreOffice; licencia GPL-3.0 / LGPL-3.0 / MPL-1.1): nspell aplica los
// afijos y se obtienen todas las formas (~550 000: conjugaciones, plurales,
// pronombres enclíticos, nombres propios).
//
// Formato (codificación frontal): una palabra por línea, ordenadas por unidad
// UTF-16 (el orden de Array.sort, el que usa la búsqueda binaria del worker);
// el primer carácter de cada línea es 48 + la longitud del prefijo que
// comparte con la anterior (0–35) y le sigue el resto de la palabra.
// 2,2 MB sin comprimir, ~270 KB con gzip.
//
// Lo usa el plugin de vite.config.js (se regenera si falta o si cambia el
// diccionario); también: node scripts/spell-dict.mjs
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
export const OUT = path.join(here, '../src/lib/spell/es-MX.words.txt')
const MAX_PREFIX = 35

export async function ensureSpellDict(out = OUT) {
  // El paquete no exporta sus archivos: se localiza su carpeta en node_modules.
  const require = createRequire(import.meta.url)
  const dir = require.resolve.paths('dictionary-es-mx').map((p) => path.join(p, 'dictionary-es-mx')).find((p) => fs.existsSync(path.join(p, 'index.aff')))
  if (!dir) throw new Error('falta el paquete dictionary-es-mx (npm install)')
  const aff = path.join(dir, 'index.aff')
  const dic = path.join(dir, 'index.dic')
  const src = Math.max(fs.statSync(aff).mtimeMs, fs.statSync(dic).mtimeMs, fs.statSync(fileURLToPath(import.meta.url)).mtimeMs)
  if (fs.existsSync(out) && fs.statSync(out).mtimeMs >= src) return false

  const { default: nspell } = await import('nspell')
  const spell = nspell({ aff: fs.readFileSync(aff), dic: fs.readFileSync(dic) })
  const words = Object.keys(spell.data).filter((w) => w && !/\s/.test(w) && spell.correct(w))
  words.sort()
  const lines = []
  let prev = ''
  for (const w of words) {
    let i = 0
    while (i < MAX_PREFIX && i < prev.length && i < w.length && prev[i] === w[i]) i++
    lines.push(String.fromCharCode(48 + i) + w.slice(i))
    prev = w
  }
  fs.mkdirSync(path.dirname(out), { recursive: true })
  const tmp = out + '.tmp'
  fs.writeFileSync(tmp, lines.join('\n'))
  fs.renameSync(tmp, out)
  return words.length
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const n = await ensureSpellDict()
  console.log(n ? `diccionario es-MX: ${n} palabras → ${path.relative(process.cwd(), OUT)}` : 'diccionario es-MX al día')
}
