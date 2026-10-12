import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  base: "/console/",
  plugins: [react()],
  server: { proxy: { "/console/api": "http://127.0.0.1:8080" } },
  build: { outDir: "dist", emptyOutDir: true },
  test: { environment: "jsdom", clearMocks: true },
});
