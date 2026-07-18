import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// The dev script sets this when the backend port is customized. Keeping it in
// Vite config lets the frontend call relative URLs such as /api/qqbot/status.
const backendTarget = process.env.VITE_BACKEND_TARGET || "http://127.0.0.1:18080";

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      "/api": backendTarget,
      // NapCat/OneBot uses WebSocket. Proxy it here so the Vite dev origin can
      // exercise the same endpoint shape as the production Go server.
      "/onebot": {
        target: backendTarget,
        ws: true
      }
    }
  },
  build: {
    outDir: "dist",
    emptyOutDir: true
  }
});
