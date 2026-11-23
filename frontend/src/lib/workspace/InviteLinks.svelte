<script>
  // Sección «Enlaces de invitado» del diálogo Compartir (propietario o admin):
  // URL firmadas que caducan en horas, días o meses. Quien abre una entra al
  // proyecto sin cuenta, con un nombre aleatorio o el suyo, para editar o solo
  // leer. Se pueden copiar de nuevo mientras sigan vigentes y revocar
  // (expulsa al momento a quien esté dentro con ese enlace).
  import { api } from '../api.js'

  let { project, open } = $props()

  let invites = $state([])
  let loading = $state(false)
  let error = $state('')
  let busy = $state(false)
  let copied = $state(0) // id del enlace recién copiado
  let showForm = $state(false)
  let fresh = $state(null) // enlace recién creado (se muestra para copiarlo a mano)

  let amount = $state(7)
  let unit = $state('days')
  let permission = $state('edit')
  let label = $state('')

  const UNITS = [
    ['hours', 'horas', 720],
    ['days', 'días', 365],
    ['months', 'meses', 12],
  ]
  const PRESETS = [
    [1, 'hours', '1 hora'],
    [24, 'hours', '24 horas'],
    [7, 'days', '7 días'],
    [30, 'days', '30 días'],
    [3, 'months', '3 meses'],
  ]
  const maxAmount = $derived(UNITS.find((u) => u[0] === unit)[2])

  async function load() {
    loading = true
    error = ''
    try {
      invites = await api.listInvites(project.id)
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
  $effect(() => {
    if (open) {
      load()
    } else {
      showForm = false
      fresh = null
    }
  })

  const linkFor = (inv) => `${location.origin}${location.pathname}#/i/${inv.token}`

  async function copy(inv) {
    try {
      await navigator.clipboard.writeText(linkFor(inv))
      copied = inv.id
      setTimeout(() => copied === inv.id && (copied = 0), 2000)
    } catch {
      fresh = inv
      error = 'No se pudo copiar automáticamente; copia el enlace a mano.'
    }
  }

  async function create(e) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      const inv = await api.createInvite(project.id, { amount: Number(amount), unit, permission, label: label.trim() })
      invites = [inv, ...invites]
      showForm = false
      label = ''
      fresh = inv
      copy(inv)
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }

  async function revoke(inv) {
    if (!confirm('¿Revocar este enlace? Quien esté dentro con él perderá el acceso al momento.')) return
    error = ''
    try {
      await api.revokeInvite(project.id, inv.id)
      invites = invites.filter((x) => x.id !== inv.id)
      if (fresh?.id === inv.id) fresh = null
    } catch (e) {
      error = e.message
    }
  }

  const fmtDate = (d) => new Date(d).toLocaleString('es-MX', { dateStyle: 'medium', timeStyle: 'short' })
  function remaining(d) {
    const h = (new Date(d) - Date.now()) / 3600000
    if (h <= 0) return 'caducado'
    if (h < 1) return 'caduca en menos de una hora'
    if (h < 48) return `caduca en ${Math.round(h)} h`
    const days = Math.round(h / 24)
    return days < 60 ? `caduca en ${days} días` : `caduca en ${Math.round(days / 30)} meses`
  }
</script>

<div class="border-t border-slate-200 pt-4">
  <div class="mb-1 flex items-center justify-between gap-2">
    <p class="text-[13px] font-semibold text-slate-800">Enlaces de invitado</p>
    {#if !showForm}
      <button type="button" class="btn-secondary btn-sm" onclick={() => (showForm = true)}>Crear enlace</button>
    {/if}
  </div>
  <p class="mb-3 text-xs text-slate-500">
    Cualquiera con el enlace entra sin cuenta, con un nombre aleatorio o el que elija, hasta que caduque o lo revoques.
    Los invitados no ven los ajustes, los miembros ni GitHub.
  </p>
  {#if error}<div class="alert-error mb-3">{error}</div>{/if}

  {#if showForm}
    <form class="mb-3 flex flex-col gap-3 rounded-lg border border-slate-200 bg-slate-50 p-3" onsubmit={create}>
      <div>
        <span class="label">Caduca en</span>
        <div class="mb-2 flex flex-wrap gap-1.5">
          {#each PRESETS as [n, u, text]}
            <button
              type="button"
              class="h-7 cursor-pointer rounded-full border px-2.5 text-xs font-medium {amount == n && unit === u
                ? 'border-primary bg-primary-tint text-primary'
                : 'border-slate-200 bg-surface text-slate-600 hover:border-slate-300'}"
              onclick={() => ((amount = n), (unit = u))}>{text}</button
            >
          {/each}
        </div>
        <div class="flex gap-2">
          <label class="sr-only" for="inv-amount">Cantidad</label>
          <input id="inv-amount" class="input !w-24" type="number" min="1" max={maxAmount} bind:value={amount} required />
          <label class="sr-only" for="inv-unit">Unidad</label>
          <select id="inv-unit" class="input" bind:value={unit} onchange={() => (amount = Math.min(amount, maxAmount))}>
            {#each UNITS as [k, text]}<option value={k}>{text}</option>{/each}
          </select>
        </div>
      </div>
      <fieldset>
        <legend class="label">Permiso</legend>
        <div class="grid grid-cols-2 gap-2">
          {#each [['edit', 'Puede editar', 'Editar, crear y subir archivos'], ['view', 'Solo lectura', 'Ver el código, la vista previa y el PDF']] as [k, text, hint]}
            <label
              class="flex cursor-pointer flex-col rounded-lg border p-2.5 text-sm {permission === k
                ? 'border-primary bg-primary-tint'
                : 'border-slate-200 bg-surface hover:border-slate-300'}"
            >
              <span class="flex items-center gap-2 font-medium">
                <input type="radio" name="inv-perm" value={k} bind:group={permission} class="accent-primary" />{text}
              </span>
              <span class="mt-0.5 text-xs text-slate-500">{hint}</span>
            </label>
          {/each}
        </div>
      </fieldset>
      <div>
        <label class="label" for="inv-label">Descripción <span class="font-normal text-slate-500">(opcional, la ve el invitado)</span></label>
        <input id="inv-label" class="input" bind:value={label} maxlength="100" placeholder="p. ej. Revisión del comité" />
      </div>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn-secondary btn-sm" onclick={() => (showForm = false)}>Cancelar</button>
        <button class="btn-primary btn-sm" disabled={busy || !(amount >= 1 && amount <= maxAmount)}>{busy ? 'Creando…' : 'Crear y copiar enlace'}</button>
      </div>
    </form>
  {/if}

  {#if fresh}
    <div class="mb-3 flex gap-2">
      <label class="sr-only" for="inv-link">Enlace de invitado</label>
      <input id="inv-link" class="input font-mono !text-xs" readonly value={linkFor(fresh)} onfocus={(e) => e.currentTarget.select()} />
      <button type="button" class="btn-primary btn-sm shrink-0 !h-10" onclick={() => copy(fresh)}>{copied === fresh.id ? '¡Copiado!' : 'Copiar'}</button>
    </div>
  {/if}

  {#if loading && !invites.length}
    <p class="text-sm text-slate-500">Cargando…</p>
  {:else if invites.length}
    <ul class="flex max-h-64 flex-col divide-y divide-slate-100 overflow-y-auto">
      {#each invites as inv (inv.id)}
        <li class="flex items-center gap-3 py-2.5 {inv.expired ? 'opacity-60' : ''}">
          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-1.5 text-sm">
              <span class="badge {inv.permission === 'edit' ? 'bg-emerald-100 text-emerald-800' : 'bg-amber-100 text-amber-800'}"
                >{inv.permission === 'edit' ? 'Edición' : 'Solo lectura'}</span
              >
              <span class="truncate font-medium">{inv.label || 'Enlace de invitado'}</span>
            </p>
            <p class="truncate text-xs text-slate-500" title="Caduca el {fmtDate(inv.expires_at)}">
              {remaining(inv.expires_at)} · {inv.joins === 0 ? 'sin usar' : `${inv.joins} ${inv.joins === 1 ? 'entrada' : 'entradas'}`}{#if inv.created_by}{' · '}creado por {inv.created_by}{/if}
            </p>
          </div>
          {#if !inv.expired}
            <button type="button" class="btn-secondary btn-sm !h-8 shrink-0" onclick={() => copy(inv)}>{copied === inv.id ? '¡Copiado!' : 'Copiar'}</button>
          {/if}
          <button type="button" class="btn-secondary btn-sm !h-8 shrink-0 !text-red-700" onclick={() => revoke(inv)}>{inv.expired ? 'Quitar' : 'Revocar'}</button>
        </li>
      {/each}
    </ul>
  {:else if !showForm}
    <p class="text-sm text-slate-500">No hay enlaces de invitado.</p>
  {/if}
</div>
