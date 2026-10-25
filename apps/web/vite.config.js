import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In development the dashboard runs on Vite and forwards /api to the Go API.
export default defineConfig({
  plugins: [react()],
  server: { proxy: { '/api': { target: process.env.IGNITION_API_URL ?? 'http://localhost:8080', changeOrigin: false } } },
});
