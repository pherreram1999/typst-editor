import { session, setSession, clearSession } from './session.svelte.js'

export class ApiError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}

const RETURN_KEY = 'typsteditor.return'

// Rutas a las que volver tras iniciar sesión (la autorización OAuth de una
// aplicación llega con su petición en el hash y no debe perderse; un enlace
// de invitado o de unirse vuelve a abrirse ya con la cuenta).
export function rememberReturn(hash = location.hash) {
  if (/^#\/(oauth\?|unirse\/|i\/)/.test(hash)) {
    try {
      sessionStorage.setItem(RETURN_KEY, hash)
    } catch {}
  }
}

export function takeReturn() {
  try {
    const h = sessionStorage.getItem(RETURN_KEY)
    sessionStorage.removeItem(RETURN_KEY)
    return h
  } catch {
    return null
  }
}

function onUnauthorized(path) {
  // Invitado cuyo enlace caducó o se revocó: vuelve a la entrada del enlace
  // (Invite.svelte) sin cerrar la sesión guardada de quien tenga cuenta.
  if (session.guest) {
    session.user = null
    return
  }
  // Token inválido/expirado: cerrar sesión y volver al login.
  if (path !== '/auth/login') {
    rememberReturn()
    clearSession()
    // La vista pública de revisión (#/r/…) no necesita sesión: quedarse ahí.
    if (!/^#\/(r|cuenta|unirse)\//.test(location.hash)) location.hash = '#/login'
  }
}

async function request(method, path, body) {
  const headers = { Accept: 'application/json' }
  const isForm = body instanceof FormData
  if (body !== undefined && !isForm) headers['Content-Type'] = 'application/json'
  if (session.token) headers.Authorization = `Bearer ${session.token}`

  const res = await fetch(`/api${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : isForm ? body : JSON.stringify(body),
  })
  const data = res.status === 204 ? null : await res.json().catch(() => null)

  if (!res.ok) {
    // Token inválido/expirado: cerrar sesión y volver al login.
    if (res.status === 401) onUnauthorized(path)
    // Un administrador le asignó una contraseña temporal: App muestra el cambio obligatorio.
    if (res.status === 403 && data?.code === 'password_change_required' && session.user) session.user.must_change_password = true
    const err = new ApiError(res.status, data?.error ?? `Error ${res.status}`)
    err.data = data
    throw err
  }
  return data
}

// Descarga binaria autenticada (PDF, archivos del proyecto).
async function requestBlob(path, init = {}) {
  const res = await fetch(`/api${path}`, {
    ...init,
    headers: session.token ? { Authorization: `Bearer ${session.token}` } : {},
  })
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    if (res.status === 401) onUnauthorized(path)
    const err = new ApiError(res.status, data?.error ?? `Error ${res.status}`)
    err.data = data
    throw err
  }
  return { blob: await res.blob(), headers: res.headers }
}

const q = encodeURIComponent

// Importa un proyecto desde la computadora: un .zip ({ zip }) o archivos con su
// ruta relativa ({ files: [{ path, file }] }). XHR en vez de fetch para poder
// informar del progreso de la subida (onProgress recibe 0…1).
function importProject(name, { zip, files }, onProgress) {
  const fd = new FormData()
  fd.append('name', name) // el servidor lo necesita antes que los archivos
  if (zip) fd.append('archive', zip, zip.name)
  else for (const f of files) {
    fd.append('path', f.path)
    fd.append('file', f.file, f.file.name)
  }
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/projects/import')
    xhr.setRequestHeader('Accept', 'application/json')
    if (session.token) xhr.setRequestHeader('Authorization', `Bearer ${session.token}`)
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total)
    xhr.onload = () => {
      let data = null
      try {
        data = JSON.parse(xhr.responseText)
      } catch {}
      if (xhr.status >= 200 && xhr.status < 300) return resolve(data)
      if (xhr.status === 401) onUnauthorized('/projects/import')
      const err = new ApiError(xhr.status, data?.error ?? (xhr.status === 413 ? 'La subida es demasiado grande' : `Error ${xhr.status}`))
      err.data = data
      reject(err)
    }
    xhr.onerror = () => reject(new ApiError(0, 'No se pudo completar la subida (¿conexión interrumpida o archivo demasiado grande?)'))
    xhr.send(fd)
  })
}

// URL del WebSocket de edición de un archivo (el token va en la query).
export function wsURL(projectId, path, since) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  let url = `${proto}//${location.host}/api/projects/${projectId}/ws?path=${q(path)}&token=${q(session.token ?? '')}`
  if (since != null) url += `&since=${since}`
  return url
}

export const api = {
  async login(username, password) {
    const data = await request('POST', '/auth/login', { username, password })
    setSession(data.token, data.user)
    return data.user
  },
  me: () => request('GET', '/auth/me'),
  // Añadir o cambiar el correo propio: se guarda al confirmarlo con el enlace recibido.
  changeEmail: (email) => request('PUT', '/auth/me/email', { email }),
  changePassword: (current, next) => request('POST', '/auth/password', { current_password: current, new_password: next }),
  listUsers: () => request('GET', '/users'),
  // Reenvía el enlace de alta (cuenta pendiente) o de restablecer la contraseña.
  userAccessLink: (id) => request('POST', `/users/${id}/access-link`),
  createUser: (u) => request('POST', '/users', u),
  updateUser: (id, u) => request('PUT', `/users/${id}`, u),

  listProjects: () => request('GET', '/projects'),
  createProject: (p) => request('POST', '/projects', p),
  importProject,
  getProject: (id) => request('GET', `/projects/${id}`),
  updateProject: (id, p) => request('PUT', `/projects/${id}`, p),
  deleteProject: (id) => request('DELETE', `/projects/${id}`),

  searchUsers: (query) => request('GET', `/users/search?q=${q(query)}`),
  listMembers: (id) => request('GET', `/projects/${id}/members`),
  // who: nombre de usuario o correo (un correo sin cuenta recibe una invitación).
  addMember: (id, who) => request('POST', `/projects/${id}/members`, who.includes('@') ? { email: who } : { username: who }),
  listEmailInvites: (id) => request('GET', `/projects/${id}/email-invites`),
  deleteEmailInvite: (id, inviteId) => request('DELETE', `/projects/${id}/email-invites/${inviteId}`),
  resendEmailInvite: (id, inviteId) => request('POST', `/projects/${id}/email-invites/${inviteId}/resend`),
  // Aceptar una invitación por correo con la sesión actual.
  acceptJoin: (token) => request('POST', `/join/${q(token)}/accept`),
  removeMember: (id, userId) => request('DELETE', `/projects/${id}/members/${userId}`),

  listTemplates: () => request('GET', '/templates'),
  cloneProject: (id, name) => request('POST', `/projects/${id}/clone`, { name }),
  thumbnail: (id) => requestBlob(`/projects/${id}/thumbnail`),

  listFolders: () => request('GET', '/folders'),
  createFolder: (name, color) => request('POST', '/folders', { name, color }),
  updateFolder: (id, f) => request('PUT', `/folders/${id}`, f),
  deleteFolder: (id) => request('DELETE', `/folders/${id}`),
  setProjectPrefs: (id, prefs) => request('PUT', `/projects/${id}/prefs`, prefs),

  // Agentes (MCP): aplicaciones autorizadas por OAuth y tokens personales.
  mcpGrants: () => request('GET', '/mcp/grants'),
  createMcpToken: (t) => request('POST', '/mcp/tokens', t),
  updateMcpGrant: (id, g) => request('PUT', `/mcp/grants/${id}`, g),
  deleteMcpGrant: (id) => request('DELETE', `/mcp/grants/${id}`),
  oauthInfo: (query) => request('GET', `/oauth/authorize/info?${query}`),
  oauthDecide: (body) => request('POST', '/oauth/authorize', body),

  listGitHubAccounts: () => request('GET', '/github/accounts'),
  createGitHubAccount: (a) => request('POST', '/github/accounts', a),
  updateGitHubAccount: (id, a) => request('PUT', `/github/accounts/${id}`, a),
  deleteGitHubAccount: (id) => request('DELETE', `/github/accounts/${id}`),
  githubRepos: (id, refresh = false) => request('GET', `/github/accounts/${id}/repos${refresh ? '?refresh=1' : ''}`),
  githubBranches: (id, repo) => request('GET', `/github/accounts/${id}/branches?repo=${q(repo)}`),

  listFiles: (id) => request('GET', `/projects/${id}/files`),
  createFile: (id, path) => request('POST', `/projects/${id}/files`, { path }),
  deleteFile: (id, path) => request('DELETE', `/projects/${id}/files?path=${q(path)}`),
  uploadFile: (id, file, dir = '') => {
    const fd = new FormData()
    fd.append('file', file)
    if (dir) fd.append('dir', dir)
    return request('POST', `/projects/${id}/upload`, fd)
  },
  optimizeImage: (id, path) => request('POST', `/projects/${id}/images/optimize`, { path }),
  rawFile: (id, path) => requestBlob(`/projects/${id}/raw?path=${q(path)}`),
  // Texto actual de un archivo (revalidando: la caché del navegador podría estar atrasada).
  textFile: (id, path) => requestBlob(`/projects/${id}/raw?path=${q(path)}`, { cache: 'no-cache' }).then(({ blob }) => blob.text()),

  preview: (id) => request('GET', `/projects/${id}/preview`),
  previewSync: (id) => request('GET', `/projects/${id}/preview/sync`),
  previewFiles: (id) => request('GET', `/projects/${id}/preview/files`),
  previewPage: (id, hash) => requestBlob(`/projects/${id}/preview/pages/${hash}`),
  pdf: (id) => requestBlob(`/projects/${id}/pdf`),
  // URL de descarga directa del .zip (ticket de un solo uso, caduca en un minuto).
  exportZipURL: (id) => request('POST', `/projects/${id}/export/ticket`).then((r) => r.url),
  // Enlace de revisión (una compilación guardada que se comparte con un enlace público).
  getReview: (id) => request('GET', `/projects/${id}/review`),
  publishReview: (id, body = {}) => request('POST', `/projects/${id}/review`, body),
  updateReview: (id, body) => request('PUT', `/projects/${id}/review`, body),
  deleteReview: (id) => request('DELETE', `/projects/${id}/review`),
  // Enlaces de invitado (URL firmadas que caducan).
  listInvites: (id) => request('GET', `/projects/${id}/invites`),
  createInvite: (id, body) => request('POST', `/projects/${id}/invites`, body),
  revokeInvite: (id, inviteId) => request('DELETE', `/projects/${id}/invites/${inviteId}`),

  gitStatus: (id) => request('GET', `/projects/${id}/git/status`),
  gitPull: (id) => request('POST', `/projects/${id}/git/pull`),
  // Completa un pull con conflictos: files = [{ path, ours_blob, take | content | delete }].
  gitResolve: (id, theirs, files) => request('POST', `/projects/${id}/git/pull/resolve`, { theirs, files }),
  gitPush: (id, message) => request('POST', `/projects/${id}/git/push`, { message }),
}

// Lectura pública de un enlace de revisión (sin sesión: un 404 no debe cerrar
// la sesión de quien sí la tenga).
export async function reviewInfo(token) {
  const res = await fetch(`/api/review/${q(token)}`, { headers: { Accept: 'application/json' } })
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new ApiError(res.status, data?.error ?? `Error ${res.status}`)
  return data
}
export const reviewPageURL = (token, n, version) => `/api/review/${q(token)}/pages/${n}?v=${version}`
export const reviewPdfURL = (token) => `/api/review/${q(token)}/document.pdf`

// Enlace de invitado (#/i/<token>): datos para la pantalla de entrada y
// apertura de la sesión de invitado. Sin sesión, como reviewInfo.
async function publicJSON(method, path, body) {
  const res = await fetch(`/api${path}`, {
    method,
    headers: { Accept: 'application/json', ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new ApiError(res.status, data?.error ?? `Error ${res.status}`)
  return data
}
// Petición GET con el token de la cuenta propia, sin efectos sobre la sesión
// (sirve aunque haya una sesión de invitado abierta en memoria).
export async function accountRequest(token, path) {
  const res = await fetch(`/api${path}`, { headers: { Accept: 'application/json', Authorization: `Bearer ${token}` } })
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new ApiError(res.status, data?.error ?? `Error ${res.status}`)
  return data
}
export const inviteInfo = (token) => publicJSON('GET', `/invite/${q(token)}`)
export const joinInvite = (token, name) => publicJSON('POST', `/invite/${q(token)}/join`, { name })

// Cuentas por correo (sin sesión): configuración, «olvidé mi contraseña»,
// enlaces de alta/restablecer/confirmar correo e invitaciones a un proyecto.
export const authConfig = () => publicJSON('GET', '/auth/config')
export const forgotPassword = (login) => publicJSON('POST', '/auth/forgot', { login })
export const accountInfo = (token) => publicJSON('GET', `/auth/account/${q(token)}`)
export async function accountPassword(token, body) {
  const data = await publicJSON('POST', `/auth/account/${q(token)}/password`, body)
  setSession(data.token, data.user)
  return data.user
}
export const accountEmail = (token) => publicJSON('POST', `/auth/account/${q(token)}/email`)
export const joinInfo = (token) => publicJSON('GET', `/join/${q(token)}`)
export async function joinRegister(token, body) {
  const data = await publicJSON('POST', `/join/${q(token)}/register`, body)
  setSession(data.token, data.user)
  return data
}
