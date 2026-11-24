import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { ensureSpellDict } from './scripts/spell-dict.mjs'

// Diccionario del corrector ortográfico (src/lib/spell/es-MX.words.txt, no se
// versiona): se genera antes de compilar o de arrancar el servidor de desarrollo.
const spellDict = () => ({
  name: 'spell-dict',
  async buildStart() {
    const n = await ensureSpellDict()
    if (n) this.info?.(`diccionario es-MX generado (${n} palabras)`)
  },
})

export default defineConfig({
  plugins: [spellDict(), svelte()],
  preview: {
    proxy: {
      '/api': { target: process.env.API_TARGET ?? 'http://localhost:8080', ws: true },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // ws: true reenvía también el WebSocket de edición colaborativa.
      // API_TARGET permite apuntar a otro backend (por defecto el local en :8080).
      '/api': { target: process.env.API_TARGET ?? 'http://localhost:8080', ws: true },
    },
  },
})
