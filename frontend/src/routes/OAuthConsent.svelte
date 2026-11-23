<script>
  // Consentimiento OAuth: una aplicación (Claude, ChatGPT, …) pide acceso a los
  // proyectos del usuario a través del servidor MCP. /oauth/authorize redirige
  // aquí con los parámetros originales en el hash (#/oauth?…).
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import GrantFields from '../lib/GrantFields.svelte'

  let { query } = $props()

  let info = $state(null)
  let error = $state('')
  let busy = $state(false)
  let write = $state(true)
  let all = $state(true)
  let selected = $state([])

  onMount(async () => {
    try {
      info = await api.oauthInfo(query)
      write = info.scopes.includes('typst:write')
    } catch (e) {
      error = e.message
    }
  })

  async function decide(approve) {
    error = ''
    if (approve && !all && selected.length === 0) {
      error = 'Elige al menos un proyecto o «Todos mis proyectos».'
      return
    }
    busy = true
    try {
      const r = await api.oauthDecide({
        query,
        approve,
        scopes: write ? ['typst:read', 'typst:write'] : ['typst:read'],
        all_projects: all,
        projects: all ? [] : selected,
      })
      location.href = r.redirect
    } catch (e) {
      error = e.message
      busy = false
    }
  }
</script>

<div class="mx-auto flex max-w-xl flex-col gap-6">
  <div class="flex flex-col gap-1">
    <h1 class="text-3xl font-bold tracking-tight">Autorizar aplicación</h1>
    <p class="text-sm text-slate-600">Has iniciado sesión como <strong>{session.user?.name}</strong> ({session.user?.username}).</p>
  </div>

  {#if error}<div class="alert-error">{error}</div>{/if}

  {#if info}
    <div class="card flex flex-col gap-5 p-6">
      <div class="flex flex-col gap-1">
        <p class="text-[15px]">
          <strong>{info.client_name}</strong> quiere acceder a tus proyectos de Typst Extend para trabajar con ellos como
          agente (MCP).
        </p>
        <p class="text-xs text-slate-500">
          Al aceptar volverás a <span class="font-mono">{info.redirect_host}</span>. Solo autoriza aplicaciones que hayas
          conectado tú.
        </p>
      </div>

      <GrantFields bind:write bind:all bind:selected allowWrite={info.scopes.includes('typst:write')} disabled={busy} />

      <p class="text-xs text-slate-500">
        La aplicación nunca tendrá más acceso que tú, y puedes revocarlo cuando quieras en
        <a href="#/mcp">Agentes</a>.
      </p>

      <div class="flex flex-wrap justify-end gap-2">
        <button type="button" class="btn-secondary" disabled={busy} onclick={() => decide(false)}>Denegar</button>
        <button type="button" class="btn-primary" disabled={busy} onclick={() => decide(true)}>
          {busy ? 'Autorizando…' : 'Autorizar'}
        </button>
      </div>
    </div>
  {:else if !error}
    <p class="text-sm text-slate-500">Cargando…</p>
  {/if}
</div>
