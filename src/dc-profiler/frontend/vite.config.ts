import {defineConfig} from 'vite';
import react from '@vitejs/plugin-react';

// The Go backend serves the built SPA and the /api/* routes. In dev, proxy
// /api to a locally-running backend on :8080.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
});
