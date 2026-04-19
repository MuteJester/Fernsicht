import { defineConfig } from "vite";

// Dev-only proxy: the production signaling server only sets CORS
// headers for https://app.fernsicht.space, so the dev server has to
// proxy /watch, /poll/, and /ticket/ to make the browser see them as
// same-origin. Set VITE_SERVER_URL=/ in .env.local to use this.
const SIGNALING_TARGET =
  process.env.VITE_DEV_SIGNALING_TARGET || "https://signal.fernsicht.space";

export default defineConfig({
  // GitHub Pages project sites are served from /<repo-name>/.
  // Override with VITE_BASE_PATH in CI.
  base: process.env.VITE_BASE_PATH || "/",
  build: {
    outDir: "dist",
  },
  server: {
    proxy: {
      "/watch":   { target: SIGNALING_TARGET, changeOrigin: true, secure: true },
      "/poll":    { target: SIGNALING_TARGET, changeOrigin: true, secure: true },
      "/ticket":  { target: SIGNALING_TARGET, changeOrigin: true, secure: true },
      "/session": { target: SIGNALING_TARGET, changeOrigin: true, secure: true },
      "/healthz": { target: SIGNALING_TARGET, changeOrigin: true, secure: true },
    },
  },
});
