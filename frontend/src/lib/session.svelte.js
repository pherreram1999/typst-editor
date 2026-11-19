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
