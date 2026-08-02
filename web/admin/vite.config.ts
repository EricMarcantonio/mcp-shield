import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

// The gateway's approval API does not send CORS headers today, so in dev we
// proxy same-origin instead of asking the backend to change. Point the proxy
// at a running gateway with DEV_GATEWAY_URL (default http://localhost:8081).
//
// `changeOrigin` stays off deliberately. The gateway refuses a state-changing
// request whose Origin does not name the host it was addressed to, which is
// how approve and reject are protected from a form POST on any page on the
// internet. Rewriting Host to the gateway's own address would break that match
// and 403 every approval in dev. Leaving it alone forwards Host unchanged,
// which is exactly what the production nginx does (`proxy_set_header Host
// $host`) — so dev behaves the way the shipped image does.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const gateway = env.DEV_GATEWAY_URL || 'http://localhost:8081';

  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        '/api': { target: gateway },
        '/healthz': { target: gateway },
      },
    },
    build: {
      outDir: 'dist',
      sourcemap: true,
    },
  };
});
