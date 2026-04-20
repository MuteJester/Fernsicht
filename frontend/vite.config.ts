import { defineConfig } from "vite";

// Dev-only proxy: the production signaling server only sets CORS
// headers for https://app.fernsicht.space, so the dev server has to
// proxy /watch, /poll/, and /ticket/ to make the browser see them as
// same-origin. Set VITE_SERVER_URL=/ in .env.local to use this.
const SIGNALING_TARGET =
  process.env.VITE_DEV_SIGNALING_TARGET || "https://signal.fernsicht.space";

export default defineConfig({
  // GitHub Pages serves us at a custom apex (app.fernsicht.space via
  // CNAME). Override with VITE_BASE_PATH only if we ever host under a
  // path prefix.
  base: process.env.VITE_BASE_PATH || "/",
  build: {
    outDir: "dist",
    rollupOptions: {
      // Three-page build:
      //   index.html   — marketing landing.
      //   install.html — install docs + platform picker.
      //   app.html     — LIVE viewer runtime (new design, real WebRTC).
      //                  Root URL detects `role=viewer` in the fragment
      //                  and hands off here via an inline redirect, so
      //                  CLI-printed URLs of the form
      //                  `https://app.fernsicht.space/
      //                  #room=<id>&role=viewer` still resolve.
      input: {
        main:    "./index.html",
        install: "./install.html",
        app:     "./app.html",
      },
    },
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
