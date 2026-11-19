<script module>
  // Caché de miniaturas por proyecto (blob URLs) compartida por toda la app.
  const cache = new Map() // id -> Promise<string|null>
  export function invalidateThumb(id) {
    cache.delete(id)
  }
</script>

<script>
  // Miniatura de la primera página de un proyecto (PNG generado por typst).
  import { api } from './api.js'

  let { projectId, alt = '', class: cls = '' } = $props()
  let src = $state(null)
  let failed = $state(false)

  $effect(() => {
    const id = projectId
    let alive = true
    src = null
    failed = false
    if (!cache.has(id)) {
      cache.set(
        id,
        api
          .thumbnail(id)
          .then(({ blob }) => URL.createObjectURL(blob))
          .catch(() => {
            cache.delete(id) // reintentar la próxima vez
            return null
          }),
      )
    }
    cache.get(id).then((url) => {
      if (!alive) return
      if (url) src = url
      else failed = true
    })
    return () => (alive = false)
  })
</script>

<div class="relative overflow-hidden bg-slate-100 {cls}">
  {#if src}
    <img {src} {alt} class="size-full object-cover object-top" draggable="false" />
  {:else if failed}
    <div class="flex size-full items-center justify-center p-4 text-center text-xs text-slate-400">Sin vista previa</div>
  {:else}
    <div class="size-full animate-pulse bg-slate-200/70"></div>
  {/if}
</div>
