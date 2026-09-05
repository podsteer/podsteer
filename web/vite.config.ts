import { writeFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

/** Absolute path of the directory the Go binary embeds. */
const EMBED_DIR = fileURLToPath(new URL('../app/adapters/assets/dist', import.meta.url))

/**
 * Absolute path of the generated Wails bindings, behind the `$bindings` alias.
 *
 * `wails3 generate bindings` mirrors the Go IMPORT PATH under its output
 * directory — one folder per bound package — so the real location is
 * `src/lib/bindings/github.com/podsteer/podsteer/app/adapters/wails`. There is
 * no flag to flatten it, and there should not be: a project binding two
 * packages needs the distinction. The alias is what keeps that path out of
 * every import line, and it is declared identically here, in vitest.config.ts
 * and in tsconfig.json, the same way `$lib` already is.
 */
const BINDINGS_DIR = fileURLToPath(
  new URL('./src/lib/bindings/github.com/podsteer/podsteer/app/adapters/wails', import.meta.url),
)

/**
 * Keeps the embed directory tracked in git.
 *
 * `//go:embed all:dist` fails to compile if the directory does not exist, so a
 * fresh clone must contain it before anything has been built — which means git
 * has to track it, which means it needs a file in it. `emptyOutDir` deletes
 * that file on every build, so it is rewritten here once the bundle is closed.
 * Without this the placeholder shows up as deleted after each build, and a
 * clone of that state cannot `go build` at all.
 */
function keepEmbedDirectory(): Plugin {
  return {
    name: 'podsteer:keep-embed-directory',
    apply: 'build',
    closeBundle() {
      writeFileSync(`${EMBED_DIR}/.gitkeep`, '')
    },
  }
}

/**
 * Vite configuration for the PodSteer frontend.
 *
 * The unusual part is `build.outDir`. Go's embed directive cannot reach into a
 * parent directory, so the compiled bundle has to sit next to the Go package
 * that embeds it (app/adapters/assets). Source stays in web/, output goes to
 * app/ — which is also why `emptyOutDir` is set explicitly: Vite refuses to
 * clear a directory outside its root without it.
 */
export default defineConfig({
  plugins: [svelte(), tailwindcss(), keepEmbedDirectory(), tightenPolicy()],

  resolve: {
    alias: {
      $lib: fileURLToPath(new URL('./src/lib', import.meta.url)),
      $stores: fileURLToPath(new URL('./src/stores', import.meta.url)),
      $pages: fileURLToPath(new URL('./src/pages', import.meta.url)),
      $bindings: BINDINGS_DIR,
    },
  },

  build: {
    outDir: EMBED_DIR,
    emptyOutDir: true,

    // Every Wails target ships a current engine — WKWebView, WebView2 or
    // WebKitGTK — so there is no reason to down-level past ES2020. Shipping
    // fewer transpilation shims keeps the bundle, and the parse cost at
    // startup, smaller.
    target: 'es2020',

    // Sourcemaps would roughly double the embedded payload, and the binary
    // size is part of the point of this project. `make dev` serves from the
    // Vite dev server with maps, which is where debugging actually happens.
    sourcemap: false,

    reportCompressedSize: false,

    // Desktop app loads from embedded assets (not network), so the bundle size
    // warning threshold is higher than for web apps. CodeMirror + xterm.js
    // are large but essential for the YAML editor and terminal.
    chunkSizeWarningLimit: 1200,
  },

  server: {
    // The port `make dev` passes to `wails3 dev`, which turns it into the
    // FRONTEND_DEVSERVER_URL the Go side reads. The two have to agree, and
    // this is one of the two places the number is written — the Makefile's
    // `dev` target is the other.
    //
    // strictPort stays false so a port already in use falls back rather than
    // failing the whole loop; `wails3 dev` checks the port is free before it
    // starts anything, so the fallback is a nicety rather than a trap.
    port: 5173,
    strictPort: false,
  },
})

/**
 * Removes the dev server's WebSocket allowance from the SHIPPED page.
 *
 * `connect-src 'self' ws: wss:` is what Vite's hot reload needs, and a bare
 * scheme source is a wildcard: it permits a WebSocket to ANY host. That
 * allowance was shipping. CLAUDE.md names this policy as one of the two things
 * keeping the no-telemetry commitment honest — "no HTTP client outside
 * adapters/k8s" being the other — and a policy that permits arbitrary outbound
 * WebSockets from the webview does not keep it.
 *
 * Stripped at build rather than removed from index.html, because `make dev`
 * genuinely needs it and a policy nobody can develop under gets loosened back.
 * `app/adapters/assets/csp_test.go` asserts the result on the embedded bundle,
 * so the two cannot drift apart unnoticed.
 */
function tightenPolicy(): Plugin {
  return {
    name: 'podsteer:tighten-content-security-policy',
    apply: 'build',
    transformIndexHtml(html) {
      return html.replace(/connect-src 'self' ws: wss:/, "connect-src 'self'")
    },
  }
}
