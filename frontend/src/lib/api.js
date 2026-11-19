import { session, setSession, clearSession } from './session.svelte.js'

export class ApiError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}

function onUnauthorized(path) {
  // Token inválido/expirado: cerrar sesión y volver al login.
  if (path !== '/auth/login') {
    clearSession()
    location.hash = '#/login'
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
    const err = new ApiError(res.status, data?.error ?? `Error ${res.status}`)
    err.data = data
    throw err
  }
  return data
}

// Descarga binaria autenticada (PDF, archivos del proyecto).
async function requestBlob(path) {
  const res = await fetch(`/api${path}`, {
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
  listUsers: () => request('GET', '/users'),
  createUser: (u) => request('POST', '/users', u),
  updateUser: (id, u) => request('PUT', `/users/${id}`, u),

  listProjects: () => request('GET', '/projects'),
  createProject: (p) => request('POST', '/projects', p),
  getProject: (id) => request('GET', `/projects/${id}`),
  updateProject: (id, p) => request('PUT', `/projects/${id}`, p),
  deleteProject: (id) => request('DELETE', `/projects/${id}`),

  searchUsers: (query) => request('GET', `/users/search?q=${q(query)}`),
  listMembers: (id) => request('GET', `/projects/${id}/members`),
  addMember: (id, username) => request('POST', `/projects/${id}/members`, { username }),
  removeMember: (id, userId) => request('DELETE', `/projects/${id}/members/${userId}`),

  listTemplates: () => request('GET', '/templates'),
  cloneProject: (id, name) => request('POST', `/projects/${id}/clone`, { name }),
  thumbnail: (id) => requestBlob(`/projects/${id}/thumbnail`),

  listFolders: () => request('GET', '/folders'),
  createFolder: (name, color) => request('POST', '/folders', { name, color }),
  updateFolder: (id, f) => request('PUT', `/folders/${id}`, f),
  deleteFolder: (id) => request('DELETE', `/folders/${id}`),
  setProjectPrefs: (id, prefs) => request('PUT', `/projects/${id}/prefs`, prefs),

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
  rawFile: (id, path) => requestBlob(`/projects/${id}/raw?path=${q(path)}`),

  preview: (id) => request('GET', `/projects/${id}/preview`),
  pdf: (id) => requestBlob(`/projects/${id}/pdf`),

  gitStatus: (id) => request('GET', `/projects/${id}/git/status`),
  gitPull: (id) => request('POST', `/projects/${id}/git/pull`),
  gitPush: (id, message) => request('POST', `/projects/${id}/git/push`, { message }),
}
