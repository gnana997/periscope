import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const backend = process.env.PERISCOPE_BACKEND_URL ?? "http://localhost:8080";

// eslint-disable-next-line no-console
console.log(`[periscope] proxying /api and /healthz → ${backend}`);

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: backend,
        changeOrigin: true,
        ws: true,
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
