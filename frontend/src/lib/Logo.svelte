<script>
  // Marca del producto: logotipo de nullpointer (SVG en línea) + «editor».
  // on="dark": sobre fondos oscuros o de color (barra, panel del login): texto blanco
  // y asterisco azul claro. on="page": colores originales; en el tema oscuro, los claros.
  import svg from '../assets/logo.svg?raw'
  import { theme } from './theme.svelte.js'

  let { on = 'page', size = 'md', class: cls = '' } = $props()

  const light = $derived(on === 'dark' || theme.current === 'dark')
  const h = $derived({ sm: 'h-[18px]', md: 'h-[22px]', lg: 'h-7' }[size])
  // El dibujo incluye el descendente de la «p»: se baja «editor» a la línea base.
  const text = $derived({ sm: 'text-[15px] pb-[1.5px]', md: 'text-[17px] pb-[2px]', lg: 'text-xl pb-[3px]' }[size])
</script>

<span class="inline-flex items-end gap-2 {cls}">
  <span class="logo {h} {light ? 'light' : ''}" role="img" aria-label="nullpointer">{@html svg}</span>
  <span class="{text} leading-none font-medium {light ? 'text-white/80' : 'text-slate-500'}">editor</span>
</span>

<style>
  .logo {
    display: inline-flex;
    aspect-ratio: 1022.5 / 181;
  }
  .logo :global(svg) {
    width: 100%;
    height: 100%;
  }
  .light :global(.word) {
    fill: #fff;
  }
  .light :global(.mark) {
    fill: #93c5fd;
  }
</style>
