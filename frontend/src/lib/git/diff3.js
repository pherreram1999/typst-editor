// Diff de líneas (Myers) y fusión a tres bandas (diff3) para el editor de
// fusión al hacer pull.
//
//   splitLines(text)            -> líneas con su «\n» (join('') devuelve el texto exacto)
//   diffLines(a, b)             -> [{ aFrom, aTo, bFrom, bTo }] tramos distintos (índices de línea)
//   diff3(base, ours, theirs)   -> { base, ours, theirs (líneas), chunks }
//
// Cada chunk describe una zona de la base que cambió en algún lado:
//   { type: 'ours'|'theirs'|'both'|'conflict', base: [from, to], ours: [from, to], theirs: [from, to] }
// 'ours'/'theirs': solo cambió ese lado (sin conflicto); 'both': los dos
// hicieron el mismo cambio; 'conflict': cambios distintos sobre la misma zona.

export function splitLines(text) {
  if (!text) return []
  const out = []
  let i = 0
  for (;;) {
    const j = text.indexOf('\n', i)
    if (j < 0) {
      if (i < text.length) out.push(text.slice(i))
      return out
    }
    out.push(text.slice(i, j + 1))
    i = j + 1
  }
}

// Más allá de esta distancia de edición, el tramo central se da entero como
// un cambio (el algoritmo guarda O(D²) de memoria).
const MAX_D = 5000

// Pares de líneas iguales (índices) entre a y b, en orden.
function matches(a, b) {
  const ids = new Map()
  const id = (s) => {
    let v = ids.get(s)
    if (v === undefined) ids.set(s, (v = ids.size))
    return v
  }
  const A = a.map(id)
  const B = b.map(id)
  const pairs = []
  // Prefijo y sufijo comunes.
  let pre = 0
  while (pre < A.length && pre < B.length && A[pre] === B[pre]) pairs.push([pre, pre++])
  let sa = A.length
  let sb = B.length
  const suffix = []
  while (sa > pre && sb > pre && A[sa - 1] === B[sb - 1]) suffix.push([--sa, --sb])
  const x0 = pre
  const y0 = pre
  const n = sa - x0
  const m = sb - y0
  if (n && m) pairs.push(...myers(A, B, x0, y0, n, m))
  pairs.push(...suffix.reverse())
  return pairs
}

function myers(A, B, x0, y0, n, m) {
  const max = Math.min(n + m, MAX_D)
  const off = max + 1
  const v = new Int32Array(2 * max + 3)
  const trace = []
  let D = -1
  outer: for (let d = 0; d <= max; d++) {
    trace.push(v.slice(off - d - 1, off + d + 2)) // V tras el paso d-1, k ∈ [-d-1, d+1]
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && v[off + k - 1] < v[off + k + 1]) ? v[off + k + 1] : v[off + k - 1] + 1
      let y = x - k
      while (x < n && y < m && A[x0 + x] === B[y0 + y]) (x++, y++)
      v[off + k] = x
      if (x >= n && y >= m) {
        D = d
        break outer
      }
    }
  }
  if (D < 0) return [] // demasiado distinto: todo el tramo es un cambio
  const out = []
  let x = n
  let y = m
  for (let d = D; d > 0; d--) {
    const V = trace[d]
    const at = (k) => V[k + d + 1]
    const k = x - y
    const prevK = k === -d || (k !== d && at(k - 1) < at(k + 1)) ? k + 1 : k - 1
    const px = at(prevK)
    const py = px - prevK
    // Paso de edición (px,py) → (sx,sy) y luego diagonal hasta (x,y).
    const sx = prevK === k - 1 ? px + 1 : px
    const sy = sx - k
    while (x > sx && y > sy) {
      x--
      y--
      out.push([x0 + x, y0 + y])
    }
    x = px
    y = py
  }
  while (x > 0 && y > 0) {
    x--
    y--
    out.push([x0 + x, y0 + y])
  }
  return out.reverse()
}

export function diffLines(a, b) {
  const hunks = []
  let pa = 0
  let pb = 0
  for (const [i, j] of [...matches(a, b), [a.length, b.length]]) {
    if (i > pa || j > pb) hunks.push({ aFrom: pa, aTo: i, bFrom: pb, bTo: j })
    pa = i + 1
    pb = j + 1
  }
  return hunks
}

const same = (x, xr, y, yr) => xr[1] - xr[0] === yr[1] - yr[0] && x.slice(xr[0], xr[1]).every((l, i) => l === y[yr[0] + i])

export function diff3(baseText, oursText, theirsText) {
  const base = splitLines(baseText ?? '')
  const ours = splitLines(oursText ?? '')
  const theirs = splitLines(theirsText ?? '')
  const hs = [
    ...diffLines(base, ours).map((h) => ({ side: 'ours', ...h })),
    ...diffLines(base, theirs).map((h) => ({ side: 'theirs', ...h })),
  ].sort((p, q) => p.aFrom - q.aFrom || p.aTo - q.aTo)

  // Agrupar los cambios que se solapan o se tocan en la base (como git: dos
  // cambios pegados de lados distintos son un conflicto).
  const groups = []
  for (const h of hs) {
    const g = groups[groups.length - 1]
    if (g && h.aFrom <= g.to) {
      g.to = Math.max(g.to, h.aTo)
      g.items.push(h)
    } else groups.push({ from: h.aFrom, to: h.aTo, items: [h] })
  }

  // Desplazamiento acumulado de cada lado (líneas añadidas - quitadas) antes de cada grupo.
  const delta = { ours: 0, theirs: 0 }
  const chunks = []
  for (const g of groups) {
    const range = (side) => {
      const items = g.items.filter((h) => h.side === side)
      if (!items.length) return [g.from + delta[side], g.to + delta[side]]
      const first = items[0]
      const last = items[items.length - 1]
      return [first.bFrom - (first.aFrom - g.from), last.bTo + (g.to - last.aTo)]
    }
    const o = range('ours')
    const t = range('theirs')
    const changedO = g.items.some((h) => h.side === 'ours')
    const changedT = g.items.some((h) => h.side === 'theirs')
    const type = changedO && changedT ? (same(ours, o, theirs, t) ? 'both' : 'conflict') : changedO ? 'ours' : 'theirs'
    chunks.push({ type, base: [g.from, g.to], ours: o, theirs: t })
    delta.ours = o[1] - g.to
    delta.theirs = t[1] - g.to
  }
  return { base, ours, theirs, chunks }
}

// Fusión automática: aplica los cambios sin conflicto; null si hay conflictos.
export function autoMerge(d) {
  const out = []
  let pos = 0
  for (const c of d.chunks) {
    if (c.type === 'conflict') return null
    out.push(...d.base.slice(pos, c.base[0]))
    out.push(...(c.type === 'theirs' ? d.theirs.slice(c.theirs[0], c.theirs[1]) : d.ours.slice(c.ours[0], c.ours[1])))
    pos = c.base[1]
  }
  out.push(...d.base.slice(pos))
  return out.join('')
}
