import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
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
