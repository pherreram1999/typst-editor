// URLs (blob) de imágenes del proyecto para miniaturas y vistas previas. La
// clave incluye el tamaño del archivo: al reemplazarlo cambia y se vuelve a
// descargar. Se conservan las últimas MAX.
import { api } from '../api.js'

const MAX = 40
const cache = new Map() // `${id}:${path}:${size}` -> Promise<string>

export function imageURL(projectId, path, size = 0) {
  const key = `${projectId}:${path}:${size}`
  if (cache.has(key)) {
    const p = cache.get(key)
    cache.delete(key)
    cache.set(key, p)
    return p
  }
  const p = api.rawFile(projectId, path).then(({ blob }) => URL.createObjectURL(blob))
  p.catch(() => cache.delete(key))
  cache.set(key, p)
  while (cache.size > MAX) {
    const [k, old] = cache.entries().next().value
    cache.delete(k)
    old.then((u) => URL.revokeObjectURL(u), () => {})
  }
  return p
}
