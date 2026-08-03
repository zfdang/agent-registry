import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const API_TARGET = 'http://localhost:8080'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/v1': API_TARGET,
      '/a2a': API_TARGET,
      '/mcp': API_TARGET,
      '/.well-known': API_TARGET,
      '/health': API_TARGET,
    },
  },
})
