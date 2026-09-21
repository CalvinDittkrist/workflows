import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// `npm run build` writes ui/dist/app, which the factory binary embeds. `npm run dev` serves the UI with
// hot reload and sends every API call to a factory started next to it (`go -C factory run . -fake`).
export default defineConfig({
  plugins: [react()],
  // The build writes into dist/app, so the placeholder that keeps dist in git survives it.
  build: { outDir: 'dist/app', chunkSizeWarningLimit: 1000 },
  server: { proxy: { '/api': 'http://127.0.0.1:7341' } },
})
