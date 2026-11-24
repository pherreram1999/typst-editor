<script>
  // Acceso de agentes (MCP): URL del servidor, cómo conectar clientes,
  // aplicaciones autorizadas por OAuth y tokens personales.
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import Modal from '../lib/Modal.svelte'
  import GrantFields from '../lib/GrantFields.svelte'

  let grants = $state([])
  let mcpUrl = $state('')
  let names = $state(new Map()) // id de proyecto → nombre
  let loading = $state(true)
  let loadError = $state('')
  let notice = $state('')
  let copied = $state('')

  // Modal de crear token / editar permisos.
  let modalOpen = $state(false)
  let editing = $state(null) // concesión en edición o null al crear un token
  let form = $state({ name: '', expires: 90, write: true, all: true, selected: [] })
  let saving = $state(false)
  let error = $state('')
  let created = $state(null) // token recién creado (se muestra una vez)

  async function load() {
    loading = true
    try {
      const [g, ps] = await Promise.all([api.mcpGrants(), api.listProjects()])
      grants = g.grants
      mcpUrl = g.mcp_url
      names = new Map(ps.map((p) => [p.id, p.name]))
    } catch (e) {
      loadError = e.message
    } finally {
      loading = false
    }
  }
  onMount(load)

  const apps = $derived(grants.filter((g) => !g.personal))
  const tokens = $derived(grants.filter((g) => g.personal))

  async function copy(text, key) {
    try {
      await navigator.clipboard.writeText(text)
      copied = key
      setTimeout(() => copied === key && (copied = ''), 1500)
    } catch {}
  }

  function openCreate() {
    editing = null
    created = null
    form = { name: '', expires: 90, write: true, all: true, selected: [] }
    error = ''
    modalOpen = true
  }

  function openEdit(g) {
    editing = g
    created = null
    form = { name: g.name, expires: 0, write: g.scopes.includes('typst:write'), all: g.all_projects, selected: [...g.projects] }
    error = ''
    modalOpen = true
  }

  async function submit(e) {
    e.preventDefault()
    error = ''
    if (!form.all && form.selected.length === 0) {
      error = 'Elige al menos un proyecto o «Todos mis proyectos».'
      return
    }
    const body = {
      scopes: form.write ? ['typst:read', 'typst:write'] : ['typst:read'],
      all_projects: form.all,
      projects: form.all ? [] : form.selected,
    }
    saving = true
    try {
      if (editing) {
        await api.updateMcpGrant(editing.id, body)
        notice = 'Permisos actualizados.'
        modalOpen = false
      } else {
        const r = await api.createMcpToken({ ...body, name: form.name.trim(), expires_days: Number(form.expires) })
        created = r.token
      }
      await load()
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  async function revoke(g) {
    const what = g.personal ? `el token «${g.name}»` : `el acceso de «${g.client_name}»`
    if (!confirm(`¿Revocar ${what}? Dejará de funcionar de inmediato.`)) return
    try {
      await api.deleteMcpGrant(g.id)
      notice = g.personal ? `Token «${g.name}» revocado.` : `Acceso de «${g.client_name}» revocado.`
      await load()
    } catch (e) {
      loadError = e.message
    }
  }

  const fmt = new Intl.DateTimeFormat('es', { dateStyle: 'medium' })
  const fmtTime = new Intl.DateTimeFormat('es', { dateStyle: 'medium', timeStyle: 'short' })

  function scopeLabel(g) {
    return g.scopes.includes('typst:write') ? 'Lectura y escritura' : 'Solo lectura'
  }

  function projectsLabel(g) {
    if (g.all_projects) return 'Todos mis proyectos'
    const ns = g.projects.map((id) => names.get(id) ?? `#${id}`)
    return ns.length <= 3 ? ns.join(', ') : `${ns.slice(0, 3).join(', ')} y ${ns.length - 3} más`
  }

  const claudeCmd = $derived(
    `claude mcp add --transport http nullpointer-editor ${mcpUrl} --header "Authorization: Bearer ${created ?? '<token>'}"`,
  )
  const jsonConfig = $derived(
    JSON.stringify(
      { mcpServers: { 'nullpointer-editor': { type: 'http', url: mcpUrl, headers: { Authorization: `Bearer ${created ?? '<token>'}` } } } },
      null,
      2,
    ),
  )
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-end justify-between gap-4">
    <div class="flex max-w-2xl flex-col gap-1">
      <h1 class="text-3xl font-bold tracking-tight">Agentes (MCP)</h1>
      <p class="text-sm text-slate-600">
        Conecta asistentes como Claude, ChatGPT o Cursor para que trabajen en tus proyectos: listar y crear proyectos,
        editar archivos, compilar, ver páginas y obtener el PDF. Nunca tienen más acceso que tú.
      </p>
    </div>
    <button type="button" class="btn-primary" onclick={openCreate}>Crear token personal</button>
  </div>

  {#if notice}<div class="alert-info">{notice}</div>{/if}
  {#if loadError}<div class="alert-error">{loadError}</div>{/if}

  <div class="card flex flex-col gap-4 p-5 text-sm leading-relaxed text-slate-700">
    <div class="flex flex-col gap-1.5">
      <span class="label">URL del servidor MCP</span>
      <div class="flex gap-2">
        <input class="input flex-1 font-mono" readonly value={mcpUrl} aria-label="URL del servidor MCP" />
        <button type="button" class="btn-secondary" onclick={() => copy(mcpUrl, 'url')}>{copied === 'url' ? 'Copiada' : 'Copiar'}</button>
      </div>
    </div>
    <div class="grid gap-4 md:grid-cols-2">
      <div>
        <p class="mb-1 font-semibold text-slate-900">Claude, ChatGPT y otros conectores remotos</p>
        <p>
          Añade un conector personalizado con esta URL (en Claude: Ajustes › Conectores; en ChatGPT: modo desarrollador ›
          Conectores). Te pedirá iniciar sesión aquí y elegir permisos y proyectos. El servidor debe ser accesible desde
          internet con https.
        </p>
      </div>
      <div>
        <p class="mb-1 font-semibold text-slate-900">Claude Code, Cursor y clientes locales</p>
        <p>
          Crea un token personal y pásalo en la cabecera <code>Authorization</code>. Los que admiten OAuth (como Claude
          Code) también pueden conectarse sin token: abrirán esta página para autorizar.
        </p>
      </div>
    </div>
  </div>

  {#if loading}
    <p class="text-sm text-slate-500">Cargando…</p>
  {:else}
    <section class="flex flex-col gap-3">
      <h2 class="text-lg font-semibold">Aplicaciones autorizadas</h2>
      {#if apps.length === 0}
        <p class="text-sm text-slate-500">Ninguna aplicación tiene acceso todavía.</p>
      {:else}
        <ul class="card divide-y divide-slate-100">
          {#each apps as g (g.id)}
            <li class="flex flex-wrap items-center gap-4 px-5 py-4">
              <div class="min-w-0 flex-1">
                <p class="font-semibold">{g.client_name}</p>
                <p class="text-xs text-slate-500">
                  {scopeLabel(g)} · {projectsLabel(g)} · autorizada el {fmt.format(new Date(g.created_at))}
                  {#if g.last_used_at}· último uso {fmtTime.format(new Date(g.last_used_at))}{/if}
                </p>
              </div>
              <div class="flex gap-2">
                <button type="button" class="btn-secondary btn-sm" onclick={() => openEdit(g)}>Permisos</button>
                <button type="button" class="btn-secondary btn-sm !text-red-700" onclick={() => revoke(g)}>Revocar</button>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section class="flex flex-col gap-3">
      <h2 class="text-lg font-semibold">Tokens personales</h2>
      {#if tokens.length === 0}
        <p class="text-sm text-slate-500">No has creado tokens personales.</p>
      {:else}
        <ul class="card divide-y divide-slate-100">
          {#each tokens as g (g.id)}
            {@const expired = g.expires_at && new Date(g.expires_at) < new Date()}
            <li class="flex flex-wrap items-center gap-4 px-5 py-4">
              <div class="min-w-0 flex-1">
                <p class="font-semibold">{g.name} {#if expired}<span class="badge ml-1">caducado</span>{/if}</p>
                <p class="text-xs text-slate-500">
                  {scopeLabel(g)} · {projectsLabel(g)} ·
                  {g.expires_at ? `caduca el ${fmt.format(new Date(g.expires_at))}` : 'sin caducidad'}
                  {#if g.last_used_at}· último uso {fmtTime.format(new Date(g.last_used_at))}{:else}· sin usar{/if}
                </p>
              </div>
              <div class="flex gap-2">
                <button type="button" class="btn-secondary btn-sm" onclick={() => openEdit(g)}>Permisos</button>
                <button type="button" class="btn-secondary btn-sm !text-red-700" onclick={() => revoke(g)}>Revocar</button>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  {/if}
</div>

<Modal
  open={modalOpen}
  title={editing ? `Permisos de ${editing.personal ? editing.name : editing.client_name}` : created ? 'Token creado' : 'Crear token personal'}
  onclose={() => !saving && (modalOpen = false)}
>
  {#if created}
    <div class="flex flex-col gap-4 text-sm">
      <div class="alert-info">Copia el token ahora: no se volverá a mostrar.</div>
      <div class="flex gap-2">
        <input class="input flex-1 font-mono" readonly value={created} aria-label="Token" />
        <button type="button" class="btn-secondary" onclick={() => copy(created, 'tok')}>{copied === 'tok' ? 'Copiado' : 'Copiar'}</button>
      </div>
      <div class="flex flex-col gap-1.5">
        <span class="label">Claude Code</span>
        <div class="flex gap-2">
          <pre class="max-h-48 min-w-0 flex-1 overflow-auto rounded-lg border border-slate-200 bg-slate-50 p-3 font-mono text-xs break-all whitespace-pre-wrap">{claudeCmd}</pre>
          <button type="button" class="btn-secondary self-start" onclick={() => copy(claudeCmd, 'cmd')}>{copied === 'cmd' ? 'Copiado' : 'Copiar'}</button>
        </div>
      </div>
      <div class="flex flex-col gap-1.5">
        <span class="label">Configuración JSON (Cursor, .mcp.json, …)</span>
        <div class="flex gap-2">
          <pre class="max-h-48 min-w-0 flex-1 overflow-auto rounded-lg border border-slate-200 bg-slate-50 p-3 font-mono text-xs break-all whitespace-pre-wrap">{jsonConfig}</pre>
          <button type="button" class="btn-secondary self-start" onclick={() => copy(jsonConfig, 'json')}>{copied === 'json' ? 'Copiado' : 'Copiar'}</button>
        </div>
      </div>
      <div class="flex justify-end">
        <button type="button" class="btn-primary" onclick={() => (modalOpen = false)}>Listo</button>
      </div>
    </div>
  {:else}
    <form class="flex flex-col gap-5" onsubmit={submit}>
      {#if error}<div class="alert-error">{error}</div>{/if}
      {#if !editing}
        <label class="flex flex-col gap-1.5">
          <span class="label">Nombre</span>
          <input class="input" bind:value={form.name} required maxlength="128" placeholder="Claude Code en mi portátil" />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="label">Caducidad</span>
          <select class="input" bind:value={form.expires}>
            <option value={7}>7 días</option>
            <option value={30}>30 días</option>
            <option value={90}>90 días</option>
            <option value={365}>1 año</option>
            <option value={0}>Sin caducidad</option>
          </select>
        </label>
      {/if}
      <GrantFields bind:write={form.write} bind:all={form.all} bind:selected={form.selected} disabled={saving} />
      <div class="flex justify-end gap-2 pt-2">
        <button type="button" class="btn-secondary" onclick={() => (modalOpen = false)} disabled={saving}>Cancelar</button>
        <button class="btn-primary" disabled={saving}>{saving ? 'Guardando…' : editing ? 'Guardar' : 'Crear token'}</button>
      </div>
    </form>
  {/if}
</Modal>
