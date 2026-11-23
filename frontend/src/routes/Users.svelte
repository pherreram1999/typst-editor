<script>
  import { onMount } from 'svelte'
  import { fly } from 'svelte/transition'
  import { api, authConfig } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import { d } from '../lib/motion.js'
  import Modal from '../lib/Modal.svelte'

  // Alta por correo: el administrador solo da el correo (usuario y nombre son
  // opcionales) y la persona define su contraseña con el enlace que recibe.
  const empty = () => ({ email: '', name: '', username: '', role: 'user', active: true })

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
  // Enlace que no se pudo enviar por correo (sin RESEND o fallo): para copiarlo.
  let manual = $state(null) // { user, link, reason }
  let sending = $state(0) // id del usuario al que se reenvía el enlace
  let copied = $state(false)
  // Sin correo (MAIL_ENABLED=false o sin RESEND): contraseñas temporales.
  let mailEnabled = $state(true)
  let temp = $state(null) // { user, password }

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
    authConfig().then((c) => (mailEnabled = c.mail_enabled), () => {})
    return () => clearTimeout(noticeTimer)
  })

  function showNotice(msg) {
    notice = msg
    clearTimeout(noticeTimer)
    noticeTimer = setTimeout(() => (notice = ''), 6000)
  }

  function openCreate() {
    editingId = null
    form = empty()
    error = ''
    modalOpen = true
  }

  function openEdit(u) {
    editingId = u.id
    form = { email: u.email, name: u.name, username: u.username, role: u.role, active: u.active }
    error = ''
    modalOpen = true
  }

  function closeModal() {
    if (!saving) modalOpen = false
  }

  // Resultado de un envío: aviso o, si no salió el correo, el enlace para copiarlo.
  function afterLink(u, r, sentMsg) {
    if (r.temp_password) temp = { user: u, password: r.temp_password }
    else if (r.mail_sent) showNotice(sentMsg)
    else manual = { user: u, link: r.link, reason: r.mail_error, purpose: r.purpose }
  }

  async function submit(e) {
    e.preventDefault()
    error = ''
    saving = true
    try {
      if (editingId === null) {
        const r = await api.createUser({ email: form.email, name: form.name, username: form.username, role: form.role })
        afterLink(r.user, r, `Cuenta «${r.user.username}» creada. Enviamos a ${r.user.email} el enlace para definir la contraseña.`)
      } else {
        await api.updateUser(editingId, { name: form.name, email: form.email, role: form.role, active: form.active })
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

  async function sendLink(u) {
    if (!mailEnabled && !confirm(`¿Generar una contraseña temporal nueva para ${u.name}? La actual dejará de funcionar y deberá cambiarla al entrar.`)) return
    sending = u.id
    try {
      const r = await api.userAccessLink(u.id)
      afterLink(u, r, u.pending
        ? `Reenviamos a ${u.email} el enlace para definir la contraseña.`
        : `Enviamos a ${u.email} un enlace para restablecer la contraseña.`)
    } catch (e) {
      loadError = e.message
    } finally {
      sending = 0
    }
  }

  async function copyLink(text = manual.link) {
    try {
      await navigator.clipboard.writeText(text)
      copied = true
      setTimeout(() => (copied = false), 2000)
    } catch {
      /* el campo queda seleccionable */
    }
  }

  const isSelf = $derived(editingId !== null && editingId === session.user.id)
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-end justify-between gap-3">
    <div class="flex flex-col gap-1">
      <h1 class="text-3xl font-bold tracking-tight">Usuarios</h1>
      <p class="text-sm text-slate-600">
        {mailEnabled
          ? 'Crea cuentas con el correo de cada persona: recibirá un enlace para definir su contraseña.'
          : 'El envío de correos está desactivado: cada cuenta nueva recibe una contraseña temporal que deberá cambiar al entrar.'}
      </p>
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
            <th class="px-4 py-3">Correo</th>
            <th class="px-4 py-3">Rol</th>
            <th class="px-4 py-3">Estado</th>
            <th class="px-4 py-3"><span class="sr-only">Acciones</span></th>
          </tr>
        </thead>
        <tbody>
          {#if loading && !users.length}
            <tr><td colspan="6" class="px-4 py-6 text-center text-slate-500">Cargando…</td></tr>
          {:else}
            {#each users as u (u.id)}
              <tr class="border-t border-slate-200" in:fly={{ y: 6, duration: d(200) }}>
                <td class="px-4 py-3.5 font-medium">{u.name}</td>
                <td class="px-4 py-3.5 text-slate-600">{u.username}</td>
                <td class="max-w-56 truncate px-4 py-3.5 text-slate-600" title={u.email}>{u.email || '—'}</td>
                <td class="px-4 py-3.5">
                  <span class="badge {u.role === 'admin' ? 'bg-blue-100 text-blue-900' : 'bg-slate-100 text-slate-700'}">
                    {u.role === 'admin' ? 'Administrador' : 'Usuario'}
                  </span>
                </td>
                <td class="px-4 py-3.5">
                  {#if !u.active}
                    <span class="badge bg-red-100 text-red-800">Inactivo</span>
                  {:else if u.must_change_password}
                    <span class="badge bg-amber-100 text-amber-800" title="Tiene una contraseña temporal que debe cambiar al entrar">Clave temporal</span>
                  {:else if u.pending}
                    <span class="badge bg-amber-100 text-amber-800" title="Aún no definió su contraseña con el enlace del correo">Pendiente</span>
                  {:else}
                    <span class="badge bg-emerald-100 text-emerald-800">Activo</span>
                  {/if}
                </td>
                <td class="px-4 py-3.5 text-right whitespace-nowrap">
                  {#if u.active && (u.pending || u.email || !mailEnabled)}
                    <button type="button" class="btn-secondary btn-sm" onclick={() => sendLink(u)} disabled={sending === u.id}
                      title={!mailEnabled ? 'Generar una contraseña temporal nueva' : u.pending ? 'Reenviar el enlace para definir la contraseña' : 'Enviar un enlace para restablecer la contraseña'}
                      >{sending === u.id ? (mailEnabled ? 'Enviando…' : 'Generando…') : !mailEnabled ? 'Contraseña temporal' : u.pending ? 'Reenviar invitación' : 'Restablecer acceso'}</button
                    >
                  {/if}
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
      <label for="u-email" class="label">Correo {#if editingId !== null}<span class="font-normal text-slate-500">(para iniciar sesión, recuperar el acceso y avisos)</span>{/if}</label>
      <input id="u-email" class="input" type="email" autocomplete="off" maxlength="254" required={editingId === null} bind:value={form.email} />
      {#if editingId === null}
        <span class="text-xs text-slate-500">
          {mailEnabled
            ? 'Le enviaremos un enlace para definir su contraseña; hasta entonces no podrá iniciar sesión.'
            : 'El envío de correos está desactivado: se generará una contraseña temporal para entregársela.'}
        </span>
      {/if}
    </div>
    <div class="flex flex-col gap-1.5">
      <label for="u-name" class="label">Nombre {#if editingId === null}<span class="font-normal text-slate-500">(opcional; podrá cambiarlo al activar la cuenta)</span>{/if}</label>
      <input id="u-name" class="input" required={editingId !== null} maxlength="128" bind:value={form.name} />
    </div>
    <div class="flex flex-col gap-1.5">
      <label for="u-user" class="label">Usuario {#if editingId === null}<span class="font-normal text-slate-500">(opcional; si no, se toma del correo)</span>{/if}</label>
      <input id="u-user" class="input" pattern="[a-zA-Z0-9._\-]{'{3,64}'}" autocomplete="off"
        disabled={editingId !== null} bind:value={form.username} />
    </div>
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
        {saving ? 'Guardando…' : editingId === null ? (mailEnabled ? 'Crear y enviar invitación' : 'Crear usuario') : 'Guardar cambios'}
      </button>
    </div>
  </form>
</Modal>

<Modal open={!!manual} title="Enlace de acceso" onclose={() => (manual = null)}>
  {#if manual}
    <div class="flex flex-col gap-4">
      <div class="alert-info">
        No se envió el correo{manual.reason ? ` (${manual.reason})` : ''}. Comparte este enlace con
        <strong>{manual.user.name}</strong>{manual.user.email ? ` (${manual.user.email})` : ''} por otro medio:
        {manual.purpose === 'setup' ? 'con él define su contraseña (vence en 7 días).' : 'con él elige una contraseña nueva (vence en 2 horas).'}
      </div>
      <div class="flex gap-2">
        <label class="sr-only" for="u-link">Enlace</label>
        <input id="u-link" class="input font-mono !text-xs" readonly value={manual.link} onfocus={(e) => e.currentTarget.select()} />
        <button type="button" class="btn-primary shrink-0" onclick={copyLink}>{copied ? '¡Copiado!' : 'Copiar'}</button>
      </div>
      <div class="flex justify-end"><button type="button" class="btn-secondary" onclick={() => (manual = null)}>Cerrar</button></div>
    </div>
  {/if}
</Modal>

<Modal open={!!temp} title="Contraseña temporal" onclose={() => (temp = null)}>
  {#if temp}
    <div class="flex flex-col gap-4">
      <p class="text-sm text-slate-700">
        Entrega esta contraseña a <strong>{temp.user.name}</strong> (usuario <strong>{temp.user.username}</strong>{temp.user.email ? `, ${temp.user.email}` : ''}).
        Al iniciar sesión deberá cambiarla por una propia.
      </p>
      <div class="flex gap-2">
        <label class="sr-only" for="u-temp">Contraseña temporal</label>
        <input id="u-temp" class="input font-mono text-base tracking-wide" readonly value={temp.password} onfocus={(e) => e.currentTarget.select()} />
        <button type="button" class="btn-primary shrink-0" onclick={() => copyLink(temp.password)}>{copied ? '¡Copiada!' : 'Copiar'}</button>
      </div>
      <p class="text-xs text-amber-800">Solo se muestra ahora: no se guarda en claro. Si se pierde, genera otra desde la lista.</p>
      <div class="flex justify-end"><button type="button" class="btn-secondary" onclick={() => (temp = null)}>Listo</button></div>
    </div>
  {/if}
</Modal>
