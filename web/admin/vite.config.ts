import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

// The gateway's approval API does not send CORS headers today, so in dev we
// proxy same-origin instead of asking the backend to change. Point the proxy
// at a running gateway with DEV_GATEWAY_URL (default http://localhost:8081).
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const gateway = env.DEV_GATEWAY_URL || 'http://localhost:8081';

  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        '/api': { target: gateway, changeOrigin: true },
        '/healthz': { target: gateway, changeOrigin: true },
      },
    },
    build: {
      outDir: 'dist',
      sourcemap: true,
    },
  };
});
