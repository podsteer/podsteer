import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

/**
 * Svelte 5 configuration. vitePreprocess enables <script lang="ts"> and
 * lets the Tailwind Vite plugin process <style> blocks.
 */
export default {
  preprocess: vitePreprocess(),
}
