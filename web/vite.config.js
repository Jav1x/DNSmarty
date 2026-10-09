import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  // Тестовое окружение: DOM — роутинг-тесты рендерят настоящие <Routes> через
  // react-dom/client; JSX в тестах идёт через esbuild (oxc-pipeline Vite не знает JSX без флага).
  test: {
    environment: "happy-dom",
    esbuild: { jsx: "automatic" },
    // act() вне react-testing-library требует явного флага
    setupFiles: "./src/test-setup.js",
  },
  build: {
    outDir: "../internal/panel/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/install": "http://127.0.0.1:8080",
    },
  },
});
