<script>
  // Lista de personas (author/editor) como filas Apellidos | Nombre, con
  // reordenar y quitar. Emite el valor BibTeX («Apellido, Nombre and …») con
  // onchange; las filas vacías se conservan aquí hasta que se escriben.
  import { parseNames, formatNames } from './bibtex.js'

  let { value = '', label = 'Autores', disabled = false, onchange, onfocus, onblur } = $props()

  let rows = $state([])
  let emitted = null
  // Re-sincronizar solo si el valor cambió desde fuera (otro usuario, el código).
  $effect(() => {
    const v = value
    if (v !== emitted) {
      rows = parseNames(v).map((p) => ({ ...p }))
      emitted = v
    }
  })

  function emit() {
    emitted = formatNames(rows)
    onchange?.(emitted)
  }
  function add() {
    rows = [...rows, { last: '', first: '' }]
    queueMicrotask(() => host?.querySelector('tr:last-child input')?.focus())
  }
  function remove(i) {
    rows = rows.filter((_, k) => k !== i)
    emit()
  }
  function move(i, d) {
    const j = i + d
    if (j < 0 || j >= rows.length) return
    const r = [...rows]
    ;[r[i], r[j]] = [r[j], r[i]]
    rows = r
    emit()
  }
  let host = $state()
</script>

<div class="flex flex-col gap-1.5" bind:this={host}>
  {#if rows.length}
    <table class="w-full border-separate border-spacing-y-1 text-[13px]">
      <thead class="sr-only">
        <tr><th>Apellidos</th><th>Nombre</th><th>Acciones</th></tr>
      </thead>
      <tbody>
        {#each rows as r, i}
          <tr>
            <td class="pr-1.5">
              <input
                class="input !h-8 !text-[13px]"
                placeholder="Apellidos"
                aria-label="{label} {i + 1}: apellidos"
                bind:value={r.last}
                oninput={emit}
                {disabled}
                {onfocus}
                {onblur}
              />
            </td>
            <td class="pr-1.5">
              <input
                class="input !h-8 !text-[13px]"
                placeholder="Nombre"
                aria-label="{label} {i + 1}: nombre"
                bind:value={r.first}
                oninput={emit}
                {disabled}
                {onfocus}
                {onblur}
              />
            </td>
            <td class="w-0 whitespace-nowrap">
              <button type="button" class="size-7 cursor-pointer rounded text-slate-500 hover:bg-slate-100 disabled:opacity-30" title="Subir" aria-label="Subir" disabled={disabled || i === 0} onclick={() => move(i, -1)}>↑</button>
              <button type="button" class="size-7 cursor-pointer rounded text-slate-500 hover:bg-slate-100 disabled:opacity-30" title="Bajar" aria-label="Bajar" disabled={disabled || i === rows.length - 1} onclick={() => move(i, 1)}>↓</button>
              <button type="button" class="size-7 cursor-pointer rounded text-slate-500 hover:bg-red-50 hover:text-red-700" title="Quitar" aria-label="Quitar" {disabled} onclick={() => remove(i)}>×</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
  <button type="button" class="self-start text-xs text-primary underline disabled:opacity-40" {disabled} onclick={add}>+ Añadir {label === 'Editores' ? 'editor' : 'autor'}</button>
  {#if rows.length}<p class="text-[11px] text-slate-400">Para una institución, escríbela entera en «Apellidos» y deja el nombre vacío.</p>{/if}
</div>
