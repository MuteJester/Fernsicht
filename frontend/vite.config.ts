import { defineConfig } from "vite";

export default defineConfig({
  // GitHub Pages project sites are served from /<repo-name>/.
  // Override with VITE_BASE_PATH in CI.
  base: process.env.VITE_BASE_PATH || "/",
  build: {
    outDir: "dist",
  },
});
