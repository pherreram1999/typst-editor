<script>
  // Selector de repositorio de GitHub: cuenta → búsqueda → repositorio → rama.
  import { api } from './api.js'

  let {
    accounts,
    accountId = $bindable(null),
    remote = $bindable(''),
    branch = $bindable('main'),
    onpick,
    showAccount = true,
  } = $props()

  let repos = $state([])
  let loading = $state(false)
  let error = $state('')
  let query = $state('')
  let branches = $state([])
  let loadingBranches = $state(false)
  let loadSeq = 0
  const listId = `branches-${Math.random().toString(36).slice(2, 8)}`

  async function load(id, refresh = false) {
    const seq = ++loadSeq
    loading = true
    error = ''
    try {
      const r = await api.githubRepos(id, refresh)
      if (seq !== loadSeq) return
      repos = r
      const current = r.find((x) => x.clone_url === remote)
      if (current) loadBranches(current)
    } catch (e) {
      if (seq === loadSeq) {
        error = e.message
        repos = []
      }
    } finally {
      if (seq === loadSeq) loading = false
    }
  }

  $effect(() => {
    const id = accountId
    if (id) load(id)
    else repos = []
  })

  async function loadBranches(r) {
    loadingBranches = true
    branches = []
    try {
      const b = await api.githubBranches(accountId, r.full_name)
      if (remote === r.clone_url) branches = b
    } catch {
      /* la rama se puede escribir a mano */
    } finally {
      loadingBranches = false
    }
  }

  function pick(r) {
    remote = r.clone_url
    branch = r.default_branch || 'main'
    onpick?.(r)
    loadBranches(r)
  }

  const terms = $derived(query.toLowerCase().split(/\s+/).filter(Boolean))
  const filtered = $derived(
    repos.filter((r) => {
      const hay = `${r.full_name} ${r.description}`.toLowerCase()
      return terms.every((t) => hay.includes(t))
    }),
  )
  const selected = $derived(repos.find((r) => r.clone_url === remote))

  const rtf = new Intl.RelativeTimeFormat('es', { numeric: 'auto' })
  function ago(iso) {
    const s = (new Date(iso) - Date.now()) / 1000
    if (!iso || Number.isNaN(s) || new Date(iso).getFullYear() < 2000) return ''
    for (const [u, n] of [['year', 31536000], ['month', 2592000], ['day', 86400], ['hour', 3600], ['minute', 60]]) {
      if (Math.abs(s) >= n) return rtf.format(Math.round(s / n), u)
    }
    return 'ahora'
  }
</script>

{#if !accounts.length}
  <div class="alert-info">
    No tienes cuentas de GitHub registradas. <a href="#/github">Añade una</a> con tu usuario y token para elegir
    repositorios.
  </div>
{:else}
  <div class="flex flex-col gap-3">
    <label class="flex flex-col gap-1.5" class:hidden={!showAccount}>
      <span class="label">Cuenta de GitHub</span>
      <select class="input" bind:value={accountId}>
        {#each accounts as a (a.id)}
          <option value={a.id}>{a.login}{a.label ? ` — ${a.label}` : ''}</option>
        {/each}
      </select>
    </label>

    <div class="flex flex-col gap-1.5">
      <span class="label">Repositorio</span>
      <div class="flex gap-2">
        <div class="relative flex-1">
          <svg class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-slate-400" width="16" height="16"
            viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"
            ><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg
          >
          <input
            class="input !pl-9"
            type="search"
            bind:value={query}
            placeholder="Buscar repositorio por nombre o descripción…"
            aria-label="Buscar repositorio"
          />
        </div>
        <button
          type="button"
          class="btn-secondary !w-11 !px-0"
          onclick={() => load(accountId, true)}
          disabled={loading}
          title="Volver a cargar desde GitHub"
          aria-label="Recargar repositorios"
        >
          <svg class={loading ? 'animate-spin' : ''} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
            stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
            ><path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" /></svg
          >
        </button>
      </div>

      {#if error}
        <div class="alert-error">{error}</div>
      {/if}

      <ul class="max-h-64 overflow-y-auto rounded-lg border border-slate-200" role="listbox" aria-label="Repositorios">
        {#if loading && !repos.length}
          <li class="px-3 py-6 text-center text-sm text-slate-500">Cargando repositorios…</li>
        {:else if !filtered.length}
          <li class="px-3 py-6 text-center text-sm text-slate-500">
            {repos.length ? 'Ningún repositorio coincide con la búsqueda.' : 'El token no tiene acceso a ningún repositorio.'}
          </li>
        {/if}
        {#each filtered.slice(0, 200) as r (r.full_name)}
          {@const isSel = r.clone_url === remote}
          <li role="option" aria-selected={isSel}>
            <button
              type="button"
              class="flex w-full cursor-pointer flex-col gap-0.5 border-b border-slate-100 px-3 py-2 text-left last:border-0 {isSel
                ? 'bg-primary-tint'
                : 'hover:bg-slate-50'}"
              onclick={() => pick(r)}
            >
              <span class="flex items-center gap-2 text-sm">
                <span class="size-3.5 shrink-0 rounded-full border-2 {isSel ? 'border-primary bg-primary ring-2 ring-white ring-inset' : 'border-slate-300'}"></span>
                <span class="truncate font-medium {isSel ? 'text-primary' : 'text-slate-900'}">{r.full_name}</span>
                <span class="badge !py-0 {r.private ? 'bg-amber-100 text-amber-800' : 'bg-slate-100 text-slate-600'}">{r.private ? 'privado' : 'público'}</span>
                {#if r.empty}<span class="badge !py-0 bg-slate-100 text-slate-600">vacío</span>{/if}
                {#if !r.can_push}<span class="badge !py-0 bg-red-50 text-red-700" title="El token no puede hacer push a este repositorio">solo lectura</span>{/if}
                <span class="ml-auto shrink-0 text-xs text-slate-400">{ago(r.pushed_at)}</span>
              </span>
              {#if r.description}
                <span class="truncate pl-5.5 text-xs text-slate-500">{r.description}</span>
              {/if}
            </button>
          </li>
        {/each}
      </ul>
      {#if repos.length}
        <p class="text-xs text-slate-500">
          {filtered.length} de {repos.length} repositorios{filtered.length > 200 ? ' (se muestran los 200 primeros)' : ''}
        </p>
      {/if}
    </div>

    {#if remote}
      <div class="grid grid-cols-[2fr_1fr] items-end gap-3">
        <p class="truncate pb-3 text-xs text-slate-500" title={remote}>
          {selected ? '' : 'URL actual: '}{remote}
        </p>
        <label class="flex flex-col gap-1.5">
          <span class="label">Rama {loadingBranches ? '…' : ''}</span>
          <input class="input" bind:value={branch} list={listId} required autocomplete="off" />
          <datalist id={listId}>
            {#each branches as b}<option value={b}></option>{/each}
          </datalist>
        </label>
      </div>
      {#if selected?.empty || (selected && !loadingBranches && branches.length && !branches.includes(branch))}
        <p class="-mt-1 text-xs text-slate-500">
          La rama «{branch}» no existe todavía: el proyecto empezará con una plantilla y el primer push la creará.
        </p>
      {/if}
    {/if}
  </div>
{/if}
