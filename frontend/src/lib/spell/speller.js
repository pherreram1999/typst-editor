// Corrector ortográfico (español de México): fachada del worker
// (spell.worker.js) para el hilo principal. Se crea al primer uso y lo
// comparten todos los editores. Guarda en caché qué palabras están bien o mal
// y mantiene el diccionario personal (localStorage, por navegador) y las
// palabras ignoradas en esta sesión.
import dictURL from './es-MX.words.txt?url'

const USER_KEY = 'typsteditor.spell.words'
const ENABLED_KEY = 'typsteditor.spell'

// Activación por navegador (por omisión, activado).
export function spellEnabled() {
  try {
    return localStorage.getItem(ENABLED_KEY) !== 'off'
  } catch {
    return true
  }
}
export function saveSpellEnabled(on) {
  try {
    localStorage.setItem(ENABLED_KEY, on ? 'on' : 'off')
  } catch {
    /* sin almacenamiento */
  }
}

function readUser() {
  try {
    const v = JSON.parse(localStorage.getItem(USER_KEY) ?? '[]')
    return new Set(Array.isArray(v) ? v.filter((w) => typeof w === 'string') : [])
  } catch {
    return new Set()
  }
}

class Speller {
  constructor() {
    this.worker = null
    this.ready = null
    this.seq = 0
    this.waiting = new Map()
    this.known = new Map() // palabra → true (correcta) | false
    this.user = readUser() // minúsculas
    this.ignored = new Set() // minúsculas, solo esta sesión
    this.listeners = new Set()
    this.failed = false
  }

  start() {
    if (this.ready) return this.ready
    this.worker = new Worker(new URL('./spell.worker.js', import.meta.url), { type: 'module' })
    this.worker.onmessage = (e) => {
      const p = this.waiting.get(e.data.id)
      if (!p) return
      this.waiting.delete(e.data.id)
      if (e.data.error) p.reject(new Error(e.data.error))
      else p.resolve(e.data)
    }
    this.ready = this.call({ type: 'load', url: dictURL })
      .then(() => this.call({ type: 'user', words: [...this.user] }))
      .catch((err) => {
        this.failed = true
        console.warn('corrector ortográfico no disponible:', err)
        throw err
      })
    return this.ready
  }

  call(msg) {
    const id = ++this.seq
    return new Promise((resolve, reject) => {
      this.waiting.set(id, { resolve, reject })
      this.worker.postMessage({ id, ...msg })
    })
  }

  accepted(w) {
    const l = w.toLowerCase()
    return this.user.has(l) || this.ignored.has(l)
  }

  // Devuelve el conjunto de palabras incorrectas entre words.
  async misspelled(words) {
    await this.start()
    const ask = [...new Set(words.filter((w) => !this.known.has(w) && !this.accepted(w)))]
    // Por tandas: el worker responde rápido y no hay que esperar a todas.
    for (let i = 0; i < ask.length; i += 2000) {
      const part = ask.slice(i, i + 2000)
      const { bad } = await this.call({ type: 'check', words: part })
      const badSet = new Set(bad)
      for (const w of part) this.known.set(w, !badSet.has(w))
    }
    if (this.known.size > 50000) this.known.clear() // no crecer sin límite
    return new Set(words.filter((w) => this.known.get(w) === false && !this.accepted(w)))
  }

  async suggest(word) {
    await this.start()
    return (await this.call({ type: 'suggest', word })).list
  }

  addWord(word) {
    this.user.add(word.toLowerCase())
    try {
      localStorage.setItem(USER_KEY, JSON.stringify([...this.user].sort()))
    } catch {
      /* sin almacenamiento: vale para esta sesión */
    }
    this.call({ type: 'user', words: [...this.user] }).catch(() => {})
    this.changed()
  }

  ignore(word) {
    this.ignored.add(word.toLowerCase())
    this.changed()
  }

  // Los editores vuelven a revisar cuando cambia el diccionario personal.
  subscribe(fn) {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }
  changed() {
    for (const fn of this.listeners) fn()
  }
}

export const speller = new Speller()
