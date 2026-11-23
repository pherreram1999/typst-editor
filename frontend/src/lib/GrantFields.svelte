<script>
  // Permisos de un acceso de agente (MCP): lectura o lectura y escritura, y
  // todos los proyectos del usuario o una selección. Lo usan la pantalla de
  // consentimiento OAuth y los tokens personales.
  import { onMount } from 'svelte'
  import { api } from './api.js'

  let {
    write = $bindable(true),
    all = $bindable(true),
    selected = $bindable([]),
    allowWrite = true, // la aplicación pidió permiso de escritura
    disabled = false,
  } = $props()

  let projects = $state([])
  let loading = $state(true)
  let error = $state('')
  let filter = $state('')

  onMount(async () => {
    try {
      // Por MCP solo cuentan los proyectos propios y compartidos (nunca los
      // que un administrador ve por su rol).
      projects = (await api.listProjects()).filter((p) => p.access === 'owner' || p.access === 'member')
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  })

  const norm = (s) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase()
  const shown = $derived(filter ? projects.filter((p) => norm(p.name).includes(norm(filter))) : projects)

  function toggle(id) {
    selected = selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id]
  }
</script>

<fieldset class="flex flex-col gap-2" {disabled}>
  <legend class="label mb-1.5">Permisos</legend>
  <label class="flex items-start gap-2.5 text-sm">
    <input type="radio" class="mt-0.5 size-4 accent-blue-800" checked={!write} onchange={() => (write = false)} />
    <span><span class="font-medium">Solo lectura</span><span class="block text-xs text-slate-500">Ver proyectos y archivos, compilar, ver páginas y descargar el PDF.</span></span>
  </label>
  <label class="flex items-start gap-2.5 text-sm" class:opacity-50={!allowWrite}>
    <input
      type="radio"
      class="mt-0.5 size-4 accent-blue-800"
      checked={write}
      disabled={!allowWrite}
      onchange={() => (write = true)}
    />
    <span>
      <span class="font-medium">Lectura y escritura</span>
      <span class="block text-xs text-slate-500">
        Además, crear proyectos y crear, editar, subir, renombrar y borrar archivos. Los cambios aparecen en vivo a quien
        tenga el archivo abierto.
      </span>
    </span>
  </label>
</fieldset>

<fieldset class="flex flex-col gap-2" {disabled}>
  <legend class="label mb-1.5">Proyectos</legend>
  <label class="flex items-start gap-2.5 text-sm">
    <input type="radio" class="mt-0.5 size-4 accent-blue-800" checked={all} onchange={() => (all = true)} />
    <span><span class="font-medium">Todos mis proyectos</span><span class="block text-xs text-slate-500">Los propios y los compartidos contigo, también los que se creen después.</span></span>
  </label>
  <label class="flex items-start gap-2.5 text-sm">
    <input type="radio" class="mt-0.5 size-4 accent-blue-800" checked={!all} onchange={() => (all = false)} />
    <span><span class="font-medium">Solo los que elija</span><span class="block text-xs text-slate-500">Los proyectos que cree el agente se añaden a la lista.</span></span>
  </label>
  {#if !all}
    <div class="ml-6 flex flex-col gap-2">
      {#if loading}
        <p class="text-sm text-slate-500">Cargando proyectos…</p>
      {:else if error}
        <div class="alert-error">{error}</div>
      {:else if projects.length === 0}
        <p class="text-sm text-slate-500">No tienes proyectos todavía.</p>
      {:else}
        {#if projects.length > 8}
          <input class="input" type="search" placeholder="Buscar proyecto…" bind:value={filter} aria-label="Buscar proyecto" />
        {/if}
        <ul class="max-h-56 overflow-auto rounded-lg border border-slate-200">
          {#each shown as p (p.id)}
            <li>
              <label class="flex cursor-pointer items-center gap-2.5 px-3 py-2 text-sm hover:bg-slate-50">
                <input type="checkbox" class="size-4 accent-blue-800" checked={selected.includes(p.id)} onchange={() => toggle(p.id)} />
                <span class="min-w-0 flex-1 truncate">{p.name}</span>
                {#if p.access === 'member'}<span class="badge">compartido</span>{/if}
              </label>
            </li>
          {:else}
            <li class="px-3 py-2 text-sm text-slate-500">Ningún proyecto coincide.</li>
          {/each}
        </ul>
        <p class="text-xs text-slate-500">{selected.length} seleccionado{selected.length === 1 ? '' : 's'}</p>
      {/if}
    </div>
  {/if}
</fieldset>
