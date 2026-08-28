// defineConfig comes from vitest/config rather than vite so the `test`
// block below is typed. Importing it from vite leaves that key untyped and
// a misspelled option silently does nothing.
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The dev server proxies /api to the Go binary rather than pointing the
// front end at http://localhost:8080 directly.
//
// Same-origin in development is worth the four lines: it is how the app is
// meant to be deployed (static files and the API behind one hostname), and
// developing against a cross-origin setup means every CORS mistake is
// discovered in production instead. The API answers preflights either way
// — see internal/web/cors.go — so this is about matching production, not
// about working around a missing header.
const goServer = process.env.QUEUEPILOT_API ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: goServer, changeOrigin: true },
    },
  },
  preview: {
    port: 4173,
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./vitest.setup.ts"],
    // The Playwright specs live in e2e/ and drive a real browser. Vitest
    // would happily collect them, fail on an import it cannot resolve in
    // jsdom, and report a broken unit suite for a suite that is not one.
    exclude: ["node_modules/**", "dist/**", "e2e/**"],
    css: false,
  },
});
