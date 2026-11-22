<script>
  // Cuentas de GitHub del usuario (usuario + token). Se registran una vez y
  // después se eligen al crear o configurar un proyecto.
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import Modal from '../lib/Modal.svelte'

  const empty = () => ({ login: '', token: '', label: '' })

  let accounts = $state([])
  let loading = $state(true)
  let loadError = $state('')
  let modalOpen = $state(false)
  let editing = $state(null) // cuenta en edición o null al crear
  let form = $state(empty())
  let saving = $state(false)
  let error = $state('')
  let notice = $state('')

  async function load() {
    loading = true
    try {
      accounts = await api.listGitHubAccounts()
    } catch (e) {
      loadError = e.message
    } finally {
      loading = false
    }
  }
  onMount(load)

  function openCreate() {
    editing = null
    form = empty()
    error = ''
    modalOpen = true
  }

  function openEdit(a) {
    editing = a
    form = { login: a.login, token: '', label: a.label }
    error = ''
    modalOpen = true
  }

  async function submit(e) {
    e.preventDefault()
    error = ''
    saving = true
    try {
      if (editing) {
        const body = { label: form.label }
        if (form.token.trim()) body.token = form.token
        await api.updateGitHubAccount(editing.id, body)
        notice = `Cuenta «${editing.login}» actualizada.`
      } else {
        const a = await api.createGitHubAccount(form)
        notice = `Cuenta «${a.login}» verificada y guardada.`
      }
      modalOpen = false
      await load()
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  async function remove(a) {
    if (!confirm(`¿Eliminar la cuenta «${a.login}»? Los proyectos que la usan no podrán hacer pull/push hasta que les asignes otra.`)) return
    try {
      await api.deleteGitHubAccount(a.id)
      notice = `Cuenta «${a.login}» eliminada.`
      await load()
    } catch (e) {
      loadError = e.message
    }
  }

  const fmt = new Intl.DateTimeFormat('es', { dateStyle: 'medium' })
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-end justify-between gap-4">
    <div class="flex max-w-2xl flex-col gap-1">
      <h1 class="text-3xl font-bold tracking-tight">Cuentas de GitHub</h1>
      <p class="text-sm text-slate-600">
        Registra tu usuario de GitHub y un token una sola vez. Al crear un proyecto podrás elegir cualquiera de tus
        repositorios, y el proyecto usará esta cuenta para hacer pull y push.
      </p>
    </div>
    <button type="button" class="btn-primary" onclick={openCreate}>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"
        stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
      Añadir cuenta
    </button>
  </div>

  {#if notice}<div class="alert-info">{notice}</div>{/if}
  {#if loadError}<div class="alert-error">{loadError}</div>{/if}

  {#if loading}
    <p class="text-sm text-slate-500">Cargando…</p>
  {:else if accounts.length === 0}
    <div class="card flex flex-col items-center gap-3 p-10 text-center">
      <p class="text-[15px] font-medium">No tienes cuentas de GitHub registradas</p>
      <p class="max-w-md text-sm text-slate-600">
        Añade una para importar tus repositorios como proyectos y sincronizarlos.
      </p>
      <button type="button" class="btn-secondary mt-2" onclick={openCreate}>Añadir la primera</button>
    </div>
  {:else}
    <ul class="card divide-y divide-slate-100">
      {#each accounts as a (a.id)}
        <li class="flex flex-wrap items-center gap-4 px-5 py-4">
          <img
            src="https://github.com/{encodeURIComponent(a.login)}.png?size=80"
            alt=""
            class="size-10 rounded-full bg-slate-100"
            loading="lazy"
            referrerpolicy="no-referrer"
          />
          <div class="min-w-0 flex-1">
            <p class="font-semibold">
              {a.login}
              {#if a.is_org}<span class="badge ml-1">Organización</span>{/if}
              {#if a.label}<span class="font-normal text-slate-500">· {a.label}</span>{/if}
            </p>
            <p class="text-xs text-slate-500">Token guardado · actualizado el {fmt.format(new Date(a.updated_at))}</p>
          </div>
          <div class="flex gap-2">
            <button type="button" class="btn-secondary btn-sm" onclick={() => openEdit(a)}>Cambiar token</button>
            <button type="button" class="btn-secondary btn-sm !text-red-700" onclick={() => remove(a)}>Eliminar</button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  <div class="card p-5 text-sm leading-relaxed text-slate-700">
    <p class="mb-2 font-semibold text-slate-900">¿Qué token necesito?</p>
    <ul class="list-disc space-y-1 pl-5">
      <li>
        <strong>Fine-grained</strong> (recomendado): en GitHub → <em>Settings → Developer settings → Personal access
          tokens → Fine-grained tokens</em>, elige los repositorios y concede <strong>Contents: Read and write</strong>
        (y <em>Metadata: Read</em>, que se añade solo).
      </li>
      <li><strong>Classic</strong>: con el permiso <code>repo</code>.</li>
    </ul>
    <p class="mt-2 text-slate-500">
      El token se comprueba contra GitHub al guardarlo, se almacena en el servidor y nunca se vuelve a mostrar.
    </p>
  </div>
</div>

<Modal open={modalOpen} title={editing ? `Cuenta ${editing.login}` : 'Añadir cuenta de GitHub'} onclose={() => !saving && (modalOpen = false)}>
  <form class="flex flex-col gap-4" onsubmit={submit}>
    {#if error}<div class="alert-error">{error}</div>{/if}
    <label class="flex flex-col gap-1.5">
      <span class="label">Usuario u organización de GitHub</span>
      <input class="input" bind:value={form.login} required disabled={!!editing} autocomplete="off" placeholder="octocat" />
      {#if !editing}
        <span class="text-xs text-slate-500">
          Para una organización, usa el token de un usuario con acceso a ella (en un token fine-grained, elige la
          organización como <em>Resource owner</em>).
        </span>
      {/if}
    </label>
    <label class="flex flex-col gap-1.5">
      <span class="label">{editing ? 'Nuevo token (déjalo vacío para no cambiarlo)' : 'Token de acceso personal'}</span>
      <input class="input font-mono" type="password" bind:value={form.token} required={!editing} autocomplete="off" placeholder="github_pat_…" />
    </label>
    <label class="flex flex-col gap-1.5">
      <span class="label">Descripción (opcional)</span>
      <input class="input" bind:value={form.label} maxlength="128" placeholder="Cuenta del trabajo" />
    </label>
    <div class="flex justify-end gap-2 pt-2">
      <button type="button" class="btn-secondary" onclick={() => (modalOpen = false)} disabled={saving}>Cancelar</button>
      <button class="btn-primary" disabled={saving}>{saving ? 'Verificando…' : 'Guardar'}</button>
    </div>
  </form>
</Modal>
