import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'
import { svelte } from '@sveltejs/vite-plugin-svelte'

/**
 * Component tests, run against a DOM rather than a browser.
 *
 * These exist because of a specific failure: DetailList keyed its rows by
 * label, Svelte throws on a duplicate key in a keyed each, and the whole pod
 * overview stopped rendering for every pod with two volume mounts — which is
 * every pod. `svelte-check` passed, the build passed, and the first report
 * came from somebody using the application.
 *
 * That is the class this suite is for: things that compile and type-check and
 * then throw at runtime. It is deliberately not an end-to-end harness — the
 * backend is a Wails binding surface that does not exist in a browser, so
 * anything reaching for it belongs in a Go test instead.
 *
 * happy-dom rather than jsdom: it is several times faster to start, and
 * nothing here needs the corners of the DOM where they differ.
 */
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: {
      $lib: fileURLToPath(new URL('./src/lib', import.meta.url)),
      $stores: fileURLToPath(new URL('./src/stores', import.meta.url)),
      $pages: fileURLToPath(new URL('./src/pages', import.meta.url)),
    },
    // The browser build of Svelte, so components mount and update rather than
    // render once to a string.
    conditions: ['browser'],
  },
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'],
  },
})
