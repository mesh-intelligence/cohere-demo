import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import uiYaml from "@declarative-agents/ui-kit/vite";

export default defineConfig({
  base: "./",
  // The kit plugin validates ../ui.yaml at build time with the same rules the Go
  // validator applies and serves it as virtual:ui-config, so the app reads its
  // routes from the file the chart co-generates rather than a second copy.
  plugins: [react(), uiYaml({ registryIds: ["chat", "observability", "corpus", "provisioning"] })],
  // The kit is a separate package with React as a peer; one React instance
  // serves both, or hooks called inside kit components throw.
  resolve: { dedupe: ["react", "react-dom"] },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5176,
    proxy: {
      "/api": "http://localhost:18080",
      "/monitor": "http://localhost:18082",
    },
  },
});
