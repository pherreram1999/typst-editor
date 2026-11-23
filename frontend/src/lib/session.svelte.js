// Estado global de sesión (Svelte 5 runes). El token se guarda en localStorage
// y se valida contra /api/auth/me al cargar la aplicación.
const TOKEN_KEY = 'typsteditor.token'

function readToken() {
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return null
  }
}

export const session = $state({
  token: readToken(),
  user: null,
  ready: false,
  // Sesión de invitado (enlace #/i/…): token y usuario solo en memoria, sin
  // tocar el token guardado de quien tenga cuenta en este navegador.
  guest: false,
})

export function setSession(token, user) {
  session.token = token
  session.user = user
  try {
    localStorage.setItem(TOKEN_KEY, token)
  } catch {}
}

export function clearSession() {
  session.token = null
  session.user = null
  try {
    localStorage.removeItem(TOKEN_KEY)
  } catch {}
}

// Entrar como invitado: el token del enlace sustituye al de la sesión solo en
// memoria (api.js, el WebSocket y el compilador del navegador lo usan igual).
export function enterGuest(token, user) {
  session.guest = true
  session.token = token
  session.user = user
}

// Salir del modo invitado: vuelve el token guardado (si lo hay) para validarlo de nuevo.
export function leaveGuest() {
  session.guest = false
  session.token = readToken()
  session.user = null
}
