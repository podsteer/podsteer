import { writeFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

/** Absolute path of the directory the Go binary embeds. */
const EMBED_DIR = fileURLToPath(new URL('../app/adapters/assets/dist', import.meta.url))

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
    name: 'k8sense:keep-embed-directory',
    apply: 'build',
    closeBundle() {
      writeFileSync(`${EMBED_DIR}/.gitkeep`, '')
    },
  }
}

/**
 * Vite configuration for the K8Sense frontend.
 *
 * The unusual part is `build.outDir`. Go's embed directive cannot reach into a
 * parent directory, so the compiled bundle has to sit next to the Go package
 * that embeds it (app/adapters/assets). Source stays in web/, output goes to
 * app/ — which is also why `emptyOutDir` is set explicitly: Vite refuses to
 * clear a directory outside its root without it.
 */
export default defineConfig({
  plugins: [svelte(), tailwindcss(), keepEmbedDirectory()],

  resolve: {
    alias: {
      $lib: fileURLToPath(new URL('./src/lib', import.meta.url)),
      $stores: fileURLToPath(new URL('./src/stores', import.meta.url)),
      $pages: fileURLToPath(new URL('./src/pages', import.meta.url)),
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
    // size is part of the point of this project. `wails dev` serves from the
    // dev server with maps, which is where debugging actually happens.
    sourcemap: false,

    reportCompressedSize: false,
  },

  server: {
    // Wails discovers this automatically via "frontend:dev:serverUrl": "auto",
    // but pinning the port keeps the dev URL predictable when attaching a
    // browser devtools session alongside the desktop window.
    port: 5173,
    strictPort: false,
  },
})
