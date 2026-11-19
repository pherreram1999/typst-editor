<script>
  import { onMount } from 'svelte'
  import { fly, slide } from 'svelte/transition'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import { d } from '../lib/motion.js'
  import Modal from '../lib/Modal.svelte'

  const empty = () => ({ name: '', username: '', password: '', password2: '', role: 'user', active: true })

  let users = $state([])
  let loading = $state(true)
  let loadError = $state('')

  let modalOpen = $state(false)
  let form = $state(empty())
  let editingId = $state(null) // null = creando
  let saving = $state(false)
  let error = $state('')
  let notice = $state('')
  let noticeTimer

  async function load() {
    loading = true
    loadError = ''
    try {
      users = await api.listUsers()
    } catch (e) {
      loadError = e.message
    } finally {
      loading = false
    }
  }

  onMount(() => {
    load()
    return () => clearTimeout(noticeTimer)
  })

  function showNotice(msg) {
    notice = msg
    clearTimeout(noticeTimer)
    noticeTimer = setTimeout(() => (notice = ''), 4000)
  }

  function openCreate() {
    editingId = null
    form = empty()
    error = ''
    modalOpen = true
  }

  function openEdit(u) {
    editingId = u.id
    form = { name: u.name, username: u.username, password: '', password2: '', role: u.role, active: u.active }
    error = ''
    modalOpen = true
  }

  function closeModal() {
    if (!saving) modalOpen = false
  }

  async function submit(e) {
    e.preventDefault()
    error = ''
    if (needsConfirm && form.password !== form.password2) {
      error = 'Las contraseñas no coinciden.'
      return
    }
    saving = true
    try {
      if (editingId === null) {
        await api.createUser({ name: form.name, username: form.username, password: form.password, role: form.role })
        showNotice(`Usuario «${form.username}» creado correctamente.`)
      } else {
        const body = { name: form.name, role: form.role, active: form.active }
        if (form.password) body.password = form.password
        await api.updateUser(editingId, body)
        showNotice(`Usuario «${form.username}» actualizado.`)
      }
      modalOpen = false
      await load()
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  const isSelf = $derived(editingId !== null && editingId === session.user.id)
  // Se pide confirmación al crear, o al editar si se escribe una contraseña nueva.
  const needsConfirm = $derived(editingId === null || form.password !== '')
  const mismatch = $derived(form.password2 !== '' && form.password !== form.password2)
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-end justify-between gap-3">
    <div class="flex flex-col gap-1">
      <h1 class="text-3xl font-bold tracking-tight">Usuarios</h1>
      <p class="text-sm text-slate-600">Crea y administra las cuentas de acceso.</p>
    </div>
    <button type="button" class="btn-primary" onclick={openCreate}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
        stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
      Nuevo usuario
    </button>
  </div>

  {#if notice}
    <div class="alert-info" role="status" transition:fly={{ y: -8, duration: d(200) }}>{notice}</div>
  {/if}
  {#if loadError}<div class="alert-error" role="alert">{loadError}</div>{/if}

  <section class="card min-w-0 overflow-hidden">
    <div class="overflow-x-auto">
      <table class="w-full border-collapse text-sm">
        <thead>
          <tr class="bg-primary-tint text-left text-xs font-semibold tracking-wide text-blue-900 uppercase">
            <th class="px-4 py-3">Nombre</th>
            <th class="px-4 py-3">Usuario</th>
            <th class="px-4 py-3">Rol</th>
            <th class="px-4 py-3">Estado</th>
            <th class="px-4 py-3"><span class="sr-only">Acciones</span></th>
          </tr>
        </thead>
        <tbody>
          {#if loading && !users.length}
            <tr><td colspan="5" class="px-4 py-6 text-center text-slate-500">Cargando…</td></tr>
          {:else}
            {#each users as u (u.id)}
              <tr class="border-t border-slate-200" in:fly={{ y: 6, duration: d(200) }}>
                <td class="px-4 py-3.5 font-medium">{u.name}</td>
                <td class="px-4 py-3.5 text-slate-600">{u.username}</td>
                <td class="px-4 py-3.5">
                  <span class="badge {u.role === 'admin' ? 'bg-blue-100 text-blue-900' : 'bg-slate-100 text-slate-700'}">
                    {u.role === 'admin' ? 'Administrador' : 'Usuario'}
                  </span>
                </td>
                <td class="px-4 py-3.5">
                  <span class="badge {u.active ? 'bg-emerald-100 text-emerald-800' : 'bg-red-100 text-red-800'}">
                    {u.active ? 'Activo' : 'Inactivo'}
                  </span>
                </td>
                <td class="px-4 py-3.5 text-right">
                  <button type="button" class="btn-secondary btn-sm" onclick={() => openEdit(u)}>Editar</button>
                </td>
              </tr>
            {/each}
          {/if}
        </tbody>
      </table>
    </div>
  </section>
</div>

<Modal open={modalOpen} title={editingId === null ? 'Nuevo usuario' : 'Editar usuario'} onclose={closeModal}>
  <form onsubmit={submit} class="flex flex-col gap-4">
    {#if error}<div class="alert-error" role="alert">{error}</div>{/if}

    <div class="flex flex-col gap-1.5">
      <label for="u-name" class="label">Nombre</label>
      <input id="u-name" class="input" required maxlength="128" bind:value={form.name} />
    </div>
    <div class="flex flex-col gap-1.5">
      <label for="u-user" class="label">Usuario</label>
      <input id="u-user" class="input" required pattern="[a-zA-Z0-9._\-]{'{3,64}'}" autocomplete="off"
        disabled={editingId !== null} bind:value={form.username} />
    </div>
    <div class="flex flex-col gap-1.5">
      <label for="u-pass" class="label">
        Contraseña {#if editingId !== null}<span class="font-normal text-slate-500">(dejar vacío para no cambiar)</span>{/if}
      </label>
      <input id="u-pass" class="input" type="password" autocomplete="new-password" minlength="8" maxlength="72"
        required={editingId === null} bind:value={form.password} />
    </div>
    {#if needsConfirm}
      <div class="flex flex-col gap-1.5" transition:slide={{ duration: d(180) }}>
        <label for="u-pass2" class="label">Confirmar contraseña</label>
        <input id="u-pass2" class="input {mismatch ? 'border-red-700 focus:border-red-700 focus:ring-red-100' : ''}"
          type="password" autocomplete="new-password" required aria-invalid={mismatch}
          aria-describedby={mismatch ? 'u-pass2-err' : undefined} bind:value={form.password2} />
        {#if mismatch}
          <span id="u-pass2-err" class="text-xs text-red-700">Las contraseñas no coinciden.</span>
        {/if}
      </div>
    {/if}
    <div class="flex flex-col gap-1.5">
      <label for="u-role" class="label">Rol</label>
      <select id="u-role" class="input" disabled={isSelf} bind:value={form.role}>
        <option value="user">Usuario</option>
        <option value="admin">Administrador</option>
      </select>
    </div>
    {#if editingId !== null}
      <label class="flex items-center gap-2 text-sm text-slate-700">
        <input type="checkbox" class="size-4 accent-blue-800" disabled={isSelf} bind:checked={form.active} />
        Cuenta activa
      </label>
    {/if}

    <div class="mt-2 flex justify-end gap-3">
      <button type="button" class="btn-secondary" onclick={closeModal} disabled={saving}>Cancelar</button>
      <button type="submit" class="btn-primary" disabled={saving}>
        {saving ? 'Guardando…' : editingId === null ? 'Crear usuario' : 'Guardar cambios'}
      </button>
    </div>
  </form>
</Modal>
