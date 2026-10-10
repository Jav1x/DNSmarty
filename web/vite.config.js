import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "happy-dom",
    esbuild: { jsx: "automatic" },
    setupFiles: "./src/test-setup.js",
  },
  build: {
    outDir: "../internal/panel/dist",
    emptyOutDir: true,
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/install": "http://127.0.0.1:8080",
    },
  },
});
