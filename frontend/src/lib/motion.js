import { cubicOut } from 'svelte/easing'

// Respeta la preferencia del sistema "reducir movimiento".
const reduced =
  typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches

export const d = (ms) => (reduced ? 0 : ms)

// Entrada de página: sube ligeramente y aparece.
export function pageIn(node, { delay = 0 } = {}) {
  return {
    delay: d(delay),
    duration: d(260),
    easing: cubicOut,
    css: (t, u) => `opacity:${t}; transform: translateY(${u * 12}px)`,
  }
}

// Salida de página: solo se desvanece, rápido.
export function pageOut() {
  return { duration: d(140), css: (t) => `opacity:${t}` }
}

// Panel del modal: escala desde 96% y sube.
export function modalIn() {
  return {
    duration: d(220),
    easing: cubicOut,
    css: (t, u) => `opacity:${t}; transform: translateY(${u * 16}px) scale(${0.96 + 0.04 * t})`,
  }
}

export function modalOut() {
  return {
    duration: d(150),
    css: (t) => `opacity:${t}; transform: scale(${0.98 + 0.02 * t})`,
  }
}

// Panel lateral (drawer): entra y sale deslizándose desde la derecha.
export function drawerIn() {
  return {
    duration: d(260),
    easing: cubicOut,
    css: (t, u) => `transform: translateX(${u * 100}%)`,
  }
}

export function drawerOut() {
  return { duration: d(180), css: (t, u) => `transform: translateX(${u * 100}%)` }
}
