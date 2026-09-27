import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

const webPort = Number(process.env.STATUSFORGE_WEB_PORT ?? 5173);
const apiProxyTarget = process.env.STATUSFORGE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080';

// Local-first: the dev server binds the loopback interface only.
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: webPort,
    strictPort: true,
    proxy: {
      '/api': {
        target: apiProxyTarget,
        changeOrigin: false,
      },
    },
  },
  preview: {
    host: '127.0.0.1',
    port: webPort,
    strictPort: true,
  },
  test: {
    environment: 'jsdom',
    globals: false,
    setupFiles: ['src/test/setup.ts'],
  },
});
