import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import type { ProxyOptions } from "vite";

const backend = process.env.PERISCOPE_BACKEND_URL ?? "http://localhost:8088";

// eslint-disable-next-line no-console
console.log(`[periscope] proxying /api and /healthz → ${backend}`);

// quietProxyErrors swallows the noise emitted when a websocket peer
// disconnects mid-write (EPIPE / ECONNRESET). Both are normal when an
// exec session ends or a tab refreshes, but Vite logs them as proxy
// errors which buries real signal.
function configureQuietWS(proxy: ProxyOptions["configure"]) {
  return (server: any, options: any) => {
    proxy?.(server, options);
    const swallow = (err: NodeJS.ErrnoException) => {
      if (err && (err.code === "EPIPE" || err.code === "ECONNRESET")) return;
      // eslint-disable-next-line no-console
      console.error("[periscope] proxy error:", err);
    };
    server.on("error", swallow);
    server.on("proxyReqWs", (proxyReq: any) => {
      proxyReq.on("error", swallow);
    });
    server.on("proxyResWs", (_res: any, socket: any) => {
      socket.on("error", swallow);
    });
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: backend,
        changeOrigin: true,
        ws: true,
        configure: configureQuietWS(undefined),
      },
      "/healthz": {
        target: backend,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
